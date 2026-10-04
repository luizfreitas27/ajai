package tui

import (
	"slices"
	"strings"
	"testing"
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

func TestRunStopAndSetupWorkFromEveryTab(t *testing.T) {
	for _, from := range []tab{tabDashboard, tabAnswers, tabSchedule, tabJobs, tabConfig} {
		h := newHarness(t)
		h.open(100, 30)
		h.press(tabKey(from), "r")
		if h.stroked() != "1 Dashboard" || !strings.Contains(h.screen(), "Start a run") {
			t.Errorf("r from %s does not start a run on the Dashboard:\n%s", tabNames[from], h.screen())
		}
		h.press("esc", tabKey(from), "x")
		h.shows("No run in progress.")
		h.press(tabKey(from), "s")
		h.shows("Setup checklist")
	}
}

func TestWithoutAConfigRunAndStopLeaveTheChecklistInFront(t *testing.T) {
	h := newHarness(t)
	h.removeConfig()
	h.open(100, 30)
	h.press(tabKey(tabConfig), "r")
	if strings.Contains(h.screen(), "Start a run") || !strings.Contains(h.screen(), "Setup checklist") {
		t.Errorf("r opened the run form without a config:\n%s", h.screen())
	}
	h.press("enter", "x")
	if len(h.started) > 0 {
		t.Errorf("a run started without a config: %q", h.started)
	}
}

func TestTheOldKeysDoNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tab      tab
		before   []string // to the state the old key is pressed in
		key      string
		unwanted string // what the old key used to bring
	}{
		{"e no longer edits an answer", tabAnswers, nil, "e", "Answer: "},
		{"c no longer clears a search", tabJobs, []string{"/", "umbr", "enter"}, "c", "Go Developer"},
		{"a no longer adds a window", tabSchedule, nil, "a", "Add a schedule window"},
		{"tab no longer filters the jobs", tabJobs, nil, "tab", "[applied]"},
	} {
		h := newHarness(t)
		h.someJobs()
		h.remembered()
		h.open(100, 30)
		h.press(tabKey(tc.tab))
		h.press(tc.before...)
		h.press(tc.key)
		if strings.Contains(h.screen(), tc.unwanted) {
			t.Errorf("%s:\n%s", tc.name, h.screen())
		}
	}
}

func TestOpeningAJobAndTheDaemonKeysMoved(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(100, 30)
	h.jobsTab()
	h.selectJob("Go Developer") // a job with a link
	h.press("o")                // gone: Enter opens
	if len(h.opened) > 0 {
		t.Errorf("o opened %q", h.opened)
	}
	h.press("enter")
	if len(h.opened) != 1 {
		t.Errorf("enter opened %q", h.opened)
	}

	h.scheduleTab()
	before := len(h.systemd.calls)
	h.press("s") // the setup checklist now, never the daemon
	h.shows("Setup checklist")
	if got := h.systemctlSince(before); slices.Contains(got, "start ajai.service") {
		t.Errorf("s started the daemon: %q", got)
	}
	h.press("esc", "t")
	h.shows("The daemon's unit is not installed: i installs it.")
	h.press("i", "y", "t")
	h.shows("Stop the daemon?") // the unit runs: t stops it
}

func TestEachTabTakesItsFilterAndSearchKeys(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.remembered()
	h.open(100, 30)
	h.answersTab()
	h.press("l")
	h.shows("[approved]")
	h.press("h")
	h.shows("[pending]")
	h.jobsTab()
	h.press("left") // round the filters, backwards
	h.shows("[skipped]")
	h.press("right", "/", "umbr", "enter")
	h.shows(`matching "umbr" (esc clears)`, "esc clears · ")
	h.press("esc")
	h.shows("Go Developer")
	h.scheduleTab()
	h.press("n")
	h.shows("Add a schedule window")
}

func TestGlobalKeysAreTypedWhileAValueIs(t *testing.T) {
	for _, tc := range []struct {
		name string
		tab  tab
		open []string // to the editor
	}{
		{"the jobs search", tabJobs, []string{"/"}},
		{"an answer's editor", tabAnswers, []string{"enter"}},
		{"a setting's editor", tabConfig, []string{"enter"}},
		{"the schedule window form", tabSchedule, []string{"n"}},
		{"the run form", tabDashboard, []string{"r"}},
	} {
		h := newHarness(t)
		h.someJobs()
		h.remembered()
		h.open(100, 30)
		h.press(tabKey(tc.tab))
		h.press(tc.open...)
		if !h.m.(Model).typing() {
			t.Fatalf("%s: not typing:\n%s", tc.name, h.screen())
		}
		h.press("r", "x", "s", "1", "q", "?", "tab")
		m := h.m.(Model)
		if h.quit || m.help || m.setupShown || m.tab != tc.tab || !m.typing() {
			t.Errorf("%s: a global key acted while typing:\n%s", tc.name, h.screen())
		}
	}
}

func TestTheChecklistsHelpListsOnlyWhatWorksThere(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("s", "?")
	h.shows("Everywhere", "Setup checklist", "check again")
	if strings.Contains(h.screen(), "start a run") || strings.Contains(h.screen(), "stop the run in progress") {
		t.Errorf("the checklist's help offers keys that do something else there:\n%s", h.screen())
	}
}

func TestEveryKeyInTheFooterIsInTheHelp(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.remembered()
	h.open(100, 30)
	for _, screen := range []string{"1", "2", "3", "4", "5", "s"} {
		h.press(screen)
		footer := h.footer()
		h.press("?")
		help := h.screen()
		for _, hint := range strings.Split(footer, " · ") {
			key, _, _ := strings.Cut(hint, " ")
			if !strings.Contains(help, "  "+key+" ") && !strings.Contains(help, "  "+key+",") {
				t.Errorf("screen %s: %q is in the footer, not in the help:\n%s", screen, key, help)
			}
		}
		h.press("esc")
	}
}

func TestApprovingAQuestionWithNoAnswerSaysEnterGivesOne(t *testing.T) {
	h := newHarness(t)
	h.remembered()
	h.open(100, 30)
	h.answersTab()
	h.selectAnswer("Are you willing") // remembered with no answer yet
	h.press("a")
	h.shows("This question has no answer yet: Enter gives it one.")
}
