// Package tui provides bubbletea/lipgloss UIs for moonshine-go's live
// transcription command.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ghchinoy/moonshine-go/internal/session"
)

func noColor() bool {
	return os.Getenv("NO_COLOR") != "" || os.Getenv("MOONSHINE_NO_COLOR") != ""
}

func style(color string, bold bool) lipgloss.Style {
	if noColor() {
		return lipgloss.NewStyle()
	}
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	if bold {
		s = s.Bold(true)
	}
	return s
}

var (
	styleAccent = style("33", true)
	stylePass   = style("34", false)
	styleWarn   = style("214", false)
	styleFail   = style("196", false)
	styleMuted  = style("240", false)
	styleID     = style("86", false)
)

type updateMsg session.Update
type doneMsg struct{}

func waitForUpdate(ch <-chan session.Update) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return doneMsg{}
		}
		return updateMsg(u)
	}
}

type liveModel struct {
	updates  <-chan session.Update
	cancel   context.CancelFunc
	quitting bool

	transcript session.Update
	haveUpdate bool
}

// NewLive builds a bubbletea model for a live session. cancel stops the
// underlying session (mic + stream) when the user quits.
func NewLive(updates <-chan session.Update, cancel context.CancelFunc) tea.Model {
	return liveModel{updates: updates, cancel: cancel}
}

func (m liveModel) Init() tea.Cmd { return waitForUpdate(m.updates) }

func (m liveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			if !m.quitting {
				m.quitting = true
				m.cancel()
			}
			return m, waitForUpdate(m.updates)
		}
	case updateMsg:
		m.transcript = session.Update(msg)
		m.haveUpdate = true
		if m.transcript.Done {
			return m, tea.Quit
		}
		return m, waitForUpdate(m.updates)
	case doneMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m liveModel) View() string {
	var b strings.Builder
	b.WriteString(styleAccent.Render("moonshine live") + styleMuted.Render("  (q to quit)") + "\n\n")

	if !m.haveUpdate {
		b.WriteString(styleMuted.Render("listening...") + "\n")
	} else if m.transcript.Err != nil {
		b.WriteString(styleFail.Render("error: "+m.transcript.Err.Error()) + "\n")
	} else {
		lines := m.transcript.Transcript.Lines
		if len(lines) == 0 {
			b.WriteString(styleMuted.Render("listening...") + "\n")
		}
		for _, l := range lines {
			text := l.Text
			if text == "" {
				continue
			}
			if label := l.SpeakerLabel(); label != "" {
				text = "[" + label + "] " + text
			}
			if l.IsComplete {
				b.WriteString(stylePass.Render(text) + "\n")
			} else {
				b.WriteString(styleWarn.Render(text+" \u258f") + "\n") // trailing cursor glyph
			}
		}
	}

	b.WriteString("\n" + styleMuted.Render(strings.Repeat("-", 50)) + "\n")
	b.WriteString(statsLine(m.transcript))
	if m.quitting {
		b.WriteString("\n" + styleMuted.Render("stopping..."))
	}
	return b.String()
}

func statsLine(u session.Update) string {
	ttft := "-"
	if u.TTFT > 0 {
		ttft = fmtDuration(u.TTFT)
	}
	return fmt.Sprintf("%s ttft=%s  %s elapsed=%s  %s last_poll=%s",
		styleID.Render("stats:"), ttft,
		styleMuted.Render("|"), fmtDuration(u.Elapsed),
		styleMuted.Render("|"), fmtDuration(u.PollLatency),
	)
}

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
