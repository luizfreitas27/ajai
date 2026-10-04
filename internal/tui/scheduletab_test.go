package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

func (h *harness) scheduleTab() { h.t.Helper(); h.press(tabKey(tabSchedule)) }

// addWindowByForm fills the add form: start and end typed, the rest as given.
func (h *harness) addWindowByForm(start, end string, keysOnDays ...string) {
	h.t.Helper()
	h.press("n")
	h.press(keysOnDays...)
	h.press("down", "ctrl+u", start, "down", "ctrl+u", end, "enter")
}

func TestAWindowIsAddedInAFormCheckedLikeTheCommand(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.scheduleTab()
	h.shows("No schedule windows: n adds one.")
	h.press("n")
	h.shows("Add a schedule window", "[x] Mon", "[x] Fri", "[ ] Sat", "‹ the config's ›", "‹ live ›",
		"↑↓ field · ←→ day or choice · space picks · enter adds · esc cancels")
	h.golden("schedule-form")
	h.press("esc")

	// Saturday too; 9 to 11 (the 1 and 9 are typed, not tab keys).
	h.addWindowByForm("09:00", "11:00", "right", "right", "right", "right", "right", " ")
	h.shows("Added window 1:", "09:00-11:00", "open until 11:00", "window 1 open now, until 11:00") // it is Monday 09:30
	windows, _ := h.st.ListWindows()
	if len(windows) != 1 || len(windows[0].Days) != 6 || windows[0].Start != 9*60 || windows[0].End != 11*60 || !windows[0].Enabled {
		t.Fatalf("%+v", windows)
	}

	// Refused like `ajai schedule add`: a bad time, an overlap.
	h.addWindowByForm("9", "10:00")
	h.shows(`Refused: start: "9" is not a time of day`)
	h.press("esc")
	h.addWindowByForm("10:00", "12:00")
	h.shows("Refused: overlaps window 1")
	h.press("esc")
	if windows, _ := h.st.ListWindows(); len(windows) != 1 {
		t.Errorf("refused windows were added: %+v", windows)
	}
}

func TestAWindowIsTurnedOffAndOnAndDeleted(t *testing.T) {
	h := newHarness(t)
	id, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Tuesday}, Start: 14 * 60, End: 16 * 60, Market: "brasil", DryRun: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	h.open(100, 30)
	h.scheduleTab()
	h.shows("Tue", "14:00-16:00", "brasil", "dry run", "on", "Tue 06 Oct 14:00", "next opening Tue 06 Oct 14:00, window 1")
	h.golden("schedule")
	h.press(" ")
	h.shows("Window 1 is off.", " off")
	if w, _ := h.st.GetWindow(id); w.Enabled {
		t.Fatal("still on")
	}
	// While it is off, another window takes its hours: it cannot go back on.
	if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Tuesday}, Start: 15 * 60, End: 17 * 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.press("1", tabKey(tabSchedule)) // the tab reads the windows again
	h.selectWindow(1)
	h.press(" ")
	h.shows("Window 1 stays off: overlaps window 2")
	h.press("d")
	h.shows("Delete window 1", "y deletes · n keeps")
	h.press("n")
	if w, _ := h.st.GetWindow(id); w == nil {
		t.Fatal("n keeps it")
	}
	h.press("d", "y")
	h.shows("Deleted window 1")
	if w, _ := h.st.GetWindow(id); w != nil {
		t.Errorf("not deleted: %+v", w)
	}
}

// selectWindow moves the selection to window id.
func (h *harness) selectWindow(id int) {
	h.t.Helper()
	up := make([]string, 10) // to the top: the tab takes no g yet
	for i := range up {
		up[i] = "up"
	}
	h.selectRow(fmt.Sprintf("window %d", id), up, 10, func(row string) bool {
		return strings.HasPrefix(row, selected+strconv.Itoa(id)+" ")
	})
}

func TestTheSelectionStaysOnItsWindowWhenTheListChanges(t *testing.T) {
	h := newHarness(t)
	for _, day := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday} {
		if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{day}, Start: 9 * 60, End: 10 * 60, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	h.open(100, 30)
	h.scheduleTab()
	h.selectWindow(2)
	if _, err := h.st.DeleteWindow(1); err != nil { // `ajai schedule rm 1` elsewhere
		t.Fatal(err)
	}
	h.press("1", tabKey(tabSchedule))
	h.press("d")
	h.shows("Delete window 2 (Tue")
}

func TestTheFormOpensAsAWholeWindowAndARefusalAfterEscIsStillSaid(t *testing.T) {
	h := newHarness(t)
	h.open(80, 24)
	h.scheduleTab()
	h.press("n")
	h.golden("schedule-form-smallest")
	h.press("enter") // as it opens: Monday to Friday, 09:00 to 11:00
	h.shows("Added window 1: Mon-Fri 09:00-11:00.")

	// A refusal that comes after its form was closed.
	h.press("n")
	h.send(windowChangedMsg{form: 99, refusal: "overlaps window 1"}) // an older form's
	h.shows("Add a schedule window")
	h.press("esc")
	h.send(windowChangedMsg{form: 2, refusal: "overlaps window 1"})
	h.shows("Not added: overlaps window 1")
}

