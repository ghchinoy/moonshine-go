package moonshine

import (
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/agentflow"
)

// Ensure EmbeddingModel satisfies agentflow.EmbeddingBackend at compile time.
var _ agentflow.EmbeddingBackend = (*EmbeddingModel)(nil)

func TestEmbeddingModel_NotLoaded(t *testing.T) {
	if Loaded() {
		t.Skip("skipping native-free test since library is loaded")
	}

	_, err := NewEmbeddingModel("/tmp/fake-model", EmbeddingModelArchGemma300M, "q4")
	if err != errNotLoaded {
		t.Errorf("NewEmbeddingModel = %v, want %v", err, errNotLoaded)
	}

	files := map[string][]byte{"model_q4.ort": {0x1, 0x2}, "tokenizer.bin": {0x3}}
	_, err = NewEmbeddingModelFromMemory(EmbeddingModelArchGemma300M, "q4", files)
	if err != errNotLoaded {
		t.Errorf("NewEmbeddingModelFromMemory = %v, want %v", err, errNotLoaded)
	}

	_, err = GetEmbeddingDependencies("embeddinggemma-300m")
	if err != errNotLoaded {
		t.Errorf("GetEmbeddingDependencies = %v, want %v", err, errNotLoaded)
	}
}

func TestEmbeddingModel_Validation(t *testing.T) {
	_, err := NewEmbeddingModel("", EmbeddingModelArchGemma300M, "q4")
	if err == nil {
		t.Error("NewEmbeddingModel(empty modelDir) = nil, want error")
	}

	_, err = NewEmbeddingModelFromMemory(EmbeddingModelArchGemma300M, "q4", nil)
	if err == nil {
		t.Error("NewEmbeddingModelFromMemory(nil files) = nil, want error")
	}

	_, err = NewEmbeddingModelFromMemory(EmbeddingModelArchGemma300M, "q4", map[string][]byte{})
	if err == nil {
		t.Error("NewEmbeddingModelFromMemory(empty files) = nil, want error")
	}
}

func TestEmbeddingModel_ClosedAndDistanceValidation(t *testing.T) {
	m := &EmbeddingModel{closed: true}

	if _, err := m.CalculateEmbedding("test sentence"); err == nil {
		t.Error("CalculateEmbedding on closed model should return error")
	}

	if _, err := m.Distance([]float32{1.0}, []float32{1.0}); err == nil {
		t.Error("Distance on closed model should return error")
	}

	mOpen := &EmbeddingModel{closed: false}
	if !Loaded() {
		// When not loaded, validation checks before FFI
		if _, err := mOpen.Distance(nil, []float32{1.0}); err == nil {
			t.Error("Distance(nil, ...) should return error")
		}
		if _, err := mOpen.Distance([]float32{1.0}, []float32{1.0, 2.0}); err == nil {
			t.Error("Distance with mismatched lengths should return error")
		}
	}

	if err := m.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil", err)
	}
}
