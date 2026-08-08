package main

import (
	"context"
	"fmt"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	setupLanguage         string
	setupArch             string
	setupForce            bool
	setupIdentifySpeakers bool
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
	setupCmd.Flags().StringVar(&setupLanguage, "language", "en", "STT model language (code or English name; config key: stt.language, shared with 'transcribe')")
	setupCmd.Flags().StringVar(&setupArch, "arch", "tiny", "Model architecture: tiny, base, tiny-streaming, base-streaming, small-streaming, medium-streaming (config key: stt.arch, shared with 'transcribe')")
	setupCmd.Flags().BoolVar(&setupForce, "force", false, "Re-download files even if they already exist")
	setupCmd.Flags().BoolVar(&setupIdentifySpeakers, "identify-speakers", false, "Also download speaker diarization models (segmentation.ort and embedding.ort)")
	setupCmd.Flags().BoolVar(&setupIdentifySpeakers, "diarize", false, "Alias for --identify-speakers")
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
	language := flagOrConfig(cmd, "language", "stt.language", setupLanguage)
	archFlag := flagOrConfig(cmd, "arch", "stt.arch", setupArch)
	arch, err := modelArchFromFlag(archFlag)
	if err != nil {
		return err
	}

	manifest, err := moonshine.GetSTTDependencies(language,
		moonshine.Option{Name: "model_arch", Value: fmt.Sprintf("%d", arch)})
	if err != nil {
		return fmt.Errorf("looking up dependencies for language %q: %w", language, err)
	}

	root := viper.GetString("model.dir")
	fmt.Printf("%s %s (%s)\n", header("Downloading:"), language, archFlag)
	fmt.Printf("%s %s\n", muted("cache root:"), root)
	for _, g := range manifest.Groups {
		fmt.Printf("%s %s\n", muted("into:"), moonshine.GroupDir(root, g))
		for _, f := range g.Files {
			fileURL := f.URL
			if fileURL == "" {
				fileURL = g.BaseURL + "/" + f.Name
			}
			fmt.Printf("  %s %s\n", muted("-"), fileURL)
		}
	}

	if err := moonshine.Download(context.Background(), manifest, root, setupForce); err != nil {
		return err
	}

	if setupIdentifySpeakers {
		fmt.Printf("%s speaker diarization models\n", header("Downloading:"))
		diarManifest, err := moonshine.GetDiarizationDependencies()
		if err != nil {
			return fmt.Errorf("looking up diarization dependencies: %w", err)
		}
		if err := moonshine.Download(context.Background(), diarManifest, root, setupForce); err != nil {
			return fmt.Errorf("downloading diarization models: %w", err)
		}
	}

	fmt.Println(stylePass.Render("Done."))
	return nil
}
