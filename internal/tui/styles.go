package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// styles are the TUI's colours, the terminal printer's palette, all made by one
// renderer so colour can be turned off (--no-color, NO_COLOR, tests). Nothing
// is adaptive: an adaptive colour would make the renderer ask the terminal for
// its background while Bubble Tea reads the keyboard, and the answer would
// arrive as key presses. Dim text is faint, as in the printer, which every
// terminal draws against its own background.
type styles struct {
	plain, brand, tab, activeTab, dim, ok, warn, fail, key, section lipgloss.Style
	label                                                           lipgloss.Style   // what a value is, beside it
	filter                                                          lipgloss.Style   // the filter option shown
	logo                                                            []lipgloss.Style // the logo's rows, in its flame
	stroke                                                          lipgloss.Style   // the selection's bar and the active tab's stroke
}

// accent is flame orange, the colour of what is active or selected: the active
// tab, the selection, titles and key names. It is the same mid tone as the
// state colours (green, amber and red, which keep meaning ok, warning and
// failure), so it reads on a dark and a light background alike.
const accent = lipgloss.Color("#EA580C")

// hints draws a line of key hints, "key what · key what": each key in the
// accent, what it does faint.
func (s styles) hints(line string) string {
	parts := strings.Split(line, " · ")
	for i, p := range parts {
		key, what, _ := strings.Cut(p, " ")
		parts[i] = s.key.Render(key) + s.dim.Render(" "+what)
	}
	return strings.Join(parts, s.dim.Render(" · "))
}

// mark starts a list row: a flame bar when the row is the selection.
func (s styles) mark(selected bool) string {
	if selected {
		return s.stroke.Render("▌") + " "
	}
	return "  "
}

// byLevel is the style of a notice.
func (s styles) byLevel(l level) lipgloss.Style {
	switch l {
	case levelWarn:
		return s.warn
	case levelFail:
		return s.fail
	}
	return s.ok
}

func newStyles(r *lipgloss.Renderer) styles {
	st := styles{
		plain:     r.NewStyle(),
		brand:     r.NewStyle().Bold(true).Foreground(accent),
		activeTab: r.NewStyle().Bold(true).Foreground(accent),
		tab:       r.NewStyle().Faint(true),
		dim:       r.NewStyle().Faint(true),
		ok:        r.NewStyle().Foreground(lipgloss.Color("#16A34A")),
		warn:      r.NewStyle().Foreground(lipgloss.Color("#D97706")),
		fail:      r.NewStyle().Foreground(lipgloss.Color("#DC2626")),
		key:       r.NewStyle().Foreground(accent),
		section:   r.NewStyle().Bold(true).Foreground(accent),
		label:     r.NewStyle().Bold(true),
		filter:    r.NewStyle().Bold(true).Underline(true).Foreground(accent),
		stroke:    r.NewStyle().Foreground(accent),
	}
	for _, c := range flame {
		st.logo = append(st.logo, r.NewStyle().Foreground(c))
	}
	return st
}
