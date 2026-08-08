package agentflow

// EmbeddingBackend is the interface required for vector-based semantic phrase
// matching in PhraseMatcher. It is satisfied by pkg/moonshine.EmbeddingModel.
type EmbeddingBackend interface {
	// CalculateEmbedding computes a normalized float32 feature vector for sentence.
	CalculateEmbedding(sentence string) ([]float32, error)

	// Distance calculates cosine similarity between two embedding vectors in [-1, 1].
	Distance(embA, embB []float32) (float32, error)
}

// CosineSimilarity calculates cosine similarity between two float32 slices.
// Returns 0 if vectors have different lengths or zero norm.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (sqrt(normA) * sqrt(normB))
}

func sqrt(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Newton-Raphson approximation for float32 square root (stdlib math uses float64)
	z := x
	for i := 0; i < 10; i++ {
		z = 0.5 * (z + x/z)
	}
	return z
}
