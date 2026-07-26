package main

import (
	"fmt"
	"os"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	// Flag targets. Effective values are always read via viper.GetString in
	// runTTS (flag > env > config.yaml > default), not these vars directly
	// -- see the viper.BindPFlag calls in init() below.
	ttsLanguage     string
	ttsVoice        string
	ttsSpeed        string
	ttsG2PRoot      string
	ttsOutput       string
	ttsListVoices   bool
	ttsPlay            bool
	ttsIPA             string
	ttsShowPhonemes    bool
	ttsClone           string
	ttsCloneTranscript string
)

var ttsCmd = &cobra.Command{
	Use:     "tts <text>",
	GroupID: "voice",
	Short:   "Synthesize speech from text",
	Args:    cobra.MaximumNArgs(1),
	Long: `Synthesizes text to speech using moonshine's TTS engines (Kokoro, Piper,
or ZipVoice, selected via --voice), or clones a voice from a reference .wav
clip with --clone (also ZipVoice, but uses your reference audio instead of a
preset voice -- see docs/user-guide.md's "Recording and cloning a voice").
--g2p-root must point at a directory laid out like a moonshine checkout's
core/moonshine-tts/data (containing kokoro/, <lang>/piper-voices/, etc.) -- see
docs/user-guide.md for how to fetch these voice assets; "moonshine setup" only
automates STT model downloads (see its --help for why).

--g2p-root defaults to <moonshine.src_dir>/core/moonshine-tts/data if
moonshine.src_dir is set (env $MOONSHINE_SRC, or the moonshine.src_dir key
in config.yaml) -- set it once with "moonshine config set moonshine.src_dir
/path/to/moonshine" instead of passing --g2p-root every time.

--show-phonemes and --ipa support an inspect-and-edit workflow for fixing
mispronunciations (e.g. proper nouns): run --show-phonemes on your text to
see the default IPA phonemes G2P produces, then re-run with --ipa and your
hand-corrected phonemes to synthesize from them directly, skipping G2P.`,
	RunE: runTTS,
}

