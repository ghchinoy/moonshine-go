package main

import (
	"strings"
	"testing"
)

func TestTTSCmd_CloneVoiceMutualExclusivity(t *testing.T) {
	// Reset flags for test isolation
	ttsVoice = "kokoro_af_heart"
	ttsClone = "test.wav"
	defer func() {
		ttsVoice = ""
		ttsClone = ""
	}()

	err := runTTS(ttsCmd, []string{"Hello world"})
	if err == nil {
		t.Fatal("runTTS with both --voice and --clone returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want mutually exclusive error message", err)
	}
}
