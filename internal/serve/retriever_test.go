package serve_test

import (
	"context"
	"testing"

	"github.com/ghchinoy/moonshine-go/internal/serve"
)

func TestNoopRetriever(t *testing.T) {
	var r serve.NoopRetriever
	res, err := r.Retrieve(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected empty results, got %v", res)
	}
}

func TestStaticRetriever(t *testing.T) {
	r := serve.NewStaticRetriever(
		serve.Result{Title: "Moonshine Voice", Snippet: "Fast STT pipeline", Source: "docs/readme"},
		serve.Result{Title: "Gemini Agent", Snippet: "Agentic sidecar for voice", Source: "docs/sidecar"},
	)
	ctx := context.Background()

	t.Run("matching query", func(t *testing.T) {
		results, err := r.Retrieve(ctx, "Moonshine")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Title != "Moonshine Voice" {
			t.Errorf("unexpected title %q", results[0].Title)
		}
	})

	t.Run("matching snippet", func(t *testing.T) {
		results, err := r.Retrieve(ctx, "sidecar")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Title != "Gemini Agent" {
			t.Errorf("unexpected title %q", results[0].Title)
		}
	})

	t.Run("non-matching query", func(t *testing.T) {
		results, err := r.Retrieve(ctx, "nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("empty query", func(t *testing.T) {
		results, err := r.Retrieve(ctx, "   ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})
}
