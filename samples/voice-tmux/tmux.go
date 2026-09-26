package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TmuxClient wraps the tmux command line utility.
type TmuxClient struct {
	Target string
	DryRun bool
}

// NewTmuxClient creates a new TmuxClient after verifying tmux is available.
func NewTmuxClient(target string, dryRun bool) (*TmuxClient, error) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux executable not found on PATH: please install tmux to use this sample")
	}

	client := &TmuxClient{
		Target: target,
		DryRun: dryRun,
	}

	// Preflight check: verify tmux server is running (unless dryRun is set)
	if !dryRun {
		cmd := exec.Command(tmuxPath, "has-session")
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("no active tmux session found: please start a tmux session ('tmux new' or 'tmux') before running voice-tmux")
		}
	}

	return client, nil
}

// execTmux executes a tmux command or prints it if DryRun is enabled.
func (t *TmuxClient) execTmux(args ...string) error {
	if t.DryRun {
		fmt.Printf("[dry-run] tmux %s\n", strings.Join(args, " "))
		return nil
	}
	cmd := exec.Command("tmux", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// targetArgs returns the target flag arguments if Target is specified.
func (t *TmuxClient) targetArgs() []string {
	if t.Target != "" {
		return []string{"-t", t.Target}
	}
	return nil
}

// SendKeys sends named keys (e.g. "Enter", "C-c", "C-l") to the target pane.
func (t *TmuxClient) SendKeys(keys ...string) error {
	args := []string{"send-keys"}
	args = append(args, t.targetArgs()...)
	args = append(args, keys...)
	return t.execTmux(args...)
}

// SendLiteral sends literal text to the target pane (-l flag).
func (t *TmuxClient) SendLiteral(text string) error {
	args := []string{"send-keys"}
	args = append(args, t.targetArgs()...)
	args = append(args, "-l", text)
	return t.execTmux(args...)
}

// NewWindow creates a new tmux window.
func (t *TmuxClient) NewWindow() error {
	args := []string{"new-window"}
	args = append(args, t.targetArgs()...)
	return t.execTmux(args...)
}

// SplitWindow splits the current window horizontally or vertically.
func (t *TmuxClient) SplitWindow(horizontal bool) error {
	args := []string{"split-window"}
	args = append(args, t.targetArgs()...)
	if horizontal {
		args = append(args, "-h")
	} else {
		args = append(args, "-v")
	}
	return t.execTmux(args...)
}

// NextWindow selects the next tmux window.
func (t *TmuxClient) NextWindow() error {
	args := []string{"next-window"}
	args = append(args, t.targetArgs()...)
	return t.execTmux(args...)
}

// PreviousWindow selects the previous tmux window.
func (t *TmuxClient) PreviousWindow() error {
	args := []string{"previous-window"}
	args = append(args, t.targetArgs()...)
	return t.execTmux(args...)
}

// CopyModeScroll enters copy mode and scrolls up or down.
func (t *TmuxClient) CopyModeScroll(up bool) error {
	if up {
		// Enter copy mode and scroll up
		args := []string{"copy-mode"}
		args = append(args, t.targetArgs()...)
		args = append(args, "-u")
		return t.execTmux(args...)
	}
	// Send PageDown in copy mode
	return t.SendKeys("PageDown")
}