func init() {
	ttsCmd.Flags().StringVar(&ttsLanguage, "language", "en_us", "Language / CLI tag")
	ttsCmd.Flags().StringVar(&ttsVoice, "voice", "", `Voice id, e.g. "kokoro_af_heart", "piper_en_US-amy-low", "zipvoice_american_female"`)
	ttsCmd.Flags().StringVar(&ttsSpeed, "speed", "", "Synthesis speed multiplier (default 1.0)")
	ttsCmd.Flags().StringVar(&ttsG2PRoot, "g2p-root", "", "Directory holding kokoro/, <lang>/piper-voices/, etc. (default: derived from moonshine.src_dir; see 'moonshine config --help')")
	ttsCmd.Flags().StringVarP(&ttsOutput, "output", "o", "out.wav", "Output WAV file path")
	ttsCmd.Flags().BoolVar(&ttsListVoices, "list-voices", false, "List known voices for --language and exit")
	ttsCmd.Flags().BoolVar(&ttsPlay, "play", false, "Play the synthesized audio through the default output device after writing it")
	ttsCmd.Flags().StringVar(&ttsIPA, "ipa", "", "Synthesize directly from this IPA phonemes string, skipping G2P (mutually exclusive with <text>; see --show-phonemes)")
	ttsCmd.Flags().BoolVar(&ttsShowPhonemes, "show-phonemes", false, "Print the default G2P IPA phonemes for <text> and exit, without synthesizing audio")
	ttsCmd.Flags().StringVar(&ttsClone, "clone", "", "Clone the voice from a reference .wav clip using ZipVoice (mutually exclusive with --voice)")
	ttsCmd.Flags().StringVar(&ttsCloneTranscript, "clone-transcript", "", "Transcript of the --clone reference clip (optional; recommended for optimal voice cloning quality)")

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
	language := viper.GetString("tts.language")
	voice := viper.GetString("tts.voice")
	speed := viper.GetString("tts.speed")
	g2pRoot := viper.GetString("tts.g2p_root")

	if ttsIPA != "" && len(args) > 0 {
		return fmt.Errorf("tts: --ipa and a <text> argument are mutually exclusive -- --ipa already provides phonemes directly, it doesn't need text to convert")
	}

	if ttsShowPhonemes && len(args) == 0 {
		return fmt.Errorf("tts: --show-phonemes requires a <text> argument")
	}

	if ttsClone != "" && voice != "" {
		return fmt.Errorf("tts: --clone and --voice are mutually exclusive -- --clone automatically uses the ZipVoice zero-shot voice-cloning engine")
	}

	if !ttsListVoices && ttsIPA == "" && len(args) == 0 {
		return fmt.Errorf("tts: a text argument is required (or pass --ipa, --show-phonemes, or --list-voices)")
	}

	if err := loadLibrary(); err != nil {
		return err
	}

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

	if ttsShowPhonemes {
		phonemizer, err := moonshine.NewPhonemizer(language, createOpts...)
		if err != nil {
			return err
		}
		defer phonemizer.Close()
		phonemes, err := phonemizer.TextToPhonemes(args[0])
		if err != nil {
			return err
		}
		fmt.Println(phonemes)
		return nil
	}

	var text string
	if len(args) > 0 {
		text = args[0]
	}

	var synth *moonshine.Synthesizer
	var loadMs float64

	if ttsClone != "" {
		if speed != "" {
			createOpts = append(createOpts, moonshine.Option{Name: "speed", Value: speed})
		}
		samples, sampleRate, err := audio.LoadFileWithSampleRate(ttsClone)
		if err != nil {
			return fmt.Errorf("tts: loading clone reference clip %s: %w", ttsClone, err)
		}
		t0 := time.Now()
		var sErr error
		synth, sErr = moonshine.NewSynthesizerFromClone(language, samples, int32(sampleRate), ttsCloneTranscript, createOpts...)
		if sErr != nil {
			return fmt.Errorf("tts: creating clone synthesizer: %w", sErr)
		}
		defer synth.Close()
		loadMs = msSince(t0)
	} else {
		if voice != "" {
			createOpts = append(createOpts, moonshine.Option{Name: "voice", Value: voice})
		}
		if speed != "" {
			createOpts = append(createOpts, moonshine.Option{Name: "speed", Value: speed})
		}

		t0 := time.Now()
		var sErr error
		synth, sErr = moonshine.NewSynthesizer(language, createOpts...)
		if sErr != nil {
			return sErr
		}
		defer synth.Close()
		loadMs = msSince(t0)
	}

	synthStart := time.Now()
	var out moonshine.Audio
	var synthErr error
	if ttsIPA != "" {
		out, synthErr = synth.PhonemesToSpeech(ttsIPA)
	} else {
		out, synthErr = synth.Synthesize(text)
	}
	if synthErr != nil {
		return synthErr
	}
	synthMs := msSince(synthStart)

	if err := audio.SaveWAV(ttsOutput, out.Samples, int(out.SampleRate)); err != nil {
		return err
	}

	fmt.Printf("%s %s (%d samples, %d Hz, %.2fs)\n", stylePass.Render("Wrote"), ttsOutput, len(out.Samples), out.SampleRate, out.Duration().Seconds())
	fmt.Fprintf(os.Stderr, "%s load=%.0fms synth=%.0fms rtf=%.1fx\n",
		muted("stats:"), loadMs, synthMs, out.Duration().Seconds()/(synthMs/1000.0))

	if ttsPlay {
		fmt.Fprintln(os.Stderr, muted("playing..."))
		if err := audio.PlayFloat32(out.Samples, out.SampleRate); err != nil {
			return fmt.Errorf("tts: playing audio: %w", err)
		}
	}
	return nil
}
