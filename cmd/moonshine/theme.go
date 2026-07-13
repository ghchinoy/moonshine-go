package main

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// noColor reports whether colour output should be suppressed, per the
// NO_COLOR convention (https://no-color.org/) or MOONSHINE_NO_COLOR.
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

// Semantic colour tokens, consistent across the moonshine-go CLI.
var (
	styleAccent  = style("33", true)   // headers, section titles
	styleCommand = style("245", false) // command names / flags in help text
	stylePass    = style("34", false)  // success / final transcript lines
	styleWarn    = style("214", false) // interim / in-progress state
	styleFail    = style("196", false) // errors
	styleMuted   = style("240", false) // metadata, stats, de-emphasis
	styleID      = style("86", false)  // ids, sample rates, durations
)

func header(s string) string { return styleAccent.Render(s) }
func muted(s string) string  { return styleMuted.Render(s) }
