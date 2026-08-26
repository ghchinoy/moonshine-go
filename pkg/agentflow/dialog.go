package agentflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// DialogCancelled is thrown into a flow when the user (or a global handler) cancels it.
type DialogCancelled struct{}

func (e DialogCancelled) Error() string {
	return "dialog cancelled"
}

// DialogRestart is thrown into a flow when it should start again from the top.
type DialogRestart struct{}

func (e DialogRestart) Error() string {
	return "dialog restart"
}

// DialogNoMatch is returned by Ask / Confirm / Choose after retries run out.
type DialogNoMatch struct {
	Message string
}

func (e DialogNoMatch) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "no matching answer"
}

// AskOptions controls how long to wait for an answer, re-prompts, and retries.
type AskOptions struct {
	// Timeout gives up waiting for an answer after this duration and re-prompts.
	Timeout time.Duration
	// Reprompt is spoken when the answer wasn't understood. {prompt} is substituted.
	Reprompt string
	// MaxRetries is how many times to re-prompt before returning DialogNoMatch. Default: 2.
	MaxRetries int
}

// Default phrase sets for confirmation matching.
var (
	DefaultYesPhrases = []string{
		"yes", "yeah", "yep", "correct", "that's right", "sure", "affirmative", "okay", "please do", "do it",
	}
	DefaultNoPhrases = []string{
		"no", "nope", "incorrect", "that's wrong", "negative", "cancel", "don't do it", "stop",
	}
)

// Dialog represents an active conversation flow handed to a flow handler.
// Every method speaks and then waits, so a flow reads as straight-line code.
type Dialog struct {
	// TriggerPhrase is the phrase that initiated this flow.
	TriggerPhrase string
	// State is scratch space for the flow's own use; the runner never touches it.
	State map[string]any

	runner *AgentFlow
}

// NewDialog creates a Dialog associated with an AgentFlow runner.
func NewDialog(runner *AgentFlow, triggerPhrase string) *Dialog {
	return &Dialog{
		TriggerPhrase: triggerPhrase,
		State:         make(map[string]any),
		runner:        runner,
	}
}

// Say speaks text and waits for playback to finish.
func (d *Dialog) Say(text string) error {
	return d.runner.speak(text)
}

// SayStream speaks text tokens incrementally from textCh as they are generated
// (e.g. by an LLM token stream) and waits for playback to finish.
func (d *Dialog) SayStream(textCh <-chan string) error {
	return d.runner.speakStream(textCh)
}

// Ask asks an open question, speaks prompt, and returns what the user said.
func (d *Dialog) Ask(prompt string, opts ...AskOptions) (string, error) {
	options := parseAskOptions(opts)
	res, err := d.promptForAnswer(prompt, options, func(text string) (any, bool) {
		text = strings.TrimSpace(text)
		if text == "" {
			return "", false
		}
		return text, true
	})
	if err != nil {
		return "", err
	}
	return res.(string), nil
}

// Confirm asks a yes/no question.
func (d *Dialog) Confirm(prompt string, opts ...AskOptions) (bool, error) {
	options := parseAskOptions(opts)
	if options.MaxRetries == 0 {
		options.MaxRetries = 1
	}
	if options.Reprompt == "" {
		options.Reprompt = "Sorry, I didn't catch that. Was that a yes or a no? {prompt}"
	}

	yesPhrases := DefaultYesPhrases
	noPhrases := DefaultNoPhrases

	res, err := d.promptForAnswer(prompt, options, func(text string) (any, bool) {
		groups := []PhraseGroup{
			{Key: "yes", Phrases: yesPhrases},
			{Key: "no", Phrases: noPhrases},
		}
		key := d.runner.matchKey(text, groups)
		switch key {
		case "yes":
			return true, true
		case "no":
			return false, true
		default:
			return false, false
		}
	})
	if err != nil {
		return false, err
	}
	return res.(bool), nil
}

