package serve

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghchinoy/moonshine-go/internal/moonshine"
	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

type noopLiveSession struct {
	updates chan session.Update
}

func (n *noopLiveSession) Run(ctx context.Context) {
	<-ctx.Done()
}

func (n *noopLiveSession) Updates() <-chan session.Update {
	return n.updates
}

// ErrSessionLimitReached is returned when CreateSession is called but the
// active session count is at or above MaxSessions.
var ErrSessionLimitReached = errors.New("serve: max session limit reached")

// scopedSpeaker wraps a shared Speaker and provides per-session Speaking() state
// so barge-in muting in one session does not affect other sessions.
type scopedSpeaker struct {
	base     Speaker
	speaking atomic.Bool
}

func (s *scopedSpeaker) Speak(ctx context.Context, text, voice string, speed float64) error {
	if s.base == nil {
		return fmt.Errorf("serve: no speaker configured")
	}
	s.speaking.Store(true)
	defer s.speaking.Store(false)
	return s.base.Speak(ctx, text, voice, speed)
}

func (s *scopedSpeaker) Speaking() bool {
	return s.speaking.Load() || (s.base != nil && s.base.Speaking())
}

func (s *scopedSpeaker) Interrupt(ctx context.Context) {
	s.speaking.Store(false)
	if interrupter, ok := s.base.(interface{ Interrupt(context.Context) }); ok {
		interrupter.Interrupt(ctx)
	}
}

// SessionManagerConfig holds options for initializing a SessionManager.
type SessionManagerConfig struct {
	Transcriber  *moonshine.Transcriber
	Speaker      Speaker
	MaxSessions  int
	PollInterval time.Duration
	AllowActions bool
	IncludeAudio bool
	Agent        AgentHandler
}

// SessionManager manages per-connection serve sessions, enforcing a maximum
// session count and isolating Hub, Dispatcher, and Stream state per session.
type SessionManager struct {
	mu          sync.Mutex
	cfg         SessionManagerConfig
	activeCount int
	nextID      uint64
	sessions    map[uint64]*ManagedSession
}

// ManagedSession holds per-session components created by SessionManager.
type ManagedSession struct {
	id         uint64
	mgr        *SessionManager
	hub        *Hub
	dispatcher *Dispatcher
	sessCtrl   *LiveSessionControl
	sess       LiveSession
	spk        *scopedSpeaker
	cancel     context.CancelFunc
	ctx        context.Context

	mu     sync.Mutex
	closed bool
}

// ID returns the unique session ID.
func (s *ManagedSession) ID() uint64 {
	return s.id
}

// Hub returns this session's isolated event Hub.
func (s *ManagedSession) Hub() *Hub {
	return s.hub
}

// Dispatcher returns this session's isolated Dispatcher.
func (s *ManagedSession) Dispatcher() *Dispatcher {
	return s.dispatcher
}

// Control returns this session's LiveSessionControl.
func (s *ManagedSession) Control() *LiveSessionControl {
	return s.sessCtrl
}

// Session returns this session's LiveSession.
func (s *ManagedSession) Session() LiveSession {
	return s.sess
}

// Close terminates the session, closing its Stream, Hub, and cancelling its context.
func (s *ManagedSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()

	s.mgr.removeSession(s.id)
	return nil
}

// NewSessionManager initializes a SessionManager with cfg.
func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 10
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	return &SessionManager{
		cfg:      cfg,
		sessions: make(map[uint64]*ManagedSession),
	}
}

// CreateSession allocates a new ManagedSession for source. Returns
// ErrSessionLimitReached if active session count is at or above MaxSessions.
func (m *SessionManager) CreateSession(ctx context.Context, source serveapi.AudioSource) (*ManagedSession, error) {
	m.mu.Lock()
	if m.cfg.MaxSessions > 0 && m.activeCount >= m.cfg.MaxSessions {
		m.mu.Unlock()
		return nil, ErrSessionLimitReached
	}
	m.activeCount++
	id := m.nextID
	m.nextID++
	m.mu.Unlock()

	sessCtx, cancel := context.WithCancel(ctx)

	hub := NewHub()
	scopedSpk := &scopedSpeaker{base: m.cfg.Speaker}
	sessCtrl := &LiveSessionControl{cancel: cancel}

	if muter, ok := source.(interface{ SetMutedFunc(f func() bool) }); ok {
		muter.SetMutedFunc(func() bool {
			return scopedSpk.Speaking() || sessCtrl.IsPaused()
		})
	}

	dispatcher := NewDispatcher(scopedSpk, hub, sessCtrl, m.cfg.AllowActions)

	var liveSess LiveSession
	if m.cfg.Transcriber != nil {
		ls, err := session.NewLive(m.cfg.Transcriber, source, m.cfg.PollInterval)
		if err != nil {
			cancel()
			m.removeSession(id)
			return nil, fmt.Errorf("serve: creating session stream: %w", err)
		}
		liveSess = ls
	} else {
		liveSess = &noopLiveSession{updates: make(chan session.Update)}
	}

	go hub.IngestWithAudio(sessCtx, liveSess.Updates(), m.cfg.IncludeAudio)
	go liveSess.Run(sessCtx)

	if m.cfg.Agent != nil {
		agentRunner := NewAgentRunner(m.cfg.Agent, ActionSinkFunc(func(c context.Context, req event.ActionRequest) (event.ActionResult, error) {
			res := dispatcher.Handle(c, req)
			return res, nil
		}))
		subID, eventsCh := hub.Subscribe()

		agentEventsCh := make(chan event.TranscriptEvent, 16)
		go func() {
			defer hub.Unsubscribe(subID)
			defer close(agentEventsCh)
			for {
				select {
				case <-sessCtx.Done():
					return
				case ev, ok := <-eventsCh:
					if !ok {
						return
					}
					if te, ok := ev.(event.TranscriptEvent); ok {
						select {
						case agentEventsCh <- te:
						case <-sessCtx.Done():
							return
						}
					}
				}
			}
		}()
		go agentRunner.Run(sessCtx, agentEventsCh)
	}

	managed := &ManagedSession{
		id:         id,
		mgr:        m,
		hub:        hub,
		dispatcher: dispatcher,
		sessCtrl:   sessCtrl,
		sess:       liveSess,
		spk:        scopedSpk,
		cancel:     cancel,
		ctx:        sessCtx,
	}

	m.mu.Lock()
	m.sessions[id] = managed
	m.mu.Unlock()

	return managed, nil
}

// ActiveSessions returns the current number of active managed sessions.
func (m *SessionManager) ActiveSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeCount
}

// removeSession removes session id and decrements activeCount.
func (m *SessionManager) removeSession(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; ok {
		delete(m.sessions, id)
		m.activeCount--
	}
}

// Close closes all active sessions.
func (m *SessionManager) Close() error {
	m.mu.Lock()
	sessions := make([]*ManagedSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()

	for _, s := range sessions {
		_ = s.Close()
	}
	return nil
}
