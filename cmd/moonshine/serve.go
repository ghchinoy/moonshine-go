package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/audio"
	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	serveAddr                        string
	serveWSPath                      string
	serveGRPCAddr                    string
	serveTransport                   string
	serveAudioSource                 string
	serveRemoteAudioRate             int
	serveRemoteAudioEncoding         string
	serveRemoteAudioChannels         int
	serveMaxSessions                 int
	serveAgent                       string
	serveGeminiModel                 string
	serveAllowActions                bool
	serveTTSVoice                    string
	serveTTSLanguage                 string
	serveTTSG2PRoot                  string
	serveIncludeAudio                bool
	serveLanguage                    string
	serveArch                        string
	serveProviders                   string
	servePollInterval                time.Duration
	serveIdentifySpeakers            bool
	serveWordTimestamps              bool
	serveDiarizationClusterCadence   float64
	serveDiarizationAnalyzeCadence   float64
	serveDiarizationClusterWindowSec float64
)

var serveCmd = &cobra.Command{
	Use:     "serve",
	GroupID: "voice",
	Short:   "Start the agentic voice sidecar daemon (streams updates over WS/gRPC, handles actions)",
	Long: `Starts a long-running 'moonshine serve' sidecar daemon that listens on the microphone,
streams live transcript events as JSON over WebSocket and/or gRPC, and executes actions
(TTS speak-back, display push, session control, LLM tool calls via Gemini).`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", ":8765", "Address for WebSocket transport (host:port)")
	serveCmd.Flags().StringVar(&serveWSPath, "ws-path", "/ws", "HTTP path for WebSocket transport")
	serveCmd.Flags().StringVar(&serveGRPCAddr, "grpc-addr", ":9090", "Address for gRPC transport (host:port)")
	serveCmd.Flags().StringVar(&serveTransport, "transport", "ws", "Comma-separated list of transports to enable (ws, grpc, or ws,grpc)")
	serveCmd.Flags().StringVar(&serveAudioSource, "audio-source", "local", "Audio source mode: local (microphone) or remote (PCM streaming over WebSocket)")
	serveCmd.Flags().IntVar(&serveRemoteAudioRate, "remote-audio-rate", 16000, "Remote audio sample rate in Hz (for --audio-source remote)")
	serveCmd.Flags().StringVar(&serveRemoteAudioEncoding, "remote-audio-encoding", "float32", "Remote audio sample encoding: float32 or int16 (for --audio-source remote)")
	serveCmd.Flags().IntVar(&serveRemoteAudioChannels, "remote-audio-channels", 1, "Remote audio channel count: 1 (mono) or 2 (stereo) (for --audio-source remote)")
	serveCmd.Flags().IntVar(&serveMaxSessions, "max-sessions", 10, "Maximum concurrent sessions in remote audio mode")
	serveCmd.Flags().StringVar(&serveAgent, "agent", "external", "Agent mode: external (subscribers handle logic via IPC) or gemini (built-in Gemini LLM agent)")
	serveCmd.Flags().StringVar(&serveGeminiModel, "gemini-model", "gemini-2.5-flash", "Google Gemini model ID (for --agent gemini)")
	serveCmd.Flags().BoolVar(&serveAllowActions, "allow-actions", false, "Gate enabling mutating actions (speak, session control, run_command)")
	serveCmd.Flags().StringVar(&serveTTSVoice, "tts-voice", "", "Default voice override for TTS speaker")
	serveCmd.Flags().StringVar(&serveTTSLanguage, "tts-language", "en_us", "TTS speaker language")
	serveCmd.Flags().StringVar(&serveTTSG2PRoot, "g2p-root", "", "Directory holding kokoro/, <lang>/piper-voices/, etc. (default: derived from moonshine.src_dir)")
	serveCmd.Flags().BoolVar(&serveIncludeAudio, "include-audio", false, "Include raw PCM audio_data []float32 in transcript event frames")
	_ = viper.BindPFlag("tts.g2p_root", serveCmd.Flags().Lookup("g2p-root"))
	serveCmd.Flags().StringVar(&serveLanguage, "language", "en", "STT model language")
	serveCmd.Flags().StringVar(&serveArch, "arch", "tiny-streaming", "STT model architecture (tiny-streaming, small-streaming, medium-streaming)")
	serveCmd.Flags().StringVar(&serveProviders, "providers", defaultOrtProviders(), "ONNX Runtime execution providers (e.g. CPU, CoreML,CPU)")
	serveCmd.Flags().DurationVar(&servePollInterval, "poll-interval", 250*time.Millisecond, "STT stream poll interval")
	serveCmd.Flags().BoolVar(&serveIdentifySpeakers, "identify-speakers", false, "Enable speaker diarization")
	serveCmd.Flags().BoolVar(&serveWordTimestamps, "word-timestamps", false, "Enable per-word timing")
	serveCmd.Flags().Float64Var(&serveDiarizationClusterCadence, "diarization-cluster-cadence", 2.0, "Diarization re-clustering cadence (seconds)")
	serveCmd.Flags().Float64Var(&serveDiarizationAnalyzeCadence, "diarization-analyze-cadence", 1.0, "Diarization analyze cadence (seconds)")
	serveCmd.Flags().Float64Var(&serveDiarizationClusterWindowSec, "diarization-cluster-window-sec", 120.0, "Diarization cluster window (seconds)")
}

