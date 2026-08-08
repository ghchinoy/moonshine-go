package agentflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

const (
	defaultTriggerThreshold float32 = 0.7
	defaultPromptThreshold  float32 = 0.55
)

// FlowFunction is the signature for a conversation flow handler.
type FlowFunction func(dialog *Dialog) error

// UnmatchedHandler receives speech that no flow, global, or prompt claimed.
type UnmatchedHandler func(utterance string)

// AgentFlow is the central voice-agent manager and DSL builder.
type AgentFlow struct {
	mu sync.Mutex

	flowOrder []string
	flows     map[string]FlowFunction

	globalOrder []string
	globals     map[string]FlowFunction

	// Flow-scoped globals ("cancel", "start over") only apply while a flow is active.
	flowScopedGlobals map[string]bool

	languageCode     string
	archName         string
	voiceID          string
	modelDirectory   string
	wantsMicrophone  bool
	wantsSpeech      bool
	triggerThreshold float32

	onProgressFunc    func(progress float64, message string)
	speakOverride     func(text string) error
	heardCallbacks    []func(utterance string)
	saidCallbacks     []func(text string)
	errorCallbacks    []func(err error)
	unmatchedHandlers []UnmatchedHandler

	embedding EmbeddingBackend
	matcher   *PhraseMatcher

	actionSink serveapi.ActionSink

	activeDialog        *Dialog
	activeTriggerPhrase string
	pendingContinuation chan pendingAnswer
	isSpeaking          bool

	settleWaiters []*SettleSignal
	queueTask     chan struct{}
}

type pendingAnswer struct {
	text string
	err  error
}

// New creates a new AgentFlow instance with default configuration.
func New() *AgentFlow {
	af := &AgentFlow{
		flows:             make(map[string]FlowFunction),
		globals:           make(map[string]FlowFunction),
		flowScopedGlobals: make(map[string]bool),
		languageCode:      "en",
		archName:          "medium-streaming",
		wantsMicrophone:   true,
		wantsSpeech:       true,
		triggerThreshold:  defaultTriggerThreshold,
		matcher:           NewPhraseMatcher(nil),
	}

	// Built-in flow-scoped globals.
	af.addFlowScopedGlobal("cancel", func(d *Dialog) error { return d.Cancel() })
	af.addFlowScopedGlobal("start over", func(d *Dialog) error { return d.Restart() })

	return af
}

func (af *AgentFlow) addFlowScopedGlobal(phrase string, handler FlowFunction) {
	af.Always(phrase, handler)
	af.flowScopedGlobals[phrase] = true
}

// Configuration Builders

// Language sets the speech-to-text and synthesis language code (e.g. "en").
func (af *AgentFlow) Language(code string) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.languageCode = code
	return af
}

// ModelArch sets the streaming model architecture name.
func (af *AgentFlow) ModelArch(arch string) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.archName = arch
	return af
}

// Voice sets the voice ID used for text-to-speech synthesis (e.g. "kokoro_af_heart").
func (af *AgentFlow) Voice(id string) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.voiceID = id
	return af
}

// ModelsFrom sets a local directory path to load models from rather than downloading.
func (af *AgentFlow) ModelsFrom(dir string) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.modelDirectory = dir
	return af
}

// Microphone sets whether to open a live microphone input. Default: true.
func (af *AgentFlow) Microphone(enabled bool) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.wantsMicrophone = enabled
	return af
}

// Speech sets whether prompts are spoken aloud. Default: true.
func (af *AgentFlow) Speech(enabled bool) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.wantsSpeech = enabled
	return af
}

// TriggerThreshold sets the trigger-phrase matching threshold (0.0 to 1.0). Default: 0.7.
func (af *AgentFlow) TriggerThreshold(threshold float32) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.triggerThreshold = threshold
	return af
}

// OnProgress sets a progress reporting callback for model loading.
func (af *AgentFlow) OnProgress(fn func(progress float64, message string)) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.onProgressFunc = fn
	return af
}

// OnHeard adds a callback invoked with every utterance heard.
func (af *AgentFlow) OnHeard(fn func(utterance string)) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.heardCallbacks = append(af.heardCallbacks, fn)
	return af
}

// OnSaid adds a callback invoked with every text spoken by the agent.
func (af *AgentFlow) OnSaid(fn func(text string)) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.saidCallbacks = append(af.saidCallbacks, fn)
	return af
}

// OnError adds an error handler for unhandled flow errors.
func (af *AgentFlow) OnError(fn func(err error)) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.errorCallbacks = append(af.errorCallbacks, fn)
	return af
}

// ActionSink binds a serveapi.ActionSink for dispatching control-plane ActionRequests.
func (af *AgentFlow) ActionSink(sink serveapi.ActionSink) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.actionSink = sink
	return af
}

