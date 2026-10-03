package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/store"
)

// besideOf is the text right of the panel rule on the screen line that has
// text left of it, or "" when the line has no rule.
func (h *harness) besideOf(text string) string {
	for _, l := range strings.Split(h.screen(), "\n") {
		left, right, found := strings.Cut(l, panelRule)
		if found && strings.Contains(left, text) {
			return right
		}
	}
	return ""
}

func TestAtTheWidestTheDetailsStandBesideTheList(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(140, 30)
	h.jobsTab()
	h.selectJob("Go Developer")
	if got := h.besideOf("recorded"); got != "Go Developer" { // the details start level with the column names
		t.Errorf("beside the column names: %q, want the job's title:\n%s", got, h.screen())
	}
	h.shows("│ Acme · Recife (Remote)", "│ applied Mon 05 Oct 08:01", "│ posted 2026-10-03", "│ job: https://www.linkedin.com/jobs/view/1/",
		"│ questions answered (2)", "│   Years of Go? 3", "│   Relocate? Yes")
	rules := 0
	for _, l := range strings.Split(h.screen(), "\n") {
		if l != "" && strings.Trim(l, "─") == "" {
			rules++
		}
	}
	if rules != 1 || strings.Contains(h.screen(), "questions answered (2):") { // only the divider above the hints
		t.Errorf("the details are under the list too:\n%s", h.screen())
	}
	h.golden("jobs-beside")
}

func TestBesideTheListTheListTakesTheWholeHeight(t *testing.T) {
	h := newHarness(t)
	for i := range 60 {
		h.recordJob(i, store.Job{JobID: fmt.Sprint(i), Outcome: store.Skipped, Title: fmt.Sprintf("Job %03d", i)})
	}
	h.open(140, 30)
	h.jobsTab()
	rows := 0
	for _, l := range strings.Split(h.screen(), "\n") {
		if strings.Contains(l, "skipped   Job ") {
			rows++
		}
	}
	// 30 lines less the tab bar, two dividers and the hints (26 for the tab), less its header and the column names
	if rows != 24 {
		t.Errorf("%d rows, want 24:\n%s", rows, h.screen())
	}
}

func TestTheHeaderListsEveryFilterAndMarksTheOneShown(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(100, 30)
	h.jobsTab()
	h.shows("Jobs  [all]  applied   external   failed   skipped ")
	h.press("tab")
	h.shows("Jobs   all  [applied]  external   failed   skipped ")
	h.remembered()
	h.answersTab()
	h.shows("Answers  [pending]  approved    3 pending")
	h.press("tab")
	h.shows("Answers   pending  [approved]   1 approved")
}

func TestAnAnswerIsCorrectedInThePanelBesideTheList(t *testing.T) {
	h := newHarness(t)
	h.remembered()
	h.open(120, 30) // the narrowest panel
	h.answersTab()
	h.selectAnswer("Are you willing")
	h.shows("│ kind     radio", "│ answer   none yet: e gives it one", "│ from     the form", "│ options", "│   Yes", "│   No, not now")
	h.golden("answers-beside")
	h.press("e")
	h.shows("│ Answer: Are you willing to relocate?", "│ "+selected+"Yes")
	for _, when := range []string{"opened", "resized"} {
		if when == "resized" {
			h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
		}
		for _, l := range strings.Split(h.screen(), "\n") {
			if _, right, _ := strings.Cut(l, panelRule); strings.HasPrefix(right, "> ") && strings.Contains(right, "…") {
				t.Errorf("%s, the editor is wider than the panel, and cut: %q", when, l)
			}
		}
	}
}
