package main

import (
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-isatty"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// interactiveProgress reports whether an animated spinner makes sense right
// now: not asking for machine-readable output, colour/cursor tricks aren't
// suppressed, and stderr is actually a terminal (not redirected/piped).
func interactiveProgress() bool {
	return !jsonOutput() && !noColor() && isatty.IsTerminal(os.Stderr.Fd())
}

// withProgress runs fn, printing a status indicator to stderr the whole
// time so long-running steps (model load, decode, inference) don't look
// hung. In an interactive terminal that's an animated spinner with elapsed
// time, overwritten in place; otherwise (--json, NO_COLOR, or stderr
// redirected/piped) it's a single plain "label..." line so scripted/logged
// output stays clean.
func withProgress(label string, fn func() error) error {
	if !interactiveProgress() {
		if !jsonOutput() {
			fmt.Fprintln(os.Stderr, muted(label+"..."))
		}
		return fn()
	}

	done := make(chan error, 1)
	go func() { done <- fn() }()

	start := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case err := <-done:
			fmt.Fprint(os.Stderr, "\r\033[2K")
			return err
		case <-ticker.C:
			fmt.Fprintf(os.Stderr, "\r\033[2K%s %s %s",
				styleWarn.Render(spinnerFrames[i%len(spinnerFrames)]),
				muted(label+"..."),
				styleMuted.Render(fmt.Sprintf("(%.1fs)", time.Since(start).Seconds())))
			i++
		}
	}
}