// EmitAction dispatches an ActionRequest to the configured ActionSink.
func (af *AgentFlow) EmitAction(req serveapi.ActionRequest) (serveapi.ActionResult, error) {
	af.mu.Lock()
	sink := af.actionSink
	af.mu.Unlock()

	if sink == nil {
		return serveapi.ActionResult{}, fmt.Errorf("agentflow: no ActionSink configured")
	}
	return sink.Dispatch(context.Background(), req)
}

// SpeakWith overrides the default speech synthesizer with a custom speak function.
func (af *AgentFlow) SpeakWith(fn func(text string) error) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.speakOverride = fn
	return af
}

// SetEmbeddingBackend sets an optional EmbeddingBackend for PhraseMatcher fuzzy matching.
func (af *AgentFlow) SetEmbeddingBackend(backend EmbeddingBackend) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.embedding = backend
	af.matcher = NewPhraseMatcher(backend)
	return af
}

// ListenFor registers a conversation flow for candidate trigger phrases.
func (af *AgentFlow) ListenFor(phrase string, flow FlowFunction) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	if _, exists := af.flows[phrase]; !exists {
		af.flowOrder = append(af.flowOrder, phrase)
	}
	af.flows[phrase] = flow
	return af
}

// Always registers a global handler that runs whenever phrase is heard.
func (af *AgentFlow) Always(phrase string, handler FlowFunction) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	if _, exists := af.globals[phrase]; !exists {
		af.globalOrder = append(af.globalOrder, phrase)
	}
	af.globals[phrase] = handler
	delete(af.flowScopedGlobals, phrase)
	return af
}

// Otherwise registers a handler for utterances not claimed by any flow or prompt.
func (af *AgentFlow) Otherwise(handler UnmatchedHandler) *AgentFlow {
	af.mu.Lock()
	defer af.mu.Unlock()
	af.unmatchedHandlers = append(af.unmatchedHandlers, handler)
	return af
}

// Lifecycle & Execution

// Say speaks text outside any flow.
func (af *AgentFlow) Say(text string) error {
	return af.speak(text)
}

// HandleUtterance feeds an utterance into the agent for processing.
func (af *AgentFlow) HandleUtterance(utterance string) {
	utterance = strings.TrimSpace(utterance)
	if utterance == "" {
		return
	}

	af.mu.Lock()
	heards := append([]func(string){}, af.heardCallbacks...)
	af.mu.Unlock()

	for _, fn := range heards {
		fn(utterance)
	}

	af.dispatch(utterance)
}

// IsActive returns whether a conversation flow is currently running.
func (af *AgentFlow) IsActive() bool {
	af.mu.Lock()
	defer af.mu.Unlock()
	return af.activeDialog != nil
}

// ActiveTrigger returns the trigger phrase of the running flow, if any.
func (af *AgentFlow) ActiveTrigger() string {
	af.mu.Lock()
	defer af.mu.Unlock()
	return af.activeTriggerPhrase
}

// Cancel abandons the running flow. Returns true if a flow was active.
func (af *AgentFlow) Cancel() bool {
	af.mu.Lock()
	wasActive := af.activeDialog != nil
	if wasActive {
		af.activeDialog = nil
		af.activeTriggerPhrase = ""
	}
	pending := af.pendingContinuation
	af.pendingContinuation = nil
	af.mu.Unlock()

	if pending != nil {
		select {
		case pending <- pendingAnswer{err: DialogCancelled{}}:
		default:
		}
	}

	return wasActive
}

// Internals

func (af *AgentFlow) speak(text string) error {
	if text == "" {
		return nil
	}

	af.mu.Lock()
	saids := append([]func(string){}, af.saidCallbacks...)
	override := af.speakOverride
	wantsSpeech := af.wantsSpeech
	af.isSpeaking = true
	af.mu.Unlock()

	defer func() {
		af.mu.Lock()
		af.isSpeaking = false
		af.mu.Unlock()
	}()

	for _, fn := range saids {
		fn(text)
	}

	if !wantsSpeech {
		return nil
	}

	if override != nil {
		return override(text)
	}

	// Default fallback when no TTS engine or override is bound: silent / log
	return nil
}

func (af *AgentFlow) waitForAnswer(timeout time.Duration) (string, error) {
	ch := make(chan pendingAnswer, 1)

	af.mu.Lock()
	af.pendingContinuation = ch
	af.mu.Unlock()

	af.notifySettled()

	var timer <-chan time.Time
	if timeout > 0 {
		timer = time.After(timeout)
	}

	select {
	case ans := <-ch:
		return ans.text, ans.err
	case <-timer:
		af.mu.Lock()
		if af.pendingContinuation == ch {
			af.pendingContinuation = nil
		}
		af.mu.Unlock()
		return "", DialogNoMatch{Message: "Timed out waiting for an answer"}
	}
}