func runServe(cmd *cobra.Command, args []string) error {
	if err := loadLibrary(); err != nil {
		return err
	}

	archFlag := flagOrConfig(cmd, "arch", "live.arch", serveArch)
	arch, err := modelArchFromFlag(archFlag)
	if err != nil {
		return err
	}
	language := flagOrConfig(cmd, "language", "stt.language", serveLanguage)

	if !jsonOutput() {
		fmt.Fprintln(os.Stderr, muted("loading STT model..."))
	}
	loadOpts := append(ortProviderOptions(serveProviders),
		diarizationOptions(cmd, serveIdentifySpeakers, serveWordTimestamps,
			serveDiarizationClusterCadence, serveDiarizationAnalyzeCadence, serveDiarizationClusterWindowSec)...)
	tr, err := loadTranscriberFor(language, arch, loadOpts...)
	if err != nil {
		return err
	}
	defer tr.Close()

	var audioSource serveapi.AudioSource
	var remoteSource *serve.RemoteAudioSource

	switch strings.ToLower(strings.TrimSpace(serveAudioSource)) {
	case "local", "mic":
		if !jsonOutput() {
			fmt.Fprintln(os.Stderr, muted("opening microphone..."))
		}
		mic, err := audio.StartMicCapture()
		if err != nil {
			return fmt.Errorf("%w\n\n%s", err, muted("hint: check microphone permissions for this terminal in System Settings > Privacy & Security > Microphone"))
		}
		defer mic.Close()
		audioSource = mic

	case "remote":
		if !jsonOutput() {
			fmt.Fprintln(os.Stderr, muted(fmt.Sprintf("configuring remote PCM audio source (%dHz, %s, %d ch)...", serveRemoteAudioRate, serveRemoteAudioEncoding, serveRemoteAudioChannels)))
		}
		remoteSource = serve.NewRemoteAudioSource(serve.AudioFormat{
			SampleRate: serveRemoteAudioRate,
			Channels:   serveRemoteAudioChannels,
			Encoding:   serve.AudioEncoding(serveRemoteAudioEncoding),
		}, 100)
		defer remoteSource.Close()
		audioSource = remoteSource

	default:
		return fmt.Errorf("unknown --audio-source %q (expected 'local' or 'remote')", serveAudioSource)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// TTS Speaker
	ttsOpts := ortProviderOptions(viper.GetString("tts.providers"))
	if g2pRoot := viper.GetString("tts.g2p_root"); g2pRoot != "" {
		ttsOpts = append(ttsOpts, moonshine.Option{Name: "g2p_root", Value: g2pRoot})
	}
	if serveTTSVoice != "" {
		ttsOpts = append(ttsOpts, moonshine.Option{Name: "voice", Value: serveTTSVoice})
	}
	speaker := serve.NewTTSSpeaker(serveTTSLanguage, ttsOpts...)
	defer speaker.Close()

	hub := serve.NewHub()

	// Agent Setup
	var agentHandler serve.AgentHandler
	if strings.ToLower(serveAgent) == "gemini" {
		realClient, err := serve.NewRealGeminiClient(ctx, serveGeminiModel)
		if err != nil && !jsonOutput() {
			fmt.Fprintf(os.Stderr, "%s %v\n", styleWarn.Render("warning: could not initialize Gemini client:"), err)
		}
		geminiAgent := serve.NewGeminiAgent(serve.GeminiAgentOptions{
			Model:           serveGeminiModel,
			AllowRunCommand: serveAllowActions,
			Client:          realClient,
			Retriever:       serve.NoopRetriever{},
		})
		intentMatcher := serve.NewIntentMatcher()
		agentHandler = serve.NewCompositeHandler(intentMatcher, geminiAgent)
	} else {
		agentHandler = serve.ExternalAgent{}
	}

	var sessMgr *serve.SessionManager
	var audioFmt serve.AudioFormat

	if remoteSource != nil {
		audioFmt = serve.AudioFormat{
			SampleRate: serveRemoteAudioRate,
			Channels:   serveRemoteAudioChannels,
			Encoding:   serve.AudioEncoding(serveRemoteAudioEncoding),
		}
		sessMgr = serve.NewSessionManager(serve.SessionManagerConfig{
			Transcriber:  tr,
			Speaker:      speaker,
			MaxSessions:  serveMaxSessions,
			PollInterval: servePollInterval,
			AllowActions: serveAllowActions,
			IncludeAudio: serveIncludeAudio,
			Agent:        agentHandler,
		})
		defer sessMgr.Close()
	}

	// Configure Transports
	var transports []serve.Transport
	transportTypes := strings.Split(serveTransport, ",")
	for _, t := range transportTypes {
		switch strings.TrimSpace(strings.ToLower(t)) {
		case "ws":
			wsTr := serve.NewWSTransport(hub, serveAddr, serveWSPath)
			if sessMgr != nil {
				wsTr.SetSessionManager(sessMgr, audioFmt)
			} else if remoteSource != nil {
				wsTr.SetAudioSink(remoteSource)
			}
			transports = append(transports, wsTr)
		case "grpc":
			grpcTr := serve.NewGRPCTransport(hub, serveGRPCAddr)
			if sessMgr != nil {
				grpcTr.SetSessionManager(sessMgr, audioFmt)
			}
			transports = append(transports, grpcTr)
		}
	}

	srv, err := serve.NewServer(serve.ServerConfig{
		Transcriber:  tr,
		AudioSource:  audioSource,
		Hub:          hub,
		Transports:   transports,
		Agent:        agentHandler,
		Speaker:      speaker,
		AllowActions: serveAllowActions,
		IncludeAudio: serveIncludeAudio,
		PollInterval: servePollInterval,
	})
	if err != nil {
		return err
	}

	if !jsonOutput() {
		fmt.Fprintln(os.Stderr, stylePass.Render("moonshine serve is running"))
		fmt.Fprintf(os.Stderr, "  audio-source:  %s\n", serveAudioSource)
		if serveAudioSource == "remote" {
			fmt.Fprintf(os.Stderr, "  max-sessions:  %d\n", serveMaxSessions)
		}
		fmt.Fprintf(os.Stderr, "  transports:    %s\n", serveTransport)
		fmt.Fprintf(os.Stderr, "  allow-actions: %v\n", serveAllowActions)
		fmt.Fprintf(os.Stderr, "  include-audio: %v\n", serveIncludeAudio)
		fmt.Fprintf(os.Stderr, "  agent:         %s\n", serveAgent)
		fmt.Fprintln(os.Stderr, muted("press Ctrl-C to stop"))
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- srv.Run(ctx)
	}()

	select {
	case <-sigCh:
		if !jsonOutput() {
			fmt.Fprintln(os.Stderr, muted("\nstopping sidecar daemon..."))
		}
		cancel()
		<-runErrCh
	case err := <-runErrCh:
		if err != nil && ctx.Err() == nil {
			return err
		}
	}

	return nil
}
