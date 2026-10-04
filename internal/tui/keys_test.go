package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/schedule"
)

// footer is the screen's last line, the key hints.
func (h *harness) footer() string {
	lines := strings.Split(h.screen(), "\n")
	return lines[len(lines)-1]
}

func TestTabAndShiftTabGoRoundTheTabs(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	for _, want := range []string{"2 Answers", "3 Schedule", "4 Jobs", "5 Config", "1 Dashboard"} {
		h.press("tab")
		if got := h.stroked(); got != want {
			t.Errorf("tab: %q, want %q", got, want)
		}
	}
	h.press("shift+tab")
	if got := h.stroked(); got != "5 Config" {
		t.Errorf("shift+tab from the Dashboard: %q, want 5 Config", got)
	}
}

func TestRunStopAndSetupWorkFromAnyTab(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.jobsTab()
	h.press("r")
	if h.stroked() != "1 Dashboard" || !strings.Contains(h.screen(), "Start a run") {
		t.Errorf("r from Jobs does not start a run on the Dashboard:\n%s", h.screen())
	}
	h.press("esc", tabKey(tabAnswers), "x")
	h.shows("No run in progress.")
	h.press(tabKey(tabSchedule), "s")
	h.shows("Setup checklist")
}

func TestEachTabTakesItsKeysAndNotTheOldOnes(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.remembered()
	if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Tuesday}, Start: 14 * 60, End: 16 * 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.open(100, 30)

	h.answersTab()
	h.press("e") // gone: Enter edits
	h.shows("Answers  [pending]")
	if strings.Contains(h.screen(), "> ") {
		t.Errorf("e opened the editor:\n%s", h.screen())
	}
	h.press("l")
	h.shows("[approved]")
	h.press("h")
	h.shows("[pending]")

	h.jobsTab()
	h.press("left") // round the filters, backwards
	h.shows("[skipped]")
	h.press("right")
	h.selectJob("Go Developer") // a job with a link
	h.press("o", "c")           // gone: Enter opens, Esc clears
	if len(h.opened) > 0 {
		t.Errorf("o opened %q", h.opened)
	}
	h.press("/", "umbr", "enter")
	h.shows(`matching "umbr" (esc clears)`, "esc clears · ")
	h.press("esc")
	h.shows("Go Developer")
	h.selectJob("Go Developer")
	h.press("enter")
	if len(h.opened) != 1 {
		t.Errorf("enter opened %q", h.opened)
	}

	h.scheduleTab()
	h.press("a") // gone: n adds
	if strings.Contains(h.screen(), "Add a schedule window") {
		t.Errorf("a opened the form:\n%s", h.screen())
	}
	h.press("n")
	h.shows("Add a schedule window")
	h.press("esc", "t")
	h.shows("The daemon's unit is not installed: i installs it.")
	h.press("i", "y", "t")
	h.shows("Stop the daemon?") // the unit runs: t stops it
}

func TestGlobalKeysAreTypedWhileAValueIs(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(100, 30)
	h.jobsTab()
	h.press("/", "rxs1q?", "tab")
	h.shows("search: rxs1q?")
	if h.stroked() != "4 Jobs" || h.quit {
		t.Errorf("a global key acted while typing:\n%s", h.screen())
	}
}

func TestEveryKeyInTheFooterIsInTheHelp(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.remembered()
	h.open(100, 30)
	for _, tab := range []string{"1", "2", "3", "4", "5"} {
		h.press(tab)
		footer := h.footer()
		h.press("?")
		help := h.screen()
		for _, hint := range strings.Split(footer, " · ") {
			key, _, _ := strings.Cut(hint, " ")
			if !strings.Contains(help, "  "+key+" ") && !strings.Contains(help, "  "+key+",") {
				t.Errorf("tab %s: %q is in the footer, not in the help:\n%s", tab, key, help)
			}
		}
		h.press("esc")
	}
}
