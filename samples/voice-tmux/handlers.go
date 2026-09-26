package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

var (
	runRe        = regexp.MustCompile(`(?i)^\s*(run it|execute|hit enter|enter)\s*\.?\s*$`)
	interruptRe  = regexp.MustCompile(`(?i)^\s*(interrupt|cancel|control c|ctrl c)\s*\.?\s*$`)
	clearRe      = regexp.MustCompile(`(?i)^\s*(clear|clear screen)\s*\.?\s*$`)
	newWindowRe  = regexp.MustCompile(`(?i)^\s*(new window|new tab)\s*\.?\s*$`)
	splitRightRe = regexp.MustCompile(`(?i)^\s*(split|split right|split horizontal|split horizontally)\s*\.?\s*$`)
	splitDownRe  = regexp.MustCompile(`(?i)^\s*(split down|split vertical|split vertically)\s*\.?\s*$`)
	nextWindowRe = regexp.MustCompile(`(?i)^\s*(next window|next tab)\s*\.?\s*$`)
	prevWindowRe = regexp.MustCompile(`(?i)^\s*(previous window|prev window|previous tab|prev tab)\s*\.?\s*$`)
	scrollUpRe   = regexp.MustCompile(`(?i)^\s*(scroll up|page up)\s*\.?\s*$`)
	scrollDownRe = regexp.MustCompile(`(?i)^\s*(scroll down|page down)\s*\.?\s*$`)
	pauseRe      = regexp.MustCompile(`(?i)^\s*(stop listening|pause listening)\s*\.?\s*$`)
	resumeRe     = regexp.MustCompile(`(?i)^\s*(resume listening|start listening)\s*\.?\s*$`)
)

// controlHandler handles fast-path tmux control commands and session management.
type controlHandler struct {
	tmux         *TmuxClient
	speakConfirm bool
	debug        bool
}

func (c *controlHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	text := strings.TrimSpace(line.Text)
	if text == "" {
		return nil
	}

	switch {
	case pauseRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: stop listening\n", ts())
		}
		fmt.Printf("[%s] [control] pause session\n", ts())
		return []serveapi.ActionRequest{{Verb: "session.pause"}}

	case resumeRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: resume listening\n", ts())
		}
		fmt.Printf("[%s] [control] resume session\n", ts())
		return []serveapi.ActionRequest{{Verb: "session.resume"}}

	case runRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: run it -> send Enter\n", ts())
		}
		fmt.Printf("[%s] [control] Enter (run command)\n", ts())
		if err := c.tmux.SendKeys("Enter"); err != nil {
			fmt.Printf("[%s] [error] tmux Enter: %v\n", ts(), err)
		}
		if c.speakConfirm {
			args, _ := json.Marshal(serveapi.SpeakArgs{Text: "Running."})
			return []serveapi.ActionRequest{{Verb: "speak", Args: args}}
		}
		return []serveapi.ActionRequest{{Verb: "none"}} // non-empty sentinel so composite handler stops

	case interruptRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: interrupt -> send C-c\n", ts())
		}
		fmt.Printf("[%s] [control] Ctrl-C (interrupt)\n", ts())
		if err := c.tmux.SendKeys("C-c"); err != nil {
			fmt.Printf("[%s] [error] tmux C-c: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case clearRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: clear -> send C-l\n", ts())
		}
		fmt.Printf("[%s] [control] Ctrl-L (clear screen)\n", ts())
		if err := c.tmux.SendKeys("C-l"); err != nil {
			fmt.Printf("[%s] [error] tmux C-l: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case newWindowRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: new window\n", ts())
		}
		fmt.Printf("[%s] [control] new-window\n", ts())
		if err := c.tmux.NewWindow(); err != nil {
			fmt.Printf("[%s] [error] tmux new-window: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case splitRightRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: split right\n", ts())
		}
		fmt.Printf("[%s] [control] split-window -h\n", ts())
		if err := c.tmux.SplitWindow(true); err != nil {
			fmt.Printf("[%s] [error] tmux split-window -h: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case splitDownRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: split down\n", ts())
		}
		fmt.Printf("[%s] [control] split-window -v\n", ts())
		if err := c.tmux.SplitWindow(false); err != nil {
			fmt.Printf("[%s] [error] tmux split-window -v: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case nextWindowRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: next window\n", ts())
		}
		fmt.Printf("[%s] [control] next-window\n", ts())
		if err := c.tmux.NextWindow(); err != nil {
			fmt.Printf("[%s] [error] tmux next-window: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case prevWindowRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: previous window\n", ts())
		}
		fmt.Printf("[%s] [control] previous-window\n", ts())
		if err := c.tmux.PreviousWindow(); err != nil {
			fmt.Printf("[%s] [error] tmux previous-window: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case scrollUpRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: scroll up\n", ts())
		}
		fmt.Printf("[%s] [control] copy-mode scroll up\n", ts())
		if err := c.tmux.CopyModeScroll(true); err != nil {
			fmt.Printf("[%s] [error] tmux scroll up: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	case scrollDownRe.MatchString(text):
		if c.debug {
			fmt.Printf("[%s] [debug] control matched: scroll down\n", ts())
		}
		fmt.Printf("[%s] [control] copy-mode scroll down\n", ts())
		if err := c.tmux.CopyModeScroll(false); err != nil {
			fmt.Printf("[%s] [error] tmux scroll down: %v\n", ts(), err)
		}
		return []serveapi.ActionRequest{{Verb: "none"}}

	default:
		return nil // Not a control command; fall through to dictationHandler
	}
}

