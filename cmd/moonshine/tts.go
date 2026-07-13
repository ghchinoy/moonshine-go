package main

import (
	"fmt"
	"os"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	// Flag targets. Effective values are always read via viper.GetString in
	// runTTS (flag > env > config.yaml > default), not these vars directly
	// -- see the viper.BindPFlag calls in init() below.
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
kokoro/, <lang>/piper-voices/, etc.) -- see docs/user-guide.md for how to
fetch these voice assets; "moonshine setup" only automates STT model
downloads (see its --help for why).

--g2p-root defaults to <moonshine.src_dir>/core/moonshine-tts/data if
moonshine.src_dir is set (env $MOONSHINE_SRC, or the moonshine.src_dir key
in config.yaml) -- set it once with "moonshine config set moonshine.src_dir
/path/to/moonshine" instead of passing --g2p-root every time.`,
	RunE: runTTS,
}

func init() {
	ttsCmd.Flags().StringVar(&ttsLanguage, "language", "en_us", "Language / CLI tag")
	ttsCmd.Flags().StringVar(&ttsVoice, "voice", "", `Voice id, e.g. "kokoro_af_heart", "piper_en_US-amy-low", "zipvoice_american_female"`)
	ttsCmd.Flags().StringVar(&ttsSpeed, "speed", "", "Synthesis speed multiplier (default 1.0)")
	ttsCmd.Flags().StringVar(&ttsG2PRoot, "g2p-root", "", "Directory holding kokoro/, <lang>/piper-voices/, etc. (default: derived from moonshine.src_dir; see 'moonshine config --help')")
	ttsCmd.Flags().StringVarP(&ttsOutput, "output", "o", "out.wav", "Output WAV file path")
	ttsCmd.Flags().BoolVar(&ttsListVoices, "list-voices", false, "List known voices for --language and exit")

	// Safe to BindPFlag directly here: unlike stt.language/stt.arch (shared
	// across setup/transcribe/live -- see flagOrConfig in lib.go), these
	// tts.* keys are each only ever bound to this one command's flag, so
	// there's no risk of one BindPFlag call clobbering another command's.
	_ = viper.BindPFlag("tts.language", ttsCmd.Flags().Lookup("language"))
	_ = viper.BindPFlag("tts.voice", ttsCmd.Flags().Lookup("voice"))
	_ = viper.BindPFlag("tts.speed", ttsCmd.Flags().Lookup("speed"))
	_ = viper.BindPFlag("tts.g2p_root", ttsCmd.Flags().Lookup("g2p-root"))
}

func runTTS(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}

	language := viper.GetString("tts.language")
	voice := viper.GetString("tts.voice")
	speed := viper.GetString("tts.speed")
	g2pRoot := viper.GetString("tts.g2p_root")

	var createOpts []moonshine.Option
	if g2pRoot != "" {
		createOpts = append(createOpts, moonshine.Option{Name: "g2p_root", Value: g2pRoot})
	}

	if ttsListVoices {
		voices, err := moonshine.ListVoices(language, createOpts...)
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
		fmt.Fprintln(os.Stderr, muted("note: \"found\" only checks the file exists, not that it's real content -- Git LFS pointer stubs count as found. Run a real synthesis to confirm."))
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("tts: a text argument is required (or pass --list-voices)")
	}
	text := args[0]

	if voice != "" {
		createOpts = append(createOpts, moonshine.Option{Name: "voice", Value: voice})
	}
	if speed != "" {
		createOpts = append(createOpts, moonshine.Option{Name: "speed", Value: speed})
	}

	t0 := time.Now()
	synth, err := moonshine.NewSynthesizer(language, createOpts...)
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
