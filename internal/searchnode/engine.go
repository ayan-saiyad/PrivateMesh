// Package searchnode owns durable documents, local indexes, and policy enforcement.
package searchnode

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ayansaiyad/privatemesh/internal/authorization"
	"github.com/ayansaiyad/privatemesh/internal/identity"
	"github.com/ayansaiyad/privatemesh/internal/search"
	"github.com/ayansaiyad/privatemesh/internal/segment"
	"github.com/ayansaiyad/privatemesh/internal/vector"
	"github.com/ayansaiyad/privatemesh/internal/wal"
)

const snippetRunes = 180

// RetrievalMode selects a local retrieval strategy.
type RetrievalMode uint8

const (
	// RetrievalLexical searches term postings.
	RetrievalLexical RetrievalMode = iota + 1
	// RetrievalVector searches document embeddings.
	RetrievalVector
	// RetrievalHybrid fuses term and vector rankings.
	RetrievalHybrid
)

// Document is a stored document and its response media type.
type Document struct {
	ID        string
	Title     string
	Content   string
	MediaType string
}

// Hit is an authorized local search result.
type Hit struct {
	DocumentID string
	Title      string
	Snippet    string
	Score      float64
}

// Options configures one collection engine.
type Options struct {
	CollectionID  string
	DataDirectory string
	Dimensions    int
	Embedder      vector.Embedder
	Authorizer    *authorization.Enforcer
}

// Engine provides durable, locally authorized document operations.
type Engine struct {
	mu           sync.RWMutex
	collectionID string
	log          *wal.Log
	lexical      *search.Index
	vectors      *vector.ExactIndex
	embedder     vector.Embedder
	authorizer   *authorization.Enforcer
	documents    map[string]Document
	offset       uint64
}

// Open recovers a collection from its write-ahead log.
func Open(ctx context.Context, options Options) (*Engine, error) {
	options.CollectionID = strings.TrimSpace(options.CollectionID)
	options.DataDirectory = strings.TrimSpace(options.DataDirectory)
	if options.CollectionID == "" || options.DataDirectory == "" {
		return nil, errors.New("collection ID and data directory are required")
	}
	if options.Embedder == nil || options.Authorizer == nil {
		return nil, errors.New("embedder and authorizer are required")
	}
	vectors, err := vector.NewExactIndex(options.Dimensions)
	if err != nil {
		return nil, err
	}
	log, err := wal.Open(filepath.Join(options.DataDirectory, options.CollectionID, "mutations.wal"))
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		collectionID: options.CollectionID,
		log:          log,
		lexical:      search.NewIndex(),
		vectors:      vectors,
		embedder:     options.Embedder,
		authorizer:   options.Authorizer,
		documents:    make(map[string]Document),
	}
	entries, err := log.Replay(ctx)
	if err != nil {
		_ = log.Close()
		return nil, fmt.Errorf("replay collection log: %w", err)
	}
	for _, entry := range entries {
		if err := engine.apply(ctx, entry.Mutation); err != nil {
			_ = log.Close()
			return nil, fmt.Errorf("recover document at offset %d: %w", entry.Sequence, err)
		}
		engine.offset = entry.Sequence
	}
	return engine, nil
}

// CollectionID returns the collection owned by the engine.
func (e *Engine) CollectionID() string {
	return e.collectionID
}

// Offset returns the latest committed mutation offset.
func (e *Engine) Offset() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.offset
}

// Upsert durably adds or replaces a document.
func (e *Engine) Upsert(ctx context.Context, document Document) (uint64, error) {
	document.ID = strings.TrimSpace(document.ID)
	document.MediaType = strings.TrimSpace(document.MediaType)
	if document.ID == "" {
		return 0, errors.New("document ID is required")
	}
	if document.MediaType == "" {
		document.MediaType = "text/plain; charset=utf-8"
	}
	mutation := segment.Upsert(search.Document{ID: document.ID, Title: document.Title, Body: document.Content})
	e.mu.Lock()
	defer e.mu.Unlock()
	sequence, err := e.log.Append(mutation)
	if err != nil {
		return 0, err
	}
	if err := e.log.Commit(sequence); err != nil {
		return 0, err
	}
	if err := e.applyDocument(ctx, document); err != nil {
		return 0, err
	}
	e.offset = sequence
	return sequence, nil
}