func (af *AgentFlow) dispatch(utterance string) {
	trigger := af.matchTrigger(utterance)

	af.mu.Lock()
	hasGlobal := trigger != "" && af.globals[trigger] != nil
	pending := af.pendingContinuation
	active := af.activeDialog != nil
	unmatched := append([]UnmatchedHandler{}, af.unmatchedHandlers...)
	flow := af.flows[trigger]
	af.mu.Unlock()

	// Globals take precedence over everything (e.g. "cancel" mid-question).
	if hasGlobal {
		af.invokeGlobal(trigger)
		return
	}

	// Pending prompt answer continuation.
	if pending != nil {
		af.mu.Lock()
		af.pendingContinuation = nil
		af.mu.Unlock()

		select {
		case pending <- pendingAnswer{text: utterance}:
		default:
		}
		return
	}

	// Busy between prompts: ignore line rather than interleave flows.
	if active {
		return
	}

	// Trigger matching flow.
	if trigger != "" && flow != nil {
		go af.runFlow(trigger, flow)
		return
	}

	// Unmatched leftovers.
	for _, fn := range unmatched {
		fn(utterance)
	}
}

func (af *AgentFlow) matchTrigger(utterance string) string {
	af.mu.Lock()
	threshold := af.triggerThreshold
	matcher := af.matcher
	phrases := append([]string(nil), af.liveGlobals()...)
	phrases = append(phrases, af.flowOrder...)
	af.mu.Unlock()

	if len(phrases) == 0 {
		return ""
	}
	return matcher.MatchPhrases(utterance, phrases, threshold)
}

func (af *AgentFlow) liveGlobals() []string {
	if len(af.flowScopedGlobals) == 0 || (af.activeDialog != nil || af.pendingContinuation != nil) {
		return af.globalOrder
	}
	var filtered []string
	for _, g := range af.globalOrder {
		if !af.flowScopedGlobals[g] {
			filtered = append(filtered, g)
		}
	}
	return filtered
}

func (af *AgentFlow) matchKey(utterance string, groups []PhraseGroup) string {
	af.mu.Lock()
	matcher := af.matcher
	af.mu.Unlock()

	return matcher.Match(utterance, groups, defaultPromptThreshold)
}

func (af *AgentFlow) runFlow(triggerPhrase string, flow FlowFunction) {
	defer func() {
		af.mu.Lock()
		af.activeDialog = nil
		af.activeTriggerPhrase = ""
		af.mu.Unlock()
		af.notifySettled()
	}()

	for {
		dialog := NewDialog(af, triggerPhrase)
		af.mu.Lock()
		af.activeDialog = dialog
		af.activeTriggerPhrase = triggerPhrase
		af.mu.Unlock()

		err := flow(dialog)
		if err == nil {
			return
		}

		if _, isRestart := err.(DialogRestart); isRestart {
			continue // restart loop
		}
		if _, isCancelled := err.(DialogCancelled); isCancelled {
			return
		}
		if _, isNoMatch := err.(DialogNoMatch); isNoMatch {
			_ = af.speak("Sorry, I didn't get that. Let's start over.")
			return
		}

		af.mu.Lock()
		errs := append([]func(error){}, af.errorCallbacks...)
		af.mu.Unlock()

		for _, fn := range errs {
			fn(err)
		}
		return
	}
}

func (af *AgentFlow) invokeGlobal(triggerPhrase string) {
	af.mu.Lock()
	handler := af.globals[triggerPhrase]
	existing := af.activeDialog
	af.mu.Unlock()

	if handler == nil {
		return
	}

	dialog := existing
	if dialog == nil {
		dialog = NewDialog(af, triggerPhrase)
	}

	err := handler(dialog)
	if err != nil {
		if _, isCancel := err.(DialogCancelled); isCancel {
			af.Cancel()
		} else if _, isRestart := err.(DialogRestart); isRestart {
			af.mu.Lock()
			pending := af.pendingContinuation
			af.pendingContinuation = nil
			af.mu.Unlock()
			if pending != nil {
				select {
				case pending <- pendingAnswer{err: err}:
				default:
				}
			}
		} else {
			af.mu.Lock()
			errs := append([]func(error){}, af.errorCallbacks...)
			af.mu.Unlock()
			for _, fn := range errs {
				fn(err)
			}
		}
	}
}

// SettleSignal synchronization helper.

// RegisterSettle registers interest in the next time the runner comes to rest.
func (af *AgentFlow) RegisterSettle() *SettleSignal {
	s := &SettleSignal{done: make(chan struct{})}
	af.mu.Lock()
	af.settleWaiters = append(af.settleWaiters, s)
	af.mu.Unlock()
	return s
}

func (af *AgentFlow) notifySettled() {
	af.mu.Lock()
	waiters := af.settleWaiters
	af.settleWaiters = nil
	af.mu.Unlock()

	for _, w := range waiters {
		w.Signal()
	}
}

// SettleSignal is a one-shot notification signalled when the agent completes a flow step or parks on a prompt.
type SettleSignal struct {
	mu   sync.Mutex
	done chan struct{}
}

// Signal marks the signal as done.
func (s *SettleSignal) Signal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// Wait blocks until the signal is notified or ctx is done.
func (s *SettleSignal) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
