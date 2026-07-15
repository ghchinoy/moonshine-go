package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// flagOrConfig resolves a flag that's shared (by convention, not by
// viper.BindPFlag) across more than one command -- e.g. stt.language and
// stt.arch are both settable via "moonshine setup --language/--arch" and
// "moonshine transcribe --language/--arch". viper.BindPFlag keeps only one
// bound *pflag.Flag per key globally, so binding it from two different
// commands' local flags would make the second registration silently steal
// precedence checks for the first command's flag. Doing the "was this flag
// explicitly set on the command line" check by hand instead avoids that.
//
// Precedence: this command's flag (if explicitly set) > config key (env or
// config.yaml, via viper) > the flag's own default (flagValue, unchanged).
func flagOrConfig(cmd *cobra.Command, flagName, key, flagValue string) string {
	if cmd.Flags().Changed(flagName) {
		return flagValue
	}
	if v := viper.GetString(key); v != "" {
		return v
	}
	return flagValue
}

// loadLibrary resolves and dlopen's libmoonshine, with a CLI-friendly error
// pointing at scripts/build-libmoonshine.sh if it can't be found.
func loadLibrary() error {
	if err := moonshine.Load(viper.GetString("lib.dir")); err != nil {
		return fmt.Errorf("%w\n\n%s", err, muted("hint: run scripts/build-libmoonshine.sh against a local moonshine checkout, then set MOONSHINE_LIB_DIR or --lib-dir to its output directory (default: .moonshine/lib)"))
	}
	return nil
}

// sttModelDir resolves the on-disk directory an STT model for
// (language, arch) lives (or should be downloaded) in, under model.dir --
// see moonshine.GroupDir/PrimaryModelDir for why this is namespaced rather
// than a single flat directory.
func sttModelDir(language string, arch uint32) (string, error) {
	manifest, err := moonshine.GetSTTDependencies(language,
		moonshine.Option{Name: "model_arch", Value: fmt.Sprintf("%d", arch)})
	if err != nil {
		return "", fmt.Errorf("looking up dependencies for language %q: %w", language, err)
	}
	return moonshine.PrimaryModelDir(viper.GetString("model.dir"), manifest)
}

// loadTranscriberFor loads a transcriber for (language, arch), with a
// CLI-friendly error suggesting `moonshine setup` if the model isn't
// downloaded yet. extraOpts are passed through to moonshine.LoadTranscriber
// (e.g. ort_providers/coreml_cache_dir from ortProviderOptions).
func loadTranscriberFor(language string, arch uint32, extraOpts ...moonshine.Option) (*moonshine.Transcriber, error) {
	dir, err := sttModelDir(language, arch)
	if err != nil {
		return nil, err
	}
	tr, err := moonshine.LoadTranscriber(dir, arch, extraOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w\n\n%s", err, muted(fmt.Sprintf("hint: run `moonshine setup --language %s --arch %s` first", language, archFlagName(arch))))
	}
	return tr, nil
}

// defaultOrtProviders returns the ONNX Runtime execution provider list to
// try by default. This is CPU-only ("") on every OS, deliberately -- see
// docs/hardware-acceleration.md. moonshine-c-api.h documents CoreML,CPU as
// supported on macOS, and --providers lets you opt into it, but hands-on
// testing found CoreML measurably *slower* than CPU for the tiny arch,
// outright erroring for base, and silently returning empty transcripts for
// tiny-streaming, on the vendored onnxruntime build this project targets.
// CPU is the only option verified fast and correct across every arch.
func defaultOrtProviders() string {
	return ""
}

// ortProviderOptions builds the moonshine options for a given
// --providers value: "ort_providers" itself, plus a persistent
// "coreml_cache_dir" (under model.dir) when CoreML is requested, since
// CoreML compiles models on first load and the compiled cache lets
// subsequent runs skip that cost.
func ortProviderOptions(providers string) []moonshine.Option {
	if providers == "" {
		return nil
	}
	opts := []moonshine.Option{{Name: "ort_providers", Value: providers}}
	if strings.Contains(strings.ToLower(providers), "coreml") {
		opts = append(opts, moonshine.Option{
			Name:  "coreml_cache_dir",
			Value: filepath.Join(viper.GetString("model.dir"), "ort-coreml-cache"),
		})
	}
	return opts
}

// diarizationOptions builds the moonshine.Options for --identify-speakers /
// --word-timestamps, plus the diarization_cluster_cadence /
// diarization_analyze_cadence / diarization_cluster_window_sec tuning knobs
// -- all transcriber-*creation*-time options (moonshine-c-api.h's
// moonshine_load_transcriber_from_files), not per-transcribe-call options.
//
// The tuning knobs are only included if their flag was explicitly set on
// cmd (cmd.Flags().Changed), since their zero value is not "use the
// library's own default" for at least one of them --
// diarization_cluster_window_sec's default is 120s, but 0 means
// "unlimited" -- so silently sending 0 for an unset flag would change
// behavior rather than leave it alone. cmd need not declare all three
// flags (transcribe currently only offers identify-speakers/word-
// timestamps): Changed() on an undeclared flag name just returns false, so
// callers without the tuning flags simply never include them.
func diarizationOptions(cmd *cobra.Command, identifySpeakers, wordTimestamps bool, clusterCadence, analyzeCadence, clusterWindowSec float64) []moonshine.Option {
	var opts []moonshine.Option
	if identifySpeakers {
		opts = append(opts, moonshine.Option{Name: "identify_speakers", Value: "true"})
	}
	if wordTimestamps {
		opts = append(opts, moonshine.Option{Name: "word_timestamps", Value: "true"})
	}
	if cmd.Flags().Changed("diarization-cluster-cadence") {
		opts = append(opts, moonshine.Option{Name: "diarization_cluster_cadence", Value: fmt.Sprintf("%g", clusterCadence)})
	}
	if cmd.Flags().Changed("diarization-analyze-cadence") {
		opts = append(opts, moonshine.Option{Name: "diarization_analyze_cadence", Value: fmt.Sprintf("%g", analyzeCadence)})
	}
	if cmd.Flags().Changed("diarization-cluster-window-sec") {
		opts = append(opts, moonshine.Option{Name: "diarization_cluster_window_sec", Value: fmt.Sprintf("%g", clusterWindowSec)})
	}
	return opts
}

func archFlagName(arch uint32) string {
	switch arch {
	case moonshine.ModelArchTiny:
		return "tiny"
	case moonshine.ModelArchBase:
		return "base"
	case moonshine.ModelArchTinyStreaming:
		return "tiny-streaming"
	case moonshine.ModelArchBaseStreaming:
		return "base-streaming"
	case moonshine.ModelArchSmallStreaming:
		return "small-streaming"
	case moonshine.ModelArchMediumStreaming:
		return "medium-streaming"
	default:
		return "tiny"
	}
}
