package tui

import "github.com/charmbracelet/lipgloss"

// styles are the TUI's colours, the terminal printer's palette, all made by one
// renderer so colour can be turned off (--no-color, NO_COLOR, tests). Every
// colour reads on a light and a dark background alike: an adaptive colour
// would make the renderer ask the terminal for its background while Bubble Tea
// reads the keyboard, and the answer would arrive as key presses.
type styles struct {
	brand, tab, activeTab, dim, ok, warn, fail, key, section lipgloss.Style
}

func newStyles(r *lipgloss.Renderer) styles {
	grey := lipgloss.Color("#808080")
	return styles{
		brand:     r.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED")),
		activeTab: r.NewStyle().Bold(true).Underline(true),
		tab:       r.NewStyle().Foreground(grey),
		dim:       r.NewStyle().Foreground(grey),
		ok:        r.NewStyle().Foreground(lipgloss.Color("#16A34A")),
		warn:      r.NewStyle().Foreground(lipgloss.Color("#D97706")),
		fail:      r.NewStyle().Foreground(lipgloss.Color("#DC2626")),
		key:       r.NewStyle().Foreground(lipgloss.Color("#06B6D4")),
		section:   r.NewStyle().Bold(true),
	}
}