func TestTheListFitsTheSmallestTerminalScrollsAndKeepsUp(t *testing.T) {
	h := newHarness(t)
	for i := range 20 {
		start := schedule.Clock((6 + i%12) * 60)
		day := schedule.Week[i/12]
		if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{day}, Start: start, End: start + 30, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	h.open(80, 24)
	h.scheduleTab()
	h.shows("Mon", "06:00-06:30", "Mon 12 Oct 06:00") // the next opening fits in 80 columns
	h.golden("schedule-smallest")
	for range 19 {
		h.press("down")
	}
	h.press("d")
	h.shows("Delete window 20", "y deletes · n keeps") // the last one, in view, its question too
	h.press("n")

	// `ajai schedule add` in another terminal: the tab sees it on the next refresh.
	if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Sunday}, Start: 20 * 60, End: 21 * 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.refresh()
	h.press("down")
	h.shows(selected + "21")
}

func TestTheSelectedWindowShowsItsNextOpeningsAndWhatItsLastOneDid(t *testing.T) {
	h := newHarness(t)
	ran, _ := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Tuesday}, Start: 14 * 60, End: 16 * 60, Market: "brasil", DryRun: true, Enabled: true})
	skipped, _ := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Friday}, Start: 9 * 60, End: 10 * 60, Enabled: true})
	failed, _ := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Saturday}, Start: 9 * 60, End: 10 * 60, Enabled: true})
	quiet, _ := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Monday}, Start: 20 * 60, End: 21 * 60, Enabled: true})
	if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Sunday}, Start: 9 * 60, End: 10 * 60}); err != nil { // never opened, and off
		t.Fatal(err)
	}
	h.pastRun(time.Date(2026, 9, 28, 20, 0, 0, 0, time.Local), "", false, store.Run{Applied: 1}) // no reason recorded
	h.pastRun(time.Date(2026, 9, 29, 14, 0, 0, 0, time.Local), "brasil", true, store.Run{Applied: 3, Skipped: 4, Reason: "the search ran out of jobs"})
	runs, _ := h.st.ListRuns(2)
	for _, o := range []struct {
		k       schedule.Key
		outcome schedule.Outcome
		detail  string
		run     int64
	}{
		{schedule.Key{WindowID: ran, Date: "2026-09-29"}, schedule.OutcomeStarted, "", runs[0].ID},
		{schedule.Key{WindowID: skipped, Date: "2026-10-02"}, schedule.OutcomeSkipped, "a run started from the terminal (pid 4242) at 08:50", 0}, // as the daemon writes it
		{schedule.Key{WindowID: failed, Date: "2026-10-03"}, schedule.OutcomeFailed, "the config file has errors", 0},
		{schedule.Key{WindowID: quiet, Date: "2026-09-28"}, schedule.OutcomeStarted, "", runs[1].ID},
	} {
		if err := h.st.RecordOpening(o.k, o.outcome, o.detail, o.run); err != nil {
			t.Fatal(err)
		}
	}
	h.open(100, 30)
	h.scheduleTab()
	h.selectWindow(int(ran))
	h.shows("Window 1 · Tue 14:00-16:00 · brasil · dry run · on", "next: Tue 06 Oct 14:00 · Tue 13 Oct 14:00 · Tue 20 Oct 14:00",
		"last opening: Tue 29 Sep: started a run", "  applied 3 · external 0 · failed 0 · skipped 4 · the search ran out of jobs")
	h.selectWindow(int(skipped))
	h.shows("last opening: Fri 02 Oct: skipped: a run started from the terminal (pid 4242) at 08:50")
	h.selectWindow(int(failed))
	h.shows("last opening: Sat 03 Oct: the run could not start: the config file has errors")
	h.selectWindow(int(quiet))
	h.shows("  applied 1 · external 0 · failed 0 · skipped 0 · ended") // no reason recorded: still how it ended
	h.selectWindow(5)
	h.shows("next: none: the window is off", "last opening: none handled yet")

	h.send(tea.WindowSizeMsg{Width: 140, Height: 30})
	h.selectWindow(int(ran))
	h.shows("│ Window 1", "│ days: Tue", "│ hours: 14:00-16:00", "│ market: brasil", "│ mode: dry run", "│ state: on", "│ next openings",
		"│   Tue 13 Oct 14:00", "│ last opening", "│   Tue 29 Sep: started a run", "│   applied 3 · external 0 · failed 0 · skipped 4")
	h.golden("schedule-beside")
}

func TestMovingPastTheTopOfTheWindowsKeepsTheFirst(t *testing.T) {
	h := newHarness(t)
	if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Tuesday}, Start: 14 * 60, End: 16 * 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.open(100, 30)
	h.scheduleTab()
	h.press("up", "k", "pgup")
	if row := h.selectedRow(); !strings.HasPrefix(row, selected+"1 ") {
		t.Errorf("the first window is not selected: %q", row)
	}
}
