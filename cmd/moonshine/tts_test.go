package main

import (
	"os"
	"path/filepath"
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

func TestIsPhononVoice(t *testing.T) {
	for voice, want := range map[string]bool{
		"phonon":          true,
		"phonon_zoey":     true,
		"Phonon_Easton":   true,
		"kokoro_af_heart": false,
		"piper_en_US-amy": false,
		"":                false,
		"phononzoey":      false,
	} {
		if got := isPhononVoice(voice); got != want {
			t.Errorf("isPhononVoice(%q) = %v, want %v", voice, got, want)
		}
	}
}

func TestPhononOptions(t *testing.T) {
	voiceFile := filepath.Join(t.TempDir(), "custom.safetensors")
	if err := os.WriteFile(voiceFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, err := phononOptions("phonon_zoey", "", "", "")
	if err != nil || opts != nil {
		t.Fatalf("no flags: got opts=%v err=%v, want nil, nil", opts, err)
	}

	opts, err = phononOptions("phonon", voiceFile, "0", "42")
	if err != nil {
		t.Fatalf("valid flags: %v", err)
	}
	want := map[string]string{"phonon_voice_file": voiceFile, "phonon_temperature": "0", "phonon_seed": "42"}
	if len(opts) != len(want) {
		t.Fatalf("got %d options, want %d: %v", len(opts), len(want), opts)
	}
	for _, o := range opts {
		if want[o.Name] != o.Value {
			t.Errorf("option %s = %q, want %q", o.Name, o.Value, want[o.Name])
		}
	}

	for name, tc := range map[string][4]string{
		"non-phonon voice":   {"kokoro_af_heart", "", "0.3", ""},
		"missing voice file": {"phonon", filepath.Join(t.TempDir(), "nope.safetensors"), "", ""},
		"bad temperature":    {"phonon_zoey", "", "warm", ""},
		"negative seed":      {"phonon_zoey", "", "", "-1"},
	} {
		if _, err := phononOptions(tc[0], tc[1], tc[2], tc[3]); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}
