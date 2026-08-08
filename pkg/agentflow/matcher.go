package agentflow

import (
	"strings"
	"sync"
)

// PhraseGroup defines a key and the candidate phrases that select it.
type PhraseGroup struct {
	Key     string
	Phrases []string
}

// PhraseMatcher scores utterances against candidate phrase groups by meaning.
// When an EmbeddingBackend is provided, each phrase is embedded once and cached,
// and the key of the best-scoring phrase at or above threshold wins.
// Without an EmbeddingBackend, it falls back to case-insensitive exact and
// substring matching (allowing dialogs to run before model loading).
type PhraseMatcher struct {
	model EmbeddingBackend
	mu    sync.Mutex
	cache map[string][]float32
}

// NewPhraseMatcher creates a PhraseMatcher with an optional EmbeddingBackend.
func NewPhraseMatcher(model EmbeddingBackend) *PhraseMatcher {
	return &PhraseMatcher{
		model: model,
		cache: make(map[string][]float32),
	}
}

// Match returns the best-matching group key, or "" when no phrase clears threshold.
func (m *PhraseMatcher) Match(utterance string, groups []PhraseGroup, threshold float32) string {
	utterance = strings.TrimSpace(utterance)
	if utterance == "" || len(groups) == 0 {
		return ""
	}
	if m == nil || m.model == nil {
		return m.matchSubstring(utterance, groups)
	}

	uttEmb, err := m.model.CalculateEmbedding(utterance)
	if err != nil || len(uttEmb) == 0 {
		return m.matchSubstring(utterance, groups)
	}

	bestKey := ""
	bestScore := float32(-1.0)

	for _, group := range groups {
		for _, phrase := range group.Phrases {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			phraseEmb := m.getOrComputeEmbedding(phrase)
			if len(phraseEmb) == 0 {
				continue
			}

			score, err := m.model.Distance(uttEmb, phraseEmb)
			if err != nil {
				score = CosineSimilarity(uttEmb, phraseEmb)
			}

			if score > bestScore {
				bestScore = score
				bestKey = group.Key
			}
		}
	}

	if bestScore >= threshold {
		return bestKey
	}
	return ""
}

// MatchPhrases treats each phrase as its own key and returns the best matching phrase.
func (m *PhraseMatcher) MatchPhrases(utterance string, phrases []string, threshold float32) string {
	groups := make([]PhraseGroup, len(phrases))
	for i, p := range phrases {
		groups[i] = PhraseGroup{Key: p, Phrases: []string{p}}
	}
	return m.Match(utterance, groups, threshold)
}

func (m *PhraseMatcher) matchSubstring(utterance string, groups []PhraseGroup) string {
	lowerUtt := strings.ToLower(utterance)
	for _, group := range groups {
		for _, phrase := range group.Phrases {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			lowerPhrase := strings.ToLower(phrase)
			if lowerUtt == lowerPhrase || strings.Contains(lowerUtt, lowerPhrase) {
				return group.Key
			}
		}
	}
	return ""
}

func (m *PhraseMatcher) getOrComputeEmbedding(phrase string) []float32 {
	m.mu.Lock()
	if cached, ok := m.cache[phrase]; ok {
		m.mu.Unlock()
		return cached
	}
	m.mu.Unlock()

	emb, err := m.model.CalculateEmbedding(phrase)
	if err != nil || len(emb) == 0 {
		return nil
	}

	m.mu.Lock()
	m.cache[phrase] = emb
	m.mu.Unlock()

	return emb
}