// normalizeDictation formats spoken speech text for terminal input:
//   - If raw is true, leaves text untouched.
//   - Trims surrounding whitespace.
//   - Strips trailing sentence punctuation (. ? ! , ;).
//   - Lowercases the initial character UNLESS the second character is also uppercase
//     (e.g., "Git status." -> "git status", but "NPM test" -> "NPM test").
func normalizeDictation(text string, raw bool) string {
	text = strings.TrimSpace(text)
	if raw || text == "" {
		return text
	}

	// 1. Strip trailing punctuation
	text = strings.TrimRight(text, ".?!,;")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// 2. Lowercase leading character unless followed by another uppercase letter or digit (acronym check)
	runes := []rune(text)
	if len(runes) > 0 && unicode.IsUpper(runes[0]) {
		isAcronym := len(runes) > 1 && (unicode.IsUpper(runes[1]) || unicode.IsDigit(runes[1]))
		if !isAcronym {
			runes[0] = unicode.ToLower(runes[0])
			text = string(runes)
		}
	}

	return text
}

// dictationHandler types finalized speech literally into the target tmux pane.
// SAFETY: Never appends Enter or executes commands automatically.
type dictationHandler struct {
	tmux          *TmuxClient
	minConfidence float32
	speakConfirm  bool
	raw           bool
	debug         bool
}

func (d *dictationHandler) OnFinalizedLine(ctx context.Context, line serveapi.Line) []serveapi.ActionRequest {
	text := strings.TrimSpace(line.Text)
	if text == "" {
		return nil
	}

	conf := line.MeanConfidence()
	if d.minConfidence > 0 && conf > 0 && conf < d.minConfidence {
		fmt.Printf("[%s] [gating] low confidence (%.0f%% < %.0f%%) -- ignored: %q\n",
			ts(), conf*100, d.minConfidence*100, text)
		if d.speakConfirm {
			args, _ := json.Marshal(serveapi.SpeakArgs{Text: "Please repeat."})
			return []serveapi.ActionRequest{{Verb: "speak", Args: args}}
		}
		return nil
	}

	normalized := normalizeDictation(text, d.raw)
	if normalized == "" {
		return nil
	}

	if d.debug {
		fmt.Printf("[%s] [debug] dictation: raw=%q -> typed=%q (conf: %.0f%%)\n", ts(), text, normalized, conf*100)
	}
	fmt.Printf("[%s] [type] %s (conf: %.0f%%)\n", ts(), normalized, conf*100)

	// Send literal text with trailing space for seamless word chaining
	if err := d.tmux.SendLiteral(normalized + " "); err != nil {
		fmt.Printf("[%s] [error] tmux send-literal: %v\n", ts(), err)
	}

	return nil
}
