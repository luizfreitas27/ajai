package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The frame lays every list tab out the same way: a header (the title, the
// filter options with the one shown marked, a summary), the notes a tab adds
// under it (a search box, a notice, a problem), the list's column names and
// rows with the selection in view, and the selected item's details: in a panel
// to the right of the list from detailsBesideMinWidth columns, directly under
// the list on a narrower terminal. The details are never cut: the list gives up
// rows first.

// detailsBesideMinWidth: from this width the details stand beside the list.
const detailsBesideMinWidth = 120

// listFrame is what a list tab shows, for the frame to lay out.
type listFrame struct {
	title      string
	filters    []string // the filter options in order; none on a tab without a filter
	filter     int      // the option shown
	summary    string   // after the filters, faint
	notes      []string // under the header, already styled
	loading    bool     // nothing read yet
	empty      string   // shown when there are no rows
	rows       int
	minDetails int // the least room kept under the list for the details, so the list keeps its height
	// heading is the column names and row the row i, both width wide at most
	// (the selection mark comes before them).
	heading func(width int) string
	row     func(i, width int) string
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
	return width - panel - len(panelRule), panel
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

// visible is how many rows of the list a width × height screen shows.
func (f listFrame) visible(width, height int) int {
	_, panel := frameSplit(width)
	used := 1 + len(f.notes) + 1 // the header, the notes, the column names
	if panel > 0 {
		return max(1, height-used)
	}
	return max(1, height-used-1-max(f.minDetails, len(f.details(measure, width, false))))
}

// scroll keeps the selection among rows rows and in view of a window of
// visible rows starting at top.
func scroll(cursor, top, rows, visible int) (int, int) {
	cursor = max(0, min(cursor, rows-1))
	top = max(0, min(top, cursor))
	if cursor >= top+visible {
		top = cursor - visible + 1
	}
	return cursor, top
}

// header is the title, the filter options (the one shown in brackets, so it
// reads without colour too) and the summary.
func (f listFrame) header(s styles) string {
	head := s.section.Render(f.title)
	if len(f.filters) > 0 {
		options := make([]string, len(f.filters))
		for i, o := range f.filters {
			options[i] = s.dim.Render(" " + o + " ")
			if i == f.filter {
				options[i] = "[" + s.filter.Render(o) + "]"
			}
		}
		head += "  " + strings.Join(options, " ")
	}
	if f.summary != "" {
		head += "   " + s.dim.Render(f.summary)
	}
	return head
}

// view is the tab on a width × height screen, the list scrolled to top with
// the row cursor selected.
func (f listFrame) view(s styles, width, height, cursor, top int) string {
	lines := append([]string{f.header(s)}, f.notes...)
	switch {
	case f.loading:
		return strings.Join(append(lines, "Reading…"), "\n")
	case f.rows == 0:
		return strings.Join(append(lines, "", s.dim.Render(f.empty)), "\n")
	}
	listWidth, panel := frameSplit(width)
	list := []string{s.dim.Render("  " + f.heading(listWidth-2))}
	for i := top; i < f.rows && i < top+f.visible(width, height); i++ {
		list = append(list, s.mark(i == cursor)+ansi.Truncate(f.row(i, listWidth-2), listWidth-2, "…"))
	}
	if panel == 0 {
		lines = append(lines, list...)
		lines = append(lines, s.dim.Render(strings.Repeat("─", width)))
		for _, l := range f.details(s, width, false) {
			lines = append(lines, ansi.Truncate(l, width, "…"))
		}
		return strings.Join(lines, "\n")
	}
	details := f.details(s, panel, true)
	for i := range max(len(list), len(details), height-len(lines)) {
		left, right := "", ""
		if i < len(list) {
			left = list[i]
		}
		if i < len(details) {
			right = ansi.Truncate(details[i], panel, "…")
		}
		rule := panelRule
		if right == "" {
			rule = strings.TrimRight(rule, " ")
		}
		lines = append(lines, left+strings.Repeat(" ", max(0, listWidth-lipgloss.Width(left)))+s.dim.Render(rule)+right)
	}
	return strings.Join(lines, "\n")
}
