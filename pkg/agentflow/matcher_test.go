package agentflow_test

import (
	"fmt"
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/agentflow"
)

type mockEmbeddingBackend struct {
	embeddings map[string][]float32
}

func (m *mockEmbeddingBackend) CalculateEmbedding(sentence string) ([]float32, error) {
	if emb, ok := m.embeddings[sentence]; ok {
		return emb, nil
	}
	// Simple deterministic mock vector based on length
	return []float32{float32(len(sentence)), 1.0}, nil
}

func (m *mockEmbeddingBackend) Distance(embA, embB []float32) (float32, error) {
	sim := agentflow.CosineSimilarity(embA, embB)
	return sim, nil
}

func TestPhraseMatcher_SubstringFallback(t *testing.T) {
	matcher := agentflow.NewPhraseMatcher(nil)

	groups := []agentflow.PhraseGroup{
		{Key: "wifi", Phrases: []string{"set up wifi", "wifi setup"}},
		{Key: "lights", Phrases: []string{"turn on lights", "lights on"}},
	}

	tests := []struct {
		utterance string
		expected  string
	}{
		{"I want to set up wifi please", "wifi"},
		{"can you turn on lights now", "lights"},
		{"something unrelated", ""},
	}

	for _, tt := range tests {
		got := matcher.Match(tt.utterance, groups, 0.7)
		if got != tt.expected {
			t.Errorf("Match(%q) = %q; want %q", tt.utterance, got, tt.expected)
		}
	}
}

func TestPhraseMatcher_WithMockEmbedding(t *testing.T) {
	backend := &mockEmbeddingBackend{
		embeddings: map[string][]float32{
			"set up wifi":   {1.0, 0.0},
			"wifi connect":  {0.9, 0.1},
			"turn on light": {0.0, 1.0},
		},
	}

	matcher := agentflow.NewPhraseMatcher(backend)

	groups := []agentflow.PhraseGroup{
		{Key: "wifi", Phrases: []string{"set up wifi"}},
		{Key: "lights", Phrases: []string{"turn on light"}},
	}

	// "wifi connect" vector {0.9, 0.1} is close to "set up wifi" {1.0, 0.0}
	matched := matcher.Match("wifi connect", groups, 0.7)
	if matched != "wifi" {
		t.Errorf("expected wifi, got %q", matched)
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1.0, 0.0}
	b := []float32{1.0, 0.0}
	sim := agentflow.CosineSimilarity(a, b)
	if fmt.Sprintf("%.2f", sim) != "1.00" {
		t.Errorf("expected 1.00, got %f", sim)
	}

	c := []float32{0.0, 1.0}
	simOrth := agentflow.CosineSimilarity(a, c)
	if fmt.Sprintf("%.2f", simOrth) != "0.00" {
		t.Errorf("expected 0.00, got %f", simOrth)
	}
}