// Delete durably removes a document.
func (e *Engine) Delete(_ context.Context, documentID string) (uint64, error) {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		return 0, errors.New("document ID is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	sequence, err := e.log.Append(segment.Delete(documentID))
	if err != nil {
		return 0, err
	}
	if err := e.log.Commit(sequence); err != nil {
		return 0, err
	}
	e.lexical.Delete(documentID)
	e.vectors.Delete(documentID)
	delete(e.documents, documentID)
	e.offset = sequence
	return sequence, nil
}

// Search executes a local retrieval strategy and removes unauthorized candidates.
func (e *Engine) Search(
	ctx context.Context,
	requestID, query string,
	mode RetrievalMode,
	limit int,
	principal identity.Principal,
) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if strings.TrimSpace(requestID) == "" || query == "" || limit <= 0 {
		return nil, errors.New("request ID, query, and positive limit are required")
	}
	if mode != RetrievalLexical && mode != RetrievalVector && mode != RetrievalHybrid {
		return nil, errors.New("unsupported retrieval mode")
	}

	e.mu.RLock()
	defer e.mu.RUnlock()
	lexicalResults, err := e.lexical.Search(query, search.MatchAny, 0)
	if err != nil {
		return nil, err
	}
	lexicalIDs := make([]string, len(lexicalResults))
	lexicalScores := make(map[string]float64, len(lexicalResults))
	for index, result := range lexicalResults {
		lexicalIDs[index] = result.Document.ID
		lexicalScores[result.Document.ID] = result.Score
	}
	vectorIDs, vectorScores, err := e.vectorSearch(ctx, query)
	if err != nil {
		return nil, err
	}

	var ranked []rankedID
	switch mode {
	case RetrievalLexical:
		ranked = scoresToRanking(lexicalIDs, lexicalScores)
	case RetrievalVector:
		ranked = scoresToRanking(vectorIDs, vectorScores)
	case RetrievalHybrid:
		fused := vector.ReciprocalRankFusion([][]string{lexicalIDs, vectorIDs}, 0)
		ranked = make([]rankedID, len(fused))
		for index, result := range fused {
			ranked[index] = rankedID{id: result.ID, score: result.Score}
		}
	}

	hits := make([]Hit, 0, min(limit, len(ranked)))
	for _, candidate := range ranked {
		decision, err := e.authorizer.Authorize(ctx, authorization.Request{
			RequestID: requestID, Principal: principal, Action: authorization.ActionSearch,
			CollectionID: e.collectionID, DocumentID: candidate.id,
		})
		if err != nil {
			return nil, err
		}
		if !decision.Allowed {
			continue
		}
		document, ok := e.documents[candidate.id]
		if !ok {
			continue
		}
		hits = append(hits, Hit{
			DocumentID: document.ID,
			Title:      document.Title,
			Snippet:    snippet(document.Content),
			Score:      candidate.score,
		})
		if len(hits) == limit {
			break
		}
	}
	return hits, nil
}

// Get returns a full document after local read authorization.
func (e *Engine) Get(
	ctx context.Context,
	requestID, documentID string,
	principal identity.Principal,
) (Document, bool, error) {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(documentID) == "" {
		return Document{}, false, errors.New("request and document IDs are required")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	document, found := e.documents[strings.TrimSpace(documentID)]
	if !found {
		return Document{}, false, nil
	}
	decision, err := e.authorizer.Authorize(ctx, authorization.Request{
		RequestID: requestID, Principal: principal, Action: authorization.ActionRead,
		CollectionID: e.collectionID, DocumentID: document.ID,
	})
	if err != nil {
		return Document{}, false, err
	}
	if !decision.Allowed {
		return Document{}, false, authorization.ErrPermissionDenied
	}
	return document, true, nil
}

// Close closes the collection log.
func (e *Engine) Close() error {
	return e.log.Close()
}

func (e *Engine) apply(ctx context.Context, mutation segment.Mutation) error {
	if mutation.Deleted {
		e.lexical.Delete(mutation.Document.ID)
		e.vectors.Delete(mutation.Document.ID)
		delete(e.documents, mutation.Document.ID)
		return nil
	}
	return e.applyDocument(ctx, Document{
		ID: mutation.Document.ID, Title: mutation.Document.Title, Content: mutation.Document.Body,
		MediaType: "text/plain; charset=utf-8",
	})
}

func (e *Engine) applyDocument(ctx context.Context, document Document) error {
	vectors, err := e.embedder.Embed(ctx, []string{document.Title + "\n" + document.Content})
	if err != nil {
		return fmt.Errorf("embed document: %w", err)
	}
	if len(vectors) != 1 {
		return errors.New("embedder returned an unexpected vector count")
	}
	if err := e.lexical.Upsert(search.Document{ID: document.ID, Title: document.Title, Body: document.Content}); err != nil {
		return err
	}
	if err := e.vectors.Upsert(vector.Item{ID: document.ID, Vector: vectors[0]}); err != nil {
		return err
	}
	e.documents[document.ID] = document
	return nil
}

func (e *Engine) vectorSearch(ctx context.Context, query string) ([]string, map[string]float64, error) {
	vectors, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, nil, errors.New("embedder returned an unexpected vector count")
	}
	results, err := e.vectors.Search(vectors[0], 0)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(results))
	scores := make(map[string]float64, len(results))
	for index, result := range results {
		ids[index] = result.ID
		scores[result.ID] = float64(result.Score)
	}
	return ids, scores, nil
}

type rankedID struct {
	id    string
	score float64
}

func scoresToRanking(ids []string, scores map[string]float64) []rankedID {
	ranked := make([]rankedID, len(ids))
	for index, id := range ids {
		ranked[index] = rankedID{id: id, score: scores[id]}
	}
	return ranked
}

func snippet(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	if utf8.RuneCountInString(content) <= snippetRunes {
		return content
	}
	runes := []rune(content)
	return strings.TrimSpace(string(runes[:snippetRunes])) + "…"
}
