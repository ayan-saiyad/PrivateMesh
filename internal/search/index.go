// Package search provides local document indexing and query execution.
package search

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrDocumentIDRequired indicates that a document has no usable identifier.
	ErrDocumentIDRequired = errors.New("document ID is required")
	// ErrInvalidMatchMode indicates that a query uses an unsupported term operator.
	ErrInvalidMatchMode = errors.New("invalid match mode")
	// ErrInvalidLimit indicates that a query limit is negative.
	ErrInvalidLimit = errors.New("limit cannot be negative")
)

// Document is the searchable content owned by a search node.
type Document struct {
	ID    string
	Title string
	Body  string
}

// MatchMode controls how query terms are combined.
type MatchMode uint8

const (
	// MatchAll requires every query term to occur in a document.
	MatchAll MatchMode = iota + 1
	// MatchAny requires at least one query term to occur in a document.
	MatchAny
)

// Index stores documents and their term postings in memory.
type Index struct {
	mu            sync.RWMutex
	documents     map[string]Document
	postings      map[string]map[string]uint32
	documentTerms map[string]map[string]uint32
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{
		documents:     make(map[string]Document),
		postings:      make(map[string]map[string]uint32),
		documentTerms: make(map[string]map[string]uint32),
	}
}

// Upsert adds a document or replaces the existing document with the same ID.
func (i *Index) Upsert(document Document) error {
	document.ID = strings.TrimSpace(document.ID)
	if document.ID == "" {
		return ErrDocumentIDRequired
	}

	terms := termFrequencies(document.Title + "\n" + document.Body)

	i.mu.Lock()
	defer i.mu.Unlock()
	i.initialize()

	i.deleteLocked(document.ID)
	i.documents[document.ID] = document
	i.documentTerms[document.ID] = terms
	for term, frequency := range terms {
		if i.postings[term] == nil {
			i.postings[term] = make(map[string]uint32)
		}
		i.postings[term][document.ID] = frequency
	}

	return nil
}

// Delete removes a document and reports whether it existed.
func (i *Index) Delete(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	return i.deleteLocked(id)
}

// Get returns a document by ID.
func (i *Index) Get(id string) (Document, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	document, ok := i.documents[strings.TrimSpace(id)]
	return document, ok
}

// Len returns the number of indexed documents.
func (i *Index) Len() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.documents)
}

// Search returns matching documents ordered by ID. A zero limit returns every match.
func (i *Index) Search(query string, mode MatchMode, limit int) ([]Document, error) {
	if mode != MatchAll && mode != MatchAny {
		return nil, ErrInvalidMatchMode
	}
	if limit < 0 {
		return nil, ErrInvalidLimit
	}

	terms := uniqueTerms(tokenize(query))
	if len(terms) == 0 {
		return []Document{}, nil
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	matches := i.matchingDocuments(terms, mode)
	ids := make([]string, 0, len(matches))
	for id := range matches {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}

	documents := make([]Document, 0, len(ids))
	for _, id := range ids {
		documents = append(documents, i.documents[id])
	}
	return documents, nil
}

func (i *Index) initialize() {
	if i.documents == nil {
		i.documents = make(map[string]Document)
	}
	if i.postings == nil {
		i.postings = make(map[string]map[string]uint32)
	}
	if i.documentTerms == nil {
		i.documentTerms = make(map[string]map[string]uint32)
	}
}

func (i *Index) deleteLocked(id string) bool {
	if _, ok := i.documents[id]; !ok {
		return false
	}

	for term := range i.documentTerms[id] {
		delete(i.postings[term], id)
		if len(i.postings[term]) == 0 {
			delete(i.postings, term)
		}
	}
	delete(i.documentTerms, id)
	delete(i.documents, id)
	return true
}

func (i *Index) matchingDocuments(terms []string, mode MatchMode) map[string]struct{} {
	matches := make(map[string]struct{})

	if mode == MatchAny {
		for _, term := range terms {
			for id := range i.postings[term] {
				matches[id] = struct{}{}
			}
		}
		return matches
	}

	first := i.postings[terms[0]]
	for id := range first {
		matches[id] = struct{}{}
	}
	for _, term := range terms[1:] {
		for id := range matches {
			if _, ok := i.postings[term][id]; !ok {
				delete(matches, id)
			}
		}
		if len(matches) == 0 {
			break
		}
	}
	return matches
}

func termFrequencies(text string) map[string]uint32 {
	frequencies := make(map[string]uint32)
	for _, term := range tokenize(text) {
		frequencies[term]++
	}
	return frequencies
}

func uniqueTerms(terms []string) []string {
	seen := make(map[string]struct{}, len(terms))
	unique := make([]string, 0, len(terms))
	for _, term := range terms {
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		unique = append(unique, term)
	}
	return unique
}
