package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// AppName is used for the XDG config/data directories and as a fallback
	// env var prefix.
	AppName = "moonshine"
	// ConfigFileName is the Viper config file name (without extension).
	ConfigFileName = "config"
)

var rootCmd = &cobra.Command{
	Use:   "moonshine",
	Short: "A CLI client for the Moonshine voice library (STT + TTS)",
	Long: `moonshine talks directly to libmoonshine (github.com/moonshine-ai/moonshine)
via a pure-Go FFI binding -- no Python, no reimplementation of the model
pipeline, just a thin client over the same C API moonshine's own language
bindings use.

Get started:
  moonshine doctor                     Check build/runtime prerequisites
  moonshine setup                      Download STT model assets
  moonshine transcribe audio.wav       Transcribe a local file
  moonshine transcribe gs://bkt/a.wav  Transcribe a file from GCS
  moonshine live                       Transcribe from the microphone, live
  moonshine tts "Hello world"          Synthesize speech
  moonshine config list                See effective config + where it comes from`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command. Called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, styleFail.Render("Error: ")+err.Error())
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initViper)

	rootCmd.AddGroup(
		&cobra.Group{ID: "voice", Title: "Voice Commands:"},
		&cobra.Group{ID: "config", Title: "Configuration:"},
	)

	pf := rootCmd.PersistentFlags()
	pf.Bool("json", false, "Output machine-readable JSON")
	pf.String("lib-dir", "", "Directory containing libmoonshine.{dylib,so} (default: $MOONSHINE_LIB_DIR, then ./.moonshine/lib)")
	pf.String("model-dir", "", "Root directory for downloaded model assets (default: platform cache dir; see README Configuration)")

	_ = viper.BindPFlag("output.json", pf.Lookup("json"))
	_ = viper.BindPFlag("lib.dir", pf.Lookup("lib-dir"))
	_ = viper.BindPFlag("model.dir", pf.Lookup("model-dir"))

	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(transcribeCmd)
	rootCmd.AddCommand(liveCmd)
	rootCmd.AddCommand(ttsCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(doctorCmd)
}

// initViper wires up config file + env var + default resolution. Priority
// order (highest wins): CLI flag > env var > config file > default.
func initViper() {
	viper.BindEnv("lib.dir", "MOONSHINE_LIB_DIR") //nolint:errcheck
	// MOONSHINE_MODEL_DIR is this CLI's own override; MOONSHINE_VOICE_CACHE
	// matches the env var moonshine's Python package itself reads (see
	// python/src/moonshine_voice/download_file.py's get_cache_dir), so
	// setting it points both tools at the same downloaded-model cache.
	viper.BindEnv("model.dir", "MOONSHINE_MODEL_DIR", "MOONSHINE_VOICE_CACHE") //nolint:errcheck
	// MOONSHINE_SRC matches scripts/build-libmoonshine.sh's env var for the
	// local moonshine checkout -- reused here so it doubles as the default
	// source for tts.g2p_root (below) without introducing a second name for
	// the same thing.
	viper.BindEnv("moonshine.src_dir", "MOONSHINE_SRC") //nolint:errcheck

	viper.SetDefault("model.dir", defaultModelDir())
	viper.SetDefault("stt.arch", "tiny")
	viper.SetDefault("stt.language", "en")
	viper.SetDefault("live.arch", "tiny-streaming")
	viper.SetDefault("tts.language", "en_us")
	viper.SetDefault("tts.voice", "")
	viper.SetDefault("tts.speed", "")
	viper.SetDefault("tts.g2p_root", "")

	viper.SetConfigName(ConfigFileName)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(xdgConfigDir())
	viper.AddConfigPath(".")

	_ = viper.ReadInConfig() // missing config file is not an error

	// Derived default: once moonshine.src_dir is known (env or config,
	// resolved above/by ReadInConfig), default tts.g2p_root to
	// <src_dir>/core/moonshine-tts/data unless a flag/env/config value for
	// tts.g2p_root already outranks it. Using SetDefault (not Set) keeps
	// this at the correct, lowest precedence rather than masquerading as an
	// explicit override.
	if viper.GetString("tts.g2p_root") == "" {
		if src := viper.GetString("moonshine.src_dir"); src != "" {
			viper.SetDefault("tts.g2p_root", filepath.Join(src, "core", "moonshine-tts", "data"))
		}
	}
}

func xdgConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, AppName)
}

// platformCacheDir returns the OS-conventional cache directory for
// "moonshine_voice" -- the same app name moonshine's Python package's
// user_cache_dir("moonshine_voice") resolves to (~/Library/Caches on macOS,
// $XDG_CACHE_HOME or ~/.cache on Linux), so a shared cache root downloads
// once for both tools. Overridable via --model-dir, $MOONSHINE_MODEL_DIR, or
// $MOONSHINE_VOICE_CACHE.
func platformCacheDir() string {
	const cacheAppName = "moonshine_voice"
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", cacheAppName)
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, cacheAppName, "Cache")
	default: // linux and other unix-likes
		base := os.Getenv("XDG_CACHE_HOME")
		if base == "" {
			base = filepath.Join(home, ".cache")
		}
		return filepath.Join(base, cacheAppName)
	}
}

func defaultModelDir() string {
	return platformCacheDir()
}

// jsonOutput reports whether --json was requested.
func jsonOutput() bool { return viper.GetBool("output.json") }
