package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/config"
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
	h.shows("Answers  [pending]  approved   3 pending")
	h.press("tab")
	h.shows("Answers   pending  [approved]   1 approved")
}

func TestAnAnswerIsCorrectedInThePanelBesideTheList(t *testing.T) {
	h := newHarness(t)
	h.remembered()
	h.open(120, 30) // the narrowest panel
	h.answersTab()
	h.selectAnswer("Are you willing")
	h.shows("│ kind: radio", "│ answer: none yet: e gives it one", "│ from: the form", "│ options", "│   Yes", "│   No, not now")
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

func TestTheFramesArithmetic(t *testing.T) {
	for _, tc := range []struct{ width, list, panel, detail int }{
		{80, 80, 0, 80}, {119, 119, 0, 119}, {120, 69, 48, 48}, {140, 81, 56, 56},
	} {
		list, panel := frameSplit(tc.width)
		if list != tc.list || panel != tc.panel || detailWidth(tc.width) != tc.detail {
			t.Errorf("width %d: list %d panel %d detail %d, want %d %d %d", tc.width, list, panel, detailWidth(tc.width), tc.list, tc.panel, tc.detail)
		}
	}
	f := listFrame{title: "T", rows: 10, heading: func(int) string { return "h" }, details: func(styles, int, bool) []string { return nil }}
	for _, tc := range []struct{ cursor, top, wantCursor, wantTop int }{
		{-3, 0, 0, 0}, {12, 0, 9, 5}, {2, 5, 2, 2}, {6, 0, 6, 2}, {5, 3, 5, 3},
	} {
		// 140×7: a header and the column names leave 5 rows beside the details
		if c, top := f.keep(tc.cursor, tc.top, 140, 7); c != tc.wantCursor || top != tc.wantTop {
			t.Errorf("keep(%d, %d) = %d, %d, want %d, %d", tc.cursor, tc.top, c, top, tc.wantCursor, tc.wantTop)
		}
	}
	if got := shares(10, 45, 30); got[0] != 4 || got[1] != 3 || got[2] != 3 {
		t.Errorf("shares(10, 45, 30) = %v", got)
	}
	if got := columns([]int{3, 4, 2}, "abcdef", "x", "long"); got != "ab…  x     l…" {
		t.Errorf("columns: %q", got)
	}
}

func TestOnTheSmallestTerminalTheSummaryTakesALineOfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(80, 24)
	h.jobsTab()
	lines := strings.Split(h.screen(), "\n")
	if !strings.HasPrefix(lines[2], "Jobs  [all]") || strings.TrimSpace(lines[3]) != "applied 1 · external 1 · failed 0 · skipped 1" {
		t.Errorf("the counts are not whole under the filters:\n%s", h.screen())
	}
	h.remembered()
	h.answersTab()
	h.golden("answers-smallest")
}

func TestBesideTheListALongLineWrapsAndTooManyLinesSayHowManyMore(t *testing.T) {
	h := newHarness(t)
	var questions []string
	for i := range 20 {
		questions = append(questions, fmt.Sprintf("Question %02d? yes", i))
	}
	link := "https://www.linkedin.com/jobs/view/4012345678/?refId=abcdefghijklmnop"
	h.recordJob(1, store.Job{JobID: "1", Outcome: store.Applied, Title: "Long", JobLink: link, Questions: strings.Join(questions, "; ")})
	h.open(120, 24)
	h.jobsTab()
	var panel strings.Builder
	for _, l := range strings.Split(h.screen(), "\n") {
		if _, right, found := strings.Cut(l, panelRule); found {
			panel.WriteString(right)
			if strings.Contains(right, "…") && !strings.Contains(right, "more lines") {
				t.Errorf("a line of the panel was cut: %q", right)
			}
		}
	}
	if !strings.Contains(panel.String(), "4012345678/?refId=abcdefghijklmnop") {
		t.Errorf("the link is not whole across the panel's lines:\n%s", h.screen())
	}
	h.shows("Question 00? yes", "more lines")
	if strings.Contains(h.screen(), "Question 19? yes") {
		t.Errorf("more lines than the screen holds:\n%s", h.screen())
	}
}

// cutEditors are the panel's editor lines (they start with the "> " prompt)
// that the panel had to cut, or to wrap: then the line after one holds
// tail, the end of what is typed.
func (h *harness) cutEditors(tail string) []string {
	var cut []string
	for _, l := range strings.Split(h.screen(), "\n") {
		_, right, _ := strings.Cut(l, panelRule)
		if strings.HasPrefix(right, "> ") && strings.Contains(right, "…") || tail != "" && strings.Contains(right, tail) && !strings.HasPrefix(right, "> ") {
			cut = append(cut, right)
		}
	}
	return cut
}

func TestConfigAndTheChecklistOnTheFrame(t *testing.T) {
	h := newHarness(t)
	h.writeConfig()
	h.open(80, 24)
	h.press(tabKey(tabConfig))
	h.golden("config-smallest")
	h.send(tea.WindowSizeMsg{Width: 140, Height: 30})
	h.moveTo("first_name")
	h.shows("│ Your first name")
	h.golden("config-beside")
	for _, l := range strings.Split(h.screen(), "\n") { // section titles keep no mark, beside the details too
		if strings.HasPrefix(l, selected+"personals") || strings.HasPrefix(l, "  personals") {
			t.Errorf("a section title is drawn as a row: %q", l)
		}
	}
	h.press("1", "s")
	h.shows("Setup checklist", "│ config:")
	h.golden("setup-beside")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.golden("setup-smallest")
}

func TestTheEditorsInThePanelFitIt(t *testing.T) {
	h := newHarness(t)
	h.writeConfig()
	file, err := config.LoadFile(h.p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Config.SetChecked("search.terms", "Dev Python, QA, Software Engineer, Backend Developer, Golang Engineer"); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(h.p.ConfigFile, file.Config); err != nil {
		t.Fatal(err)
	}
	h.open(120, 30) // the narrowest panel
	h.press(tabKey(tabConfig))
	h.moveTo("terms")
	h.press("enter")
	h.shows("│ Edit search.terms")
	if cut := h.cutEditors("Golang Engineer"); len(cut) > 0 {
		t.Errorf("the setting's editor is wider than the panel: %q", cut)
	}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.press("right") // typing on: the input lays its text out again
	if cut := h.cutEditors("Golang Engineer"); len(cut) > 0 {
		t.Errorf("resized, the setting's editor is wider than the panel: %q", cut)
	}
	h.press("esc", "1", "s", "i")
	h.shows("│ Import from the Python bot")
	if cut := h.cutEditors(""); len(cut) > 0 {
		t.Errorf("the import's folder box is wider than the panel: %q", cut)
	}
}

func TestAPathShowsUnderHomeAsTildeButIsEditedAsItIs(t *testing.T) {
	h := newHarness(t)
	h.writeConfig()
	h.open(100, 30)
	h.press(tabKey(tabConfig))
	h.moveTo("resume")
	h.shows(selected + "resume                             ~/resumes/cv.pdf")
	h.press("enter")
	if !strings.Contains(h.screen(), "> "+filepath.Join(h.p.DataDir, "resumes", "cv.pdf")) {
		t.Errorf("the editor does not hold the path as it is:\n%s", h.screen())
	}
}
