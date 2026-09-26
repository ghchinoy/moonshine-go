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

// ListPanes returns active tmux panes formatted as "session:window.pane (command)".
func ListPanes() ([]string, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{session_name}:#{window_index}.#{pane_index} (#{pane_current_command})").Output()
	if err != nil {
		return nil, err
	}
	var panes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			panes = append(panes, trimmed)
		}
	}
	return panes, nil
}

// NewTmuxClient creates a new TmuxClient after verifying tmux is available and target pane exists.
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

		panes, _ := ListPanes()

		if target == "" {
			var sb strings.Builder
			sb.WriteString("-target flag is required to prevent accidentally sending keystrokes into an unintended window/pane.\n")
			if len(panes) > 0 {
				sb.WriteString("Available tmux panes:\n")
				for _, p := range panes {
					sb.WriteString(fmt.Sprintf("  - %s\n", p))
				}
				targetSuggestion := strings.Split(panes[0], " ")[0]
				sb.WriteString(fmt.Sprintf("Example usage: go run . -target %q\n", targetSuggestion))
			}
			sb.WriteString("(Use -dry-run to test voice commands safely without sending keystrokes to a live shell).")
			return nil, fmt.Errorf("%s", sb.String())
		}

		// Verify target pane exists
		checkCmd := exec.Command(tmuxPath, "list-panes", "-t", target)
		if err := checkCmd.Run(); err != nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("target pane %q not found in active tmux sessions.\n", target))
			if len(panes) > 0 {
				sb.WriteString("Available tmux panes:\n")
				for _, p := range panes {
					sb.WriteString(fmt.Sprintf("  - %s\n", p))
				}
			}
			return nil, fmt.Errorf("%s", sb.String())
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