// Choose offers a set of choices and returns the key of the choice picked.
// Each map key maps to candidate selection phrases; the key itself also matches.
func (d *Dialog) Choose(prompt string, choices map[string][]string, opts ...AskOptions) (string, error) {
	options := parseAskOptions(opts)
	groups := make([]PhraseGroup, 0, len(choices))
	for key, phrases := range choices {
		allPhrases := append([]string{key}, phrases...)
		groups = append(groups, PhraseGroup{Key: key, Phrases: allPhrases})
	}

	res, err := d.promptForAnswer(prompt, options, func(text string) (any, bool) {
		matchedKey := d.runner.matchKey(text, groups)
		if matchedKey != "" {
			return matchedKey, true
		}
		return "", false
	})
	if err != nil {
		return "", err
	}
	return res.(string), nil
}

// EmitAction dispatches a serveapi.ActionRequest to the configured ActionSink.
func (d *Dialog) EmitAction(req serveapi.ActionRequest) (serveapi.ActionResult, error) {
	return d.runner.EmitAction(req)
}

// PauseListening emits a session.pause control action to pause speech recognition.
func (d *Dialog) PauseListening() (serveapi.ActionResult, error) {
	return d.EmitAction(serveapi.ActionRequest{Verb: "session.pause"})
}

// ResumeListening emits a session.resume control action to resume speech recognition.
func (d *Dialog) ResumeListening() (serveapi.ActionResult, error) {
	return d.EmitAction(serveapi.ActionRequest{Verb: "session.resume"})
}

// StopSession emits a session.stop control action to terminate the session.
func (d *Dialog) StopSession() (serveapi.ActionResult, error) {
	return d.EmitAction(serveapi.ActionRequest{Verb: "session.stop"})
}

// Display emits a display ActionRequest with card as JSON args.
func (d *Dialog) Display(card serveapi.DisplayCard) (serveapi.ActionResult, error) {
	args, err := json.Marshal(card)
	if err != nil {
		return serveapi.ActionResult{}, err
	}
	return d.EmitAction(serveapi.ActionRequest{
		Verb: "display",
		Args: args,
	})
}

// Cancel abandons the flow.
func (d *Dialog) Cancel() error {
	return DialogCancelled{}
}

// Restart runs the flow again from the beginning.
func (d *Dialog) Restart() error {
	return DialogRestart{}
}

func (d *Dialog) promptForAnswer(prompt string, options AskOptions, interpret func(string) (any, bool)) (any, error) {
	maxRetries := options.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}
	repromptTemplate := options.Reprompt
	if repromptTemplate == "" {
		repromptTemplate = "Sorry, I didn't catch that. {prompt}"
	}

	attempt := 0
	for {
		line := prompt
		if attempt > 0 {
			line = strings.ReplaceAll(repromptTemplate, "{prompt}", prompt)
		}

		if err := d.runner.speak(line); err != nil {
			return nil, err
		}

		answer, err := d.runner.waitForAnswer(options.Timeout)
		if err != nil {
			if _, isNoMatch := err.(DialogNoMatch); isNoMatch && attempt < maxRetries {
				attempt++
				continue
			}
			return nil, err
		}

		val, ok := interpret(strings.TrimSpace(answer))
		if ok {
			return val, nil
		}

		if attempt >= maxRetries {
			return nil, DialogNoMatch{Message: fmt.Sprintf("Gave up understanding: %q", answer)}
		}
		attempt++
	}
}

func parseAskOptions(opts []AskOptions) AskOptions {
	if len(opts) > 0 {
		return opts[0]
	}
	return AskOptions{MaxRetries: 2}
}

// SpellOut renders a string as a space-separated spoken form for reading back.
func SpellOut(value string) string {
	chars := make([]string, len(value))
	for i, r := range value {
		chars[i] = string(r)
	}
	return strings.Join(chars, " ")
}
