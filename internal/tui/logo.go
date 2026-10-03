package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// logo is "ajai" in the block letters of Omarchy's logo: the "a" is Omarchy's,
// the "j" and the "i" are drawn in the same stroke. Each row has its colour in
// flame.
var logo = []string{
	"  ▄███████         ▄█    ▄███████   ▄█",
	" ███   ███        ███   ███   ███  ███",
	" ███   ███        ███   ███   ███  ███",
	"▄███▄▄▄███        ███  ▄███▄▄▄███  ███",
	"▀███▀▀▀███        ███  ▀███▀▀▀███  ███",
	" ███   ███  ▄██   ███   ███   ███  ███",
	" ███   ███  ███   ███   ███   ███  ███",
	" ███   █▀    ▀█████▀    ███   █▀   █▀",
}

// logoWidth is the logo's widest row.
var logoWidth = func() int {
	w := 0
	for _, row := range logo {
		w = max(w, lipgloss.Width(row))
	}
	return w
}()

const (
	logoGap = "   " // between the logo and the summary beside it
	// logoBesideMinWidth: from this width the logo may stand beside the
	// Dashboard's summary; narrower, it may go above it.
	logoBesideMinWidth = 100
	labelWidth         = 12   // a summary line's label
	labelGap           = "  " // between the label and the text
)

// logoLines are the logo's rows, each in its colour.
func (s styles) logoLines() []string {
	out := make([]string, len(logo))
	for i, row := range logo {
		out[i] = s.logo[i].Render(row)
	}
	return out
}

// fact is one line of the Dashboard's summary: what it is (blank on a line
// that continues the one before) and its text.
type fact struct{ label, text string }

// summaryLines lays the summary out with the logo, which never costs the last
// runs (runs of them) and the log their room, nor cuts a line: beside the
// summary when the width holds both and the height the taller; above it when
// the height holds both; otherwise the summary goes alone.
func summaryLines(s styles, facts []fact, width, height, runs int) []string {
	summary := make([]string, len(facts))
	widest := 0
	for i, f := range facts {
		summary[i] = s.label.Render(fmt.Sprintf("%-*s", labelWidth, f.label)) + labelGap + f.text
		widest = max(widest, lipgloss.Width(summary[i]))
	}
	logoRows, below := s.logoLines(), belowSummary(runs)
	switch {
	case width >= max(logoBesideMinWidth, logoWidth+len(logoGap)+widest) && height >= max(len(logoRows), len(summary))+below:
		out := make([]string, max(len(logoRows), len(summary)))
		for i := range out {
			if i < len(logoRows) {
				out[i] = logoRows[i]
			}
			if i < len(summary) {
				out[i] += strings.Repeat(" ", logoWidth-lipgloss.Width(out[i])) + logoGap + summary[i]
			}
		}
		return out
	case height >= len(logoRows)+1+len(summary)+below:
		return append(append(logoRows, ""), summary...)
	}
	return summary
}
