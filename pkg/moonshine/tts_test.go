package moonshine

import (
	"testing"
	"unsafe"
)

func TestTTSChunkLayout(t *testing.T) {
	var chunk cTTSChunk
	if size := unsafe.Sizeof(chunk); size != 48 {
		t.Fatalf("sizeof(cTTSChunk) = %d, want 48", size)
	}
	if off := unsafe.Offsetof(chunk.audioData); off != 0 {
		t.Errorf("offsetof(audioData) = %d, want 0", off)
	}
	if off := unsafe.Offsetof(chunk.audioDataCount); off != 8 {
		t.Errorf("offsetof(audioDataCount) = %d, want 8", off)
	}
	if off := unsafe.Offsetof(chunk.sampleRate); off != 16 {
		t.Errorf("offsetof(sampleRate) = %d, want 16", off)
	}
	if off := unsafe.Offsetof(chunk.text); off != 24 {
		t.Errorf("offsetof(text) = %d, want 24", off)
	}
	if off := unsafe.Offsetof(chunk.utteranceID); off != 32 {
		t.Errorf("offsetof(utteranceID) = %d, want 32", off)
	}
	if off := unsafe.Offsetof(chunk.isFinal); off != 40 {
		t.Errorf("offsetof(isFinal) = %d, want 40", off)
	}
}

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

func TestSynthesizer_Streaming_NotLoaded(t *testing.T) {
	if Loaded() {
		t.Skip("skipping native-free test since library is loaded")
	}
	s := &Synthesizer{language: "en_us"}
	if err := s.PushText("hello"); err != errNotLoaded {
		t.Errorf("PushText = %v, want %v", err, errNotLoaded)
	}
	if err := s.Flush(); err != errNotLoaded {
		t.Errorf("Flush = %v, want %v", err, errNotLoaded)
	}
	if err := s.EndInput(); err != errNotLoaded {
		t.Errorf("EndInput = %v, want %v", err, errNotLoaded)
	}
	if err := s.Cancel(); err != errNotLoaded {
		t.Errorf("Cancel = %v, want %v", err, errNotLoaded)
	}
	if chunk, err := s.NextChunk(); err != errNotLoaded || chunk != nil {
		t.Errorf("NextChunk = (%v, %v), want (nil, %v)", chunk, err, errNotLoaded)
	}
	if _, err := s.SplitUtterances("Hello world."); err != errNotLoaded {
		t.Errorf("SplitUtterances = %v, want %v", err, errNotLoaded)
	}
	if _, err := SplitUtterances("en_us", "Hello world."); err != errNotLoaded {
		t.Errorf("SplitUtterances standalone = %v, want %v", err, errNotLoaded)
	}
	if s.IsStreaming() {
		t.Errorf("IsStreaming = true, want false")
	}
}

func TestSynthesizer_Streaming_Closed(t *testing.T) {
	s := &Synthesizer{closed: true, language: "en_us"}
	if err := s.PushText("hello"); err == nil {
		t.Error("PushText on closed synthesizer should return error")
	}
	if err := s.Flush(); err == nil {
		t.Error("Flush on closed synthesizer should return error")
	}
	if err := s.EndInput(); err == nil {
		t.Error("EndInput on closed synthesizer should return error")
	}
	if err := s.Cancel(); err == nil {
		t.Error("Cancel on closed synthesizer should return error")
	}
	if _, err := s.NextChunk(); err == nil {
		t.Error("NextChunk on closed synthesizer should return error")
	}
	if s.IsStreaming() {
		t.Errorf("IsStreaming on closed synthesizer = true, want false")
	}
}

func TestTTSStream_Wrapper(t *testing.T) {
	s := &Synthesizer{closed: true, language: "en_us"}
	st := s.NewStream()
	if st == nil {
		t.Fatal("NewStream returned nil")
	}
	if err := st.PushText("test"); err == nil {
		t.Error("st.PushText should fail on closed synth")
	}
	if err := st.Flush(); err == nil {
		t.Error("st.Flush should fail on closed synth")
	}
	if err := st.EndInput(); err == nil {
		t.Error("st.EndInput should fail on closed synth")
	}
	if err := st.Cancel(); err == nil {
		t.Error("st.Cancel should fail on closed synth")
	}
	if _, err := st.NextChunk(); err == nil {
		t.Error("st.NextChunk should fail on closed synth")
	}
	if st.IsStreaming() {
		t.Error("st.IsStreaming on closed synth = true, want false")
	}
}
