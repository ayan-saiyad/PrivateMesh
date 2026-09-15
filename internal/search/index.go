// Package search provides local document indexing and query execution.
package search

import (
	"errors"
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

type posting struct {
	titleFrequency uint64
	bodyFrequency  uint64
}

type fieldLengths struct {
	title uint64
	body  uint64
}

// Index stores documents and their term postings in memory.
type Index struct {
	mu              sync.RWMutex
	documents       map[string]Document
	postings        map[string]map[string]posting
	documentTerms   map[string]map[string]struct{}
	lengths         map[string]fieldLengths
	totalTitleTerms uint64
	totalBodyTerms  uint64
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	index := &Index{}
	index.initialize()
	return index
}

// Upsert adds a document or replaces the existing document with the same ID.
func (i *Index) Upsert(document Document) error {
	document.ID = strings.TrimSpace(document.ID)
	if document.ID == "" {
		return ErrDocumentIDRequired
	}

	titleTerms, titleLength := analyze(document.Title)
	bodyTerms, bodyLength := analyze(document.Body)
	terms := mergeTerms(titleTerms, bodyTerms)

	i.mu.Lock()
	defer i.mu.Unlock()
	i.initialize()

	i.deleteLocked(document.ID)
	i.documents[document.ID] = document
	i.documentTerms[document.ID] = terms
	i.lengths[document.ID] = fieldLengths{title: titleLength, body: bodyLength}
	i.totalTitleTerms += titleLength
	i.totalBodyTerms += bodyLength

	for term := range terms {
		if i.postings[term] == nil {
			i.postings[term] = make(map[string]posting)
		}
		i.postings[term][document.ID] = posting{
			titleFrequency: titleTerms[term],
			bodyFrequency:  bodyTerms[term],
		}
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

func (i *Index) initialize() {
	if i.documents == nil {
		i.documents = make(map[string]Document)
	}
	if i.postings == nil {
		i.postings = make(map[string]map[string]posting)
	}
	if i.documentTerms == nil {
		i.documentTerms = make(map[string]map[string]struct{})
	}
	if i.lengths == nil {
		i.lengths = make(map[string]fieldLengths)
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

	lengths := i.lengths[id]
	i.totalTitleTerms -= lengths.title
	i.totalBodyTerms -= lengths.body
	delete(i.lengths, id)
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

func analyze(text string) (map[string]uint64, uint64) {
	tokens := tokenize(text)
	frequencies := make(map[string]uint64, len(tokens))
	var length uint64
	for _, term := range tokens {
		frequencies[term]++
		length++
	}
	return frequencies, length
}

func mergeTerms(fields ...map[string]uint64) map[string]struct{} {
	terms := make(map[string]struct{})
	for _, field := range fields {
		for term := range field {
			terms[term] = struct{}{}
		}
	}
	return terms
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
