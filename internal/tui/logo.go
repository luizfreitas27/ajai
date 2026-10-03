package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// logo is "ajai" in the block letters of Omarchy's logo: the "a" is Omarchy's,
// the "j" and the "i" are drawn in the same stroke.
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

// flame colours the logo row by row, top to bottom: a dark ember at the tips,
// then red, orange and amber, down to yellow at the base.
var flame = []lipgloss.Color{"#B8101E", "#D82814", "#F2420A", "#FB6403", "#FF8709", "#FFAA1B", "#FFCA39", "#FFE85C"}

const (
	logoWidth = 38    // the widest row
	logoGap   = "   " // between the logo and the summary beside it
	// logoBeside: from this width the logo stands beside the Dashboard's
	// summary; narrower, it goes above it if the height allows.
	logoBeside = 100
	labelWidth = 14 // a summary line's label and the gap after it
)

// logoLines are the logo's rows, each as wide as the widest, in its colour.
func (s styles) logoLines() []string {
	out := make([]string, len(logo))
	for i, row := range logo {
		out[i] = s.logo[i].Render(fmt.Sprintf("%-*s", logoWidth, row))
	}
	return out
}

// fact is one line of the Dashboard's summary: what it is (blank on a line
// that continues the one before) and its text.
type fact struct{ label, text string }

// summaryLines lays the summary out with the logo: beside it when the width
// holds both, each text wrapped in its column; above it when the height still
// leaves the last runs (runs of them) and the log their room; otherwise alone.
func summaryLines(s styles, facts []fact, width, height, runs int) []string {
	lines := func(textWidth int) []string {
		var out []string
		for _, f := range facts {
			text := f.text
			if textWidth > 0 {
				text = ansi.Wrap(text, textWidth, "")
			}
			for i, t := range strings.Split(text, "\n") {
				label := f.label
				if i > 0 {
					label = ""
				}
				out = append(out, s.label.Render(fmt.Sprintf("%-*s", labelWidth-2, label))+"  "+t)
			}
		}
		return out
	}
	art := s.logoLines()
	if width >= logoBeside {
		text := lines(width - logoWidth - len(logoGap) - labelWidth)
		out := make([]string, max(len(art), len(text)))
		for i := range out {
			left := strings.Repeat(" ", logoWidth)
			if i < len(art) {
				left = art[i]
			}
			out[i] = left
			if i < len(text) {
				out[i] += logoGap + text[i]
			}
		}
		return out
	}
	summary := lines(0)
	rest := 3 + runs + 2 + minLogLines // the last runs (a blank, the title, the heading or "none yet", the rows), the log (a blank, the title, its lines)
	if height >= len(art)+1+len(summary)+rest {
		return append(append(art, ""), summary...)
	}
	return summary
}
