package moonshine

import (
	"testing"
)

func TestNewSynthesizerFromClone_NotLoaded(t *testing.T) {
	if Loaded() {
		t.Skip("skipping native-free test since library is loaded")
	}
	pcm := []float32{0.1, 0.2, 0.3}
	_, err := NewSynthesizerFromClone("en_us", pcm, 16000, "hello world")
	if err != errNotLoaded {
		t.Errorf("NewSynthesizerFromClone = %v, want %v", err, errNotLoaded)
	}
}

func TestNewSynthesizerFromClone_Validation(t *testing.T) {
	// Set loaded flag state for validation checks if needed
	_, errEmpty := NewSynthesizerFromClone("en_us", nil, 16000, "hello")
	if errEmpty == nil {
		t.Error("NewSynthesizerFromClone(nil PCM) = nil error, want error")
	}

	_, errRate := NewSynthesizerFromClone("en_us", []float32{0.1}, 0, "hello")
	if errRate == nil {
		t.Error("NewSynthesizerFromClone(0 sampleRate) = nil error, want error")
	}
}
