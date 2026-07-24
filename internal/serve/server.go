package serve

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// LiveSession defines the interface for a live transcription update source.
// *session.Live satisfies this interface.
type LiveSession interface {
	Run(ctx context.Context)
	Updates() <-chan session.Update
}

// ServerConfig defines the options to start a moonshine serve sidecar daemon.
type ServerConfig struct {
	Transcriber  *moonshine.Transcriber
	AudioSource  serveapi.AudioSource
	Session      LiveSession
	Hub          *Hub
	Transports   []Transport
	Agent        AgentHandler
	Speaker      Speaker
	AllowActions bool
	IncludeAudio bool
	PollInterval time.Duration
}

// LiveSessionControl implements SessionControl for a live serve session.
type LiveSessionControl struct {
	mu     sync.Mutex
	paused bool
	cancel context.CancelFunc
}

// Pause pauses the live session transcription.
func (s *LiveSessionControl) Pause(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
	return nil
}

// Resume resumes the live session transcription.
func (s *LiveSessionControl) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
	return nil
}

// Stop stops the live session daemon by canceling its context.
func (s *LiveSessionControl) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

// IsPaused reports whether the session is currently paused.
func (s *LiveSessionControl) IsPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

// Server assembles and runs the moonshine serve sidecar daemon components.
type Server struct {
	cfg        ServerConfig
	hub        *Hub
	dispatcher *Dispatcher
	mgr        *Manager
	sessCtrl   *LiveSessionControl
}

// NewServer initializes a Server from cfg.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Session == nil {
		if cfg.Transcriber == nil {
			return nil, fmt.Errorf("serve: Transcriber or Session is required")
		}
		if cfg.AudioSource == nil {
			return nil, fmt.Errorf("serve: AudioSource or Session is required")
		}
	}
	if cfg.Hub == nil {
		cfg.Hub = NewHub()
	}
	if cfg.Agent == nil {
		cfg.Agent = ExternalAgent{}
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}

	return &Server{
		cfg: cfg,
		hub: cfg.Hub,
	}, nil
}

// Hub returns the daemon's event Hub.
func (s *Server) Hub() *Hub {
	return s.hub
}

// Run executes the daemon until ctx is cancelled or an error occurs.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.sessCtrl = &LiveSessionControl{cancel: cancel}

	speaker := s.cfg.Speaker
	if speaker == nil {
		ttsSpeaker := NewTTSSpeaker("en_us")
		defer ttsSpeaker.Close()
		speaker = ttsSpeaker
	}

	// Barge-in guard: if AudioSource supports SetMutedFunc (e.g. *audio.MicCapture)
	if muter, ok := s.cfg.AudioSource.(interface{ SetMutedFunc(f func() bool) }); ok {
		muter.SetMutedFunc(func() bool {
			return speaker.Speaking() || s.sessCtrl.IsPaused()
		})
	}

	s.dispatcher = NewDispatcher(speaker, s.hub, s.sessCtrl, s.cfg.AllowActions)

	if len(s.cfg.Transports) > 0 {
		s.mgr = NewManager(s.cfg.Transports...)
		if err := s.mgr.Start(ctx); err != nil {
			return fmt.Errorf("starting transports: %w", err)
		}
		defer s.mgr.Close()

		// Dispatch inbound actions from Transports Manager -> Dispatcher
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case req, ok := <-s.mgr.Actions():
					if !ok {
						return
					}
					res := s.dispatcher.Handle(ctx, req)
					s.mgr.Publish(res)
				}
			}
		}()
	}

	agentRunner := NewAgentRunner(s.cfg.Agent, ActionSinkFunc(func(ctx context.Context, req event.ActionRequest) (event.ActionResult, error) {
		res := s.dispatcher.Handle(ctx, req)
		return res, nil
	}))

	subID, eventsCh := s.hub.Subscribe()
	defer s.hub.Unsubscribe(subID)

	agentEventsCh := make(chan event.TranscriptEvent, 16)
	go func() {
		defer close(agentEventsCh)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventsCh:
				if !ok {
					return
				}
				if te, ok := ev.(event.TranscriptEvent); ok {
					select {
					case agentEventsCh <- te:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	go agentRunner.Run(ctx, agentEventsCh)

	sess := s.cfg.Session
	if sess == nil {
		liveSess, err := session.NewLive(s.cfg.Transcriber, s.cfg.AudioSource, s.cfg.PollInterval)
		if err != nil {
			return fmt.Errorf("creating live session: %w", err)
		}
		sess = liveSess
	}

	go s.hub.IngestWithAudio(ctx, sess.Updates(), s.cfg.IncludeAudio)
	go sess.Run(ctx)

	<-ctx.Done()
	return nil
}
