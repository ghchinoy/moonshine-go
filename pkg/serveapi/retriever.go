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

// StaticRetriever is a Retriever backed by a fixed set of results, returning
// those whose Title or Snippet contains the (case-insensitive) query. Useful
// for demos, tests, and small fixed knowledge sets.
type StaticRetriever struct {
	items []Result
}

// NewStaticRetriever creates a StaticRetriever over the given items.
func NewStaticRetriever(items ...Result) *StaticRetriever {
	return &StaticRetriever{items: items}
}

// Retrieve returns items whose Title or Snippet contains query
// (case-insensitive). An empty query returns all items.
func (s *StaticRetriever) Retrieve(ctx context.Context, query string) ([]Result, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return append([]Result(nil), s.items...), nil
	}
	var out []Result
	for _, it := range s.items {
		if strings.Contains(strings.ToLower(it.Title), q) ||
			strings.Contains(strings.ToLower(it.Snippet), q) {
			out = append(out, it)
		}
	}
	return out, nil
}
