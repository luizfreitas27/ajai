package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/schedule"
)

func (h *harness) scheduleTab() { h.t.Helper(); h.press(tabKey(tabSchedule)) }

// addWindowByForm fills the add form: start and end typed, the rest as given.
func (h *harness) addWindowByForm(start, end string, keysOnDays ...string) {
	h.t.Helper()
	h.press("a")
	h.press(keysOnDays...)
	h.press("down", start, "down", end, "enter")
}

func TestAWindowIsAddedInAFormCheckedLikeTheCommand(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.scheduleTab()
	h.shows("No schedule windows: a adds one.")
	h.press("a")
	h.shows("Add a schedule window", "[x] Mon", "[x] Fri", "[ ] Sat", "‹ the config's ›", "‹ live ›",
		"↑↓ field · ←→ day or choice · space picks · enter adds · esc cancels")
	h.golden("schedule-form")
	h.press("esc")

	// Saturday too; 9 to 11 (the 1 and 9 are typed, not tab keys).
	h.addWindowByForm("09:00", "11:00", "right", "right", "right", "right", "right", " ")
	h.shows("Added window 1:", "09:00-11:00", "Tue 06 Oct 09:00") // Monday's opening began at 09:00, before h.now
	windows, _ := h.st.ListWindows()
	if len(windows) != 1 || len(windows[0].Days) != 6 || windows[0].Start != 9*60 || windows[0].End != 11*60 || !windows[0].Enabled {
		t.Fatalf("%+v", windows)
	}

	// Refused like `aja schedule add`: a bad time, an overlap.
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
	h.shows("Tue", "14:00-16:00", "brasil", "dry run", "on", "Tue 06 Oct 14:00", "next opening: Tue 06 Oct 14:00, window 1")
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
	for range 3 {
		h.press("up")
	}
	for range 10 {
		for _, l := range strings.Split(h.screen(), "\n") {
			if strings.HasPrefix(l, "> "+strconv.Itoa(id)+" ") {
				return
			}
		}
		h.press("down")
	}
	h.t.Fatalf("no window %d:\n%s", id, h.screen())
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
	if _, err := h.st.DeleteWindow(1); err != nil { // `aja schedule rm 1` elsewhere
		t.Fatal(err)
	}
	h.press("1", tabKey(tabSchedule))
	h.press("d")
	h.shows("Delete window 2 (Tue")
}
