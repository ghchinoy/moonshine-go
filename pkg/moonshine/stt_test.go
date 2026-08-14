package moonshine

import (
	"testing"
)

func TestTranscriber_SetKeyterms_NotLoaded(t *testing.T) {
	if Loaded() {
		t.Skip("skipping native-free test since library is loaded")
	}
	tr := &Transcriber{}
	err := tr.SetKeyterms([]string{"Kubernetes", "Ceph"})
	if err != errNotLoaded {
		t.Errorf("SetKeyterms = %v, want %v", err, errNotLoaded)
	}
}

func TestTranscriber_SetContext_NotLoaded(t *testing.T) {
	if Loaded() {
		t.Skip("skipping native-free test since library is loaded")
	}
	tr := &Transcriber{}
	err := tr.SetContext("Migration plan for the platform team", 100)
	if err != errNotLoaded {
		t.Errorf("SetContext = %v, want %v", err, errNotLoaded)
	}
}

func TestTranscriber_Closed(t *testing.T) {
	tr := &Transcriber{closed: true}
	// Even if Loaded() is true or false, a closed transcriber should return error
	// When not loaded, errNotLoaded is returned first; when loaded, errClosed
	if err := tr.SetKeyterms([]string{"test"}); err == nil {
		t.Error("SetKeyterms on closed transcriber should return error")
	}
	if err := tr.SetContext("context", 100); err == nil {
		t.Error("SetContext on closed transcriber should return error")
	}
}
