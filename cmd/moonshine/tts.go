package main

import (
	"fmt"
	"os"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/spf13/cobra"
)

var (
	ttsLanguage   string
	ttsVoice      string
	ttsSpeed      string
	ttsG2PRoot    string
	ttsOutput     string
	ttsListVoices bool
)

var ttsCmd = &cobra.Command{
	Use:     "tts <text>",
	GroupID: "voice",
	Short:   "Synthesize speech from text",
	Args:    cobra.MaximumNArgs(1),
	Long: `Synthesizes text to speech using moonshine's TTS engines (Kokoro, Piper,
or ZipVoice, selected via --voice). --g2p-root must point at a directory
laid out like a moonshine checkout's core/moonshine-tts/data (containing
kokoro/, <lang>/piper-voices/, etc.) -- see moonshine's README for how to
fetch these voice assets; "moonshine setup" only automates STT model
downloads (see its --help for why).`,
	RunE: runTTS,
}

func init() {
	ttsCmd.Flags().StringVar(&ttsLanguage, "language", "en_us", "Language / CLI tag")
	ttsCmd.Flags().StringVar(&ttsVoice, "voice", "", `Voice id, e.g. "kokoro_af_heart", "piper_en_US-amy-low", "zipvoice_american_female"`)
	ttsCmd.Flags().StringVar(&ttsSpeed, "speed", "", "Synthesis speed multiplier (default 1.0)")
	ttsCmd.Flags().StringVar(&ttsG2PRoot, "g2p-root", "", "Directory holding kokoro/, <lang>/piper-voices/, etc. (aliases: model_root/path_root/tts_root)")
	ttsCmd.Flags().StringVarP(&ttsOutput, "output", "o", "out.wav", "Output WAV file path")
	ttsCmd.Flags().BoolVar(&ttsListVoices, "list-voices", false, "List known voices for --language and exit")
}

func runTTS(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}

	var createOpts []moonshine.Option
	if ttsG2PRoot != "" {
		createOpts = append(createOpts, moonshine.Option{Name: "g2p_root", Value: ttsG2PRoot})
	}

	if ttsListVoices {
		voices, err := moonshine.ListVoices(ttsLanguage, createOpts...)
		if err != nil {
			return err
		}
		for lang, vs := range voices {
			fmt.Println(header(lang))
			for _, v := range vs {
				state := stylePass.Render("found")
				if !v.Found {
					state = styleWarn.Render("missing")
				}
				fmt.Printf("  %-32s %s\n", v.ID, state)
			}
		}
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("tts: a text argument is required (or pass --list-voices)")
	}
	text := args[0]

	if ttsVoice != "" {
		createOpts = append(createOpts, moonshine.Option{Name: "voice", Value: ttsVoice})
	}
	if ttsSpeed != "" {
		createOpts = append(createOpts, moonshine.Option{Name: "speed", Value: ttsSpeed})
	}

	t0 := time.Now()
	synth, err := moonshine.NewSynthesizer(ttsLanguage, createOpts...)
	if err != nil {
		return err
	}
	defer synth.Close()
	loadMs := msSince(t0)

	t0 = time.Now()
	out, err := synth.Synthesize(text)
	if err != nil {
		return err
	}
	synthMs := msSince(t0)

	if err := audio.SaveWAV(ttsOutput, out.Samples, int(out.SampleRate)); err != nil {
		return err
	}

	fmt.Printf("%s %s (%d samples, %d Hz, %.2fs)\n", stylePass.Render("Wrote"), ttsOutput, len(out.Samples), out.SampleRate, out.Duration().Seconds())
	fmt.Fprintf(os.Stderr, "%s load=%.0fms synth=%.0fms rtf=%.1fx\n",
		muted("stats:"), loadMs, synthMs, out.Duration().Seconds()/(synthMs/1000.0))
	return nil
}
