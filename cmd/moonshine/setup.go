package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	setupLanguage         string
	setupArch             string
	setupForce            bool
	setupIdentifySpeakers bool
	setupTTSVoice         string
	setupTTSLanguage      string
)

var setupCmd = &cobra.Command{
	Use:     "setup",
	GroupID: "config",
	Short:   "Download STT and TTS model assets into the local model directory",
	Long: `Downloads the encoder/decoder/tokenizer files for a speech-to-text
model using moonshine's own download manifest API, into --model-dir (default:
the platform cache directory, e.g. ~/Library/Caches/moonshine_voice on macOS
-- see the Configuration section in README.md). Each (language, arch) model
is namespaced under its own subdirectory to avoid filename collisions.

Use --tts <voice> (e.g. 'moonshine setup --tts kokoro_af_heart' or
'moonshine setup --tts all') to download Kokoro and Piper text-to-speech voice
models and G2P assets directly from the upstream CDN into model.dir. Downloaded
voices are automatically discovered by 'moonshine tts' and 'moonshine serve'
without requiring an upstream checkout or Git LFS. Pass '--arch none' if you only
wish to download TTS voices.`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().StringVar(&setupLanguage, "language", "en", "STT model language (code or English name; config key: stt.language, shared with 'transcribe')")
	setupCmd.Flags().StringVar(&setupArch, "arch", "tiny", "Model architecture: tiny, base, tiny-streaming, base-streaming, small-streaming, medium-streaming, none (config key: stt.arch, shared with 'transcribe')")
	setupCmd.Flags().BoolVar(&setupForce, "force", false, "Re-download files even if they already exist")
	setupCmd.Flags().BoolVar(&setupIdentifySpeakers, "identify-speakers", false, "Also download speaker diarization models (segmentation.ort and embedding.ort)")
	setupCmd.Flags().BoolVar(&setupIdentifySpeakers, "diarize", false, "Alias for --identify-speakers")
	setupCmd.Flags().StringVar(&setupTTSVoice, "tts", "", "Download TTS voice model(s): single voice ID (e.g. 'kokoro_af_heart', 'piper_en_US-amy-low'), comma-separated list, or 'all'")
	setupCmd.Flags().StringVar(&setupTTSLanguage, "tts-language", "en_us", "TTS language for --tts voices (config key: tts.language)")
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
	case "none":
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown --arch %q (want one of: tiny, base, tiny-streaming, base-streaming, small-streaming, medium-streaming, none)", s)
	}
}

func runSetup(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}
	root := viper.GetString("model.dir")
	language := flagOrConfig(cmd, "language", "stt.language", setupLanguage)
	archFlag := flagOrConfig(cmd, "arch", "stt.arch", setupArch)

	if archFlag != "none" {
		arch, err := modelArchFromFlag(archFlag)
		if err != nil {
			return err
		}

		manifest, err := moonshine.GetSTTDependencies(language,
			moonshine.Option{Name: "model_arch", Value: fmt.Sprintf("%d", arch)})
		if err != nil {
			return fmt.Errorf("looking up dependencies for language %q: %w", language, err)
		}

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
	}

	if setupTTSVoice != "" {
		ttsLang := flagOrConfig(cmd, "tts-language", "tts.language", setupTTSLanguage)
		if err := downloadTTSVoices(context.Background(), ttsLang, setupTTSVoice, root, setupForce); err != nil {
			return err
		}
	}

	fmt.Println(stylePass.Render("Done."))
	return nil
}

func downloadTTSVoices(ctx context.Context, lang, voiceFlag string, root string, force bool) error {
	rawVoices := strings.Split(voiceFlag, ",")
	var targetVoices []string

	for _, v := range rawVoices {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if v == "all" {
			avail, err := moonshine.ListVoices(lang)
			if err != nil {
				return fmt.Errorf("listing voices for %q: %w", lang, err)
			}
			langVoices := avail[lang]
			if len(langVoices) == 0 {
				return fmt.Errorf("no voices found for language %q", lang)
			}
			for _, va := range langVoices {
				// Exclude zipvoice (reference clone engine, not a downloadable ONNX preset voice)
				if !strings.HasPrefix(va.ID, "zipvoice") {
					targetVoices = append(targetVoices, va.ID)
				}
			}
		} else {
			if strings.HasPrefix(v, "zipvoice") {
				return fmt.Errorf("voice %q is a ZipVoice model -- ZipVoice uses reference audio clips with 'tts --clone' rather than downloadable preset ONNX models", v)
			}
			targetVoices = append(targetVoices, v)
		}
	}

	if len(targetVoices) == 0 {
		return fmt.Errorf("no valid TTS voices specified")
	}

	fmt.Printf("%s %d TTS voice(s) for %s\n", header("Downloading:"), len(targetVoices), lang)
	fmt.Printf("%s %s\n", muted("cache root:"), root)

	for _, voice := range targetVoices {
		manifest, err := moonshine.GetTTSDependencies(lang, moonshine.Option{Name: "voice", Value: voice})
		if err != nil {
			return fmt.Errorf("looking up dependencies for voice %q (%s): %w", voice, lang, err)
		}

		fmt.Printf("%s voice %s\n", header("Downloading:"), voice)
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

		if err := moonshine.Download(ctx, manifest, root, force); err != nil {
			return fmt.Errorf("downloading voice %q: %w", voice, err)
		}
	}

	return nil
}
