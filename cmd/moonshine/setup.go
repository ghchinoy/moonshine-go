package main

import (
	"context"
	"fmt"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	setupLanguage string
	setupArch     string
	setupForce    bool
)

var setupCmd = &cobra.Command{
	Use:     "setup",
	GroupID: "config",
	Short:   "Download STT model assets into the local model directory",
	Long: `Downloads the encoder/decoder/tokenizer files for a speech-to-text
model using moonshine's own download manifest API, into --model-dir (default:
the platform cache directory, e.g. ~/Library/Caches/moonshine_voice on macOS
-- see the Configuration section in README.md). Each (language, arch) model
is namespaced under its own subdirectory to avoid filename collisions.

TTS voice assets (Kokoro/Piper/ZipVoice) are not auto-downloaded here -- the
C API only exposes their canonical asset *keys*, not a URL manifest, since
they're published through moonshine's separate voice-asset pipeline rather
than a flat CDN layout. Point the "tts" command's --g2p-root flag at a
moonshine checkout's core/moonshine-tts/data directory (or your own matching
layout) instead.`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().StringVar(&setupLanguage, "language", "en", "STT model language (code or English name)")
	setupCmd.Flags().StringVar(&setupArch, "arch", "tiny", "Model architecture: tiny, base, tiny-streaming, base-streaming, small-streaming, medium-streaming")
	setupCmd.Flags().BoolVar(&setupForce, "force", false, "Re-download files even if they already exist")
}

func modelArchFromFlag(s string) (uint32, error) {
	switch s {
	case "tiny":
		return moonshine.ModelArchTiny, nil
	case "base":
		return moonshine.ModelArchBase, nil
	case "tiny-streaming":
		return moonshine.ModelArchTinyStreaming, nil
	case "base-streaming":
		return moonshine.ModelArchBaseStreaming, nil
	case "small-streaming":
		return moonshine.ModelArchSmallStreaming, nil
	case "medium-streaming":
		return moonshine.ModelArchMediumStreaming, nil
	default:
		return 0, fmt.Errorf("unknown --arch %q (want one of: tiny, base, tiny-streaming, base-streaming, small-streaming, medium-streaming)", s)
	}
}

func runSetup(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}
	arch, err := modelArchFromFlag(setupArch)
	if err != nil {
		return err
	}

	manifest, err := moonshine.GetSTTDependencies(setupLanguage,
		moonshine.Option{Name: "model_arch", Value: fmt.Sprintf("%d", arch)})
	if err != nil {
		return fmt.Errorf("looking up dependencies for language %q: %w", setupLanguage, err)
	}

	root := viper.GetString("model.dir")
	fmt.Printf("%s %s (%s)\n", header("Downloading:"), setupLanguage, setupArch)
	fmt.Printf("%s %s\n", muted("cache root:"), root)
	for _, g := range manifest.Groups {
		fmt.Printf("%s %s\n", muted("into:"), moonshine.GroupDir(root, g))
		for _, f := range g.Files {
			fmt.Printf("  %s %s/%s\n", muted("-"), g.BaseURL, f)
		}
	}

	if err := moonshine.Download(context.Background(), manifest, root, setupForce); err != nil {
		return err
	}
	fmt.Println(stylePass.Render("Done."))
	return nil
}
