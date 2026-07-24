package serveapi

import (
	"context"
	"strings"
)

// Result represents a single lookup or search match returned by a Retriever.
type Result struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Source  string `json:"source,omitempty"`
}

// Retriever is the interface for lookup/RAG tools the agent layer can call to
// ground a response.
type Retriever interface {
	Retrieve(ctx context.Context, query string) ([]Result, error)
}

// NoopRetriever is a Retriever that returns no results. It is the default when
// no retrieval backend is configured.
type NoopRetriever struct{}

// Retrieve satisfies Retriever, returning no results.
func (NoopRetriever) Retrieve(ctx context.Context, query string) ([]Result, error) {
	return nil, nil
}

// StaticRetriever is an in-memory Retriever populated with static Results,
// for testing and demos.
type StaticRetriever struct {
	items []Result
}

// NewStaticRetriever creates a StaticRetriever pre-populated with items.
func NewStaticRetriever(items ...Result) *StaticRetriever {
	return &StaticRetriever{items: items}
}

// Retrieve performs a case-insensitive substring search across Title,
// Snippet, and Source. An empty (or all-whitespace) query returns no results
// rather than the full set, so an accidental empty query doesn't silently
// dump every item.
func (s *StaticRetriever) Retrieve(ctx context.Context, query string) ([]Result, error) {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil, nil
	}

	var matched []Result
	for _, item := range s.items {
		if strings.Contains(strings.ToLower(item.Title), q) ||
			strings.Contains(strings.ToLower(item.Snippet), q) ||
			strings.Contains(strings.ToLower(item.Source), q) {
			matched = append(matched, item)
		}
	}
	return matched, nil
}
