package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The frame lays every list tab out the same way: a header (the title, the
// filter options with the one shown marked, a summary), the notes a tab adds
// under it (a search box, a notice, a problem), the list's column names and
// rows with the selection in view, and the selected item's details: in a panel
// to the right of the list from detailsBesideMinWidth columns, directly under
// the list on a narrower terminal. The details are wrapped to their width,
// never cut: under the list, the list gives up rows first; beside it, when
// they are taller than the screen their last line says how many more there
// are.

// detailsBesideMinWidth: from this width the details stand beside the list.
const detailsBesideMinWidth = 120

// listFrame is what a list tab shows, for the frame to lay out.
type listFrame struct {
	title      string
	filters    []string // the filter options in order; none on a tab without a filter
	filter     int      // the option shown
	summary    string   // after the filters, faint; on a line of its own when they leave no room
	notes      []string // under the header, already styled
	waiting    string   // shown instead of the list until there is something to list ("" then)
	empty      string   // shown when there are no rows
	rows       int
	minDetails int // the least room kept under the list for the details, so the list keeps its height
	// heading is the column names, if the list has any, and row the row i,
	// both width wide at most (the selection mark comes before them).
	heading func(width int) string
	row     func(i, width int) string
	// isTitle, if set, says whether row i is a section's title: drawn as it
	// is, without a mark, and brought into view with the rows under it; the
	// tab never selects it.
	isTitle func(i int) bool
	// details is the selected item's, already styled, for a panel width wide;
	// beside says whether it stands beside the list, with the room of its height.
	details func(s styles, width int, beside bool) []string
}

// frameSplit is the list's width and that of the details panel beside it, 0
// when the details go under the list.
func frameSplit(width int) (list, panel int) {
	if width < detailsBesideMinWidth {
		return width, 0
	}
	panel = width * 2 / 5
	return width - panel - lipgloss.Width(panelRule), panel
}

// detailWidth is the width the details have on a width-wide screen.
func detailWidth(width int) int {
	if _, panel := frameSplit(width); panel > 0 {
		return panel
	}
	return width
}

// panelRule separates the list from the details beside it.
const panelRule = " │ "

// above is how many lines come before the rows: the header, the notes and
// the column names.
func (f listFrame) above(width int) int {
	n := len(f.headerLines(measure, width)) + len(f.notes)
	if f.heading != nil {
		n++
	}
	return n
}

// visible is how many rows of the list a width × height screen shows.
func (f listFrame) visible(width, height int) int {
	if _, panel := frameSplit(width); panel > 0 {
		return max(1, height-f.above(width))
	}
	return max(1, height-f.above(width)-1-max(f.minDetails, len(f.detailLines(measure, width, false))))
}

// detailLines are the details wrapped to width.
func (f listFrame) detailLines(s styles, width int, beside bool) []string {
	var lines []string
	for _, l := range f.details(s, width, beside) {
		lines = append(lines, strings.Split(ansi.Wrap(l, width, "/-"), "\n")...)
	}
	return lines
}

// keep is where the selection and the first row shown go on a width × height
// screen: the selection among the rows, and in view, moving the list as little
// as it can; going up, the selection's section title comes into view too when
// it fits.
func (f listFrame) keep(cursor, top, width, height int) (int, int) {
	cursor = max(0, min(cursor, f.rows-1))
	n := f.visible(width, height)
	if cursor < top {
		top = cursor
		if f.isTitle != nil {
			title := cursor
			for title > 0 && !f.isTitle(title) {
				title--
			}
			if cursor-title < n {
				top = title
			}
		}
	}
	if cursor >= top+n {
		top = cursor - n + 1
	}
	return cursor, max(0, top)
}

// headerLines are the title and the filter options (the one shown in
// brackets, so it reads without colour too), then the summary: on the same
// line when it fits the width, under it otherwise.
func (f listFrame) headerLines(s styles, width int) []string {
	head := s.section.Render(f.title)
	if len(f.filters) > 0 {
		options := make([]string, len(f.filters))
		for i, o := range f.filters {
			options[i] = s.dim.Render(" " + o + " ")
			if i == len(f.filters)-1 {
				options[i] = s.dim.Render(" " + o) // nothing to keep aligned after the last
			}
			if i == f.filter {
				options[i] = "[" + s.filter.Render(o) + "]"
			}
		}
		head += "  " + strings.Join(options, " ")
	}
	switch {
	case f.summary == "":
		return []string{head}
	case lipgloss.Width(head)+3+lipgloss.Width(f.summary) <= width:
		return []string{head + "   " + s.dim.Render(f.summary)}
	}
	return append([]string{head}, wrapStyled(s.dim, f.summary, width)...)
}

// view is the tab on a width × height screen, the list scrolled to top with
// the row cursor selected.
func (f listFrame) view(s styles, width, height, cursor, top int) string {
	lines := append(f.headerLines(s, width), f.notes...)
	switch {
	case f.waiting != "":
		return strings.Join(append(lines, f.waiting), "\n")
	case f.rows == 0:
		return strings.Join(append(lines, "", s.dim.Render(f.empty)), "\n")
	}
	listWidth, panel := frameSplit(width)
	var list []string
	if f.heading != nil {
		list = append(list, s.dim.Render("  "+f.heading(listWidth-2)))
	}
	for i := top; i < f.rows && i < top+f.visible(width, height); i++ {
		if f.isTitle != nil && f.isTitle(i) {
			list = append(list, ansi.Truncate(f.row(i, listWidth), listWidth, "…"))
			continue
		}
		list = append(list, s.mark(i == cursor)+ansi.Truncate(f.row(i, listWidth-2), listWidth-2, "…"))
	}
	if panel == 0 {
		lines = append(lines, list...)
		lines = append(lines, s.dim.Render(strings.Repeat("─", width)))
		return strings.Join(append(lines, f.detailLines(s, width, false)...), "\n")
	}
	details := f.detailLines(s, panel, true)
	if room := height - len(lines); len(details) > room {
		details = append(details[:max(0, room-1)], s.dim.Render(fmt.Sprintf("… %d more lines", len(details)-room+1)))
	}
	for i := range max(len(list), len(details), height-len(lines)) {
		left, right := "", ""
		if i < len(list) {
			left = list[i]
		}
		if i < len(details) {
			right = details[i]
		}
		rule := panelRule
		if right == "" {
			rule = strings.TrimRight(rule, " ")
		}
		lines = append(lines, left+strings.Repeat(" ", max(0, listWidth-lipgloss.Width(left)))+s.dim.Render(rule)+right)
	}
	return strings.Join(lines, "\n")
}

// waitText is a list's waiting text: text until done, then "".
func waitText(done bool, text string) string {
	if done {
		return ""
	}
	return text
}

// listNotes are the notes most list tabs show under their header: a problem
// reading what they list, or else the last notice.
func listNotes(s styles, problem string, n notice, width int) []string {
	switch {
	case problem != "":
		return []string{s.fail.Render(clip(problem, width))}
	case n.text != "":
		return []string{s.byLevel(n.level).Render(clip(n.text, width))}
	}
	return nil
}

// columns lays texts out two spaces apart, each clipped to its width and
// padded to it but the last.
func columns(widths []int, texts ...string) string {
	cells := make([]string, len(texts))
	for i, t := range texts {
		cells[i] = clip(t, widths[i])
		if i < len(texts)-1 {
			cells[i] = fmt.Sprintf("%-*s", widths[i], cells[i])
		}
	}
	return strings.Join(cells, "  ")
}

// shares splits width among columns: each of the first takes its percent,
// the last what is left.
func shares(width int, percents ...int) []int {
	out := make([]int, len(percents)+1)
	left := width
	for i, p := range percents {
		out[i] = width * p / 100
		left -= out[i]
	}
	out[len(percents)] = left
	return out
}

// labelled is a fact in the details beside a list: a faint label, its value.
func labelled(s styles, label, value string) string {
	return s.dim.Render(label+": ") + value
}
