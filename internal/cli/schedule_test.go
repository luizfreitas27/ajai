package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// friday is 2026-10-02 12:00 local time, a Friday.
var friday = time.Date(2026, 10, 2, 12, 0, 0, 0, time.Local)

func TestScheduleAddListAndTheNextOpening(t *testing.T) {
	h := newHarness(t)
	h.now = friday
	if _, out, _ := h.run("schedule", "list"); !strings.Contains(out, "No schedule windows") || !strings.Contains(out, "aja schedule add") {
		t.Errorf("%s", out)
	}
	code, out, errText := h.run("schedule", "add", "--days", "mon-fri", "--start", "09:00", "--end", "11:00", "--market", "brasil")
	if code != 0 || !strings.Contains(out, "window 1: Mon-Fri 09:00-11:00, market brasil") || !strings.Contains(out, "Mon 05 Oct 09:00, window 1") {
		t.Fatalf("%d %s %s", code, out, errText)
	}
	code, out, _ = h.run("schedule", "add", "--days", "fri", "--start", "11:30", "--end", "13:00", "--dry-run")
	if code != 0 || !strings.Contains(out, "window 2: Fri 11:30-13:00, dry run") || !strings.Contains(out, "open now") || !strings.Contains(out, "window 2 until 13:00") {
		t.Errorf("a window open right now says so: %d %s", code, out)
	}
	_, out, _ = h.run("schedule", "list")
	if !strings.Contains(out, "1  Mon-Fri 09:00-11:00, market brasil") || !strings.Contains(out, "2  Fri 11:30-13:00, dry run") ||
		!strings.Contains(out, "next opening") {
		t.Errorf("%s", out)
	}
	if _, out, _ = h.run("status"); !strings.Contains(out, "open now") || !strings.Contains(out, "next opening") {
		t.Errorf("status shows the schedule:\n%s", out)
	}
	logs, _ := os.ReadFile(filepath.Join(h.home, "logs", mustOnlyFile(t, filepath.Join(h.home, "logs"))))
	if strings.Count(string(logs), "schedule window added") != 2 {
		t.Errorf("changes to the schedule are logged:\n%s", logs)
	}
}

func TestScheduleAddRefusesBadWindowsAndStoresNothing(t *testing.T) {
	h := newHarness(t)
	h.now = friday
	if code, _, errText := h.run("schedule", "add", "--days", "mon-fri", "--start", "09:00", "--end", "11:00"); code != 0 {
		t.Fatal(errText)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--days", "funday", "--start", "09:00", "--end", "10:00"}, `unknown weekday "funday"`},
		{[]string{"--days", "sat", "--start", "9h", "--end", "10:00"}, `--start: "9h" is not a time of day`},
		{[]string{"--days", "sat", "--start", "10:00", "--end", "10:00"}, "end 10:00 is not after start 10:00"},
		{[]string{"--days", "sat", "--start", "22:00", "--end", "02:00"}, "a window cannot cross midnight, so split one that does in two"},
		{[]string{"--days", "sat", "--start", "09:00", "--end", "10:00", "--market", "usa"}, `unknown market "usa"`},
		{[]string{"--days", "wed", "--start", "10:00", "--end", "12:00"}, "overlaps window 1 (Mon-Fri 09:00-11:00) on Wednesday"},
		{[]string{"--days", "sat"}, `required flag(s) "end", "start" not set`},
	}
	for _, tc := range cases {
		code, _, errText := h.run(append([]string{"schedule", "add"}, tc.args...)...)
		if code == 0 || !strings.Contains(errText, tc.want) {
			t.Errorf("%v: %d %s", tc.args, code, errText)
		}
	}
	if _, out, _ := h.run("schedule", "list"); strings.Count(out, "Mon-Fri") != 2 || strings.Contains(out, "  2  ") {
		t.Errorf("only the first window is stored:\n%s", out)
	}
}

func TestScheduleRmEnableAndDisable(t *testing.T) {
	h := newHarness(t)
	h.now = friday
	h.run("schedule", "add", "--days", "mon-fri", "--start", "09:00", "--end", "11:00")
	if code, out, _ := h.run("schedule", "disable", "1"); code != 0 || !strings.Contains(out, "disabled") || !strings.Contains(out, "none: every window is disabled") {
		t.Errorf("%d %s", code, out)
	}
	if _, out, _ := h.run("schedule", "list"); !strings.Contains(out, "Mon-Fri 09:00-11:00  (disabled)") {
		t.Errorf("%s", out)
	}
	// While window 1 is off, an overlapping one may be added; then 1 cannot come back on.
	if code, _, errText := h.run("schedule", "add", "--days", "mon", "--start", "10:00", "--end", "12:00"); code != 0 {
		t.Fatal(errText)
	}
	if code, _, errText := h.run("schedule", "enable", "1"); code == 0 || !strings.Contains(errText, "cannot enable window 1: overlaps window 2") {
		t.Errorf("%d %s", code, errText)
	}
	if code, out, _ := h.run("schedule", "rm", "2"); code != 0 || !strings.Contains(out, "deleted") {
		t.Errorf("%d %s", code, out)
	}
	if code, out, _ := h.run("schedule", "enable", "1"); code != 0 || !strings.Contains(out, "enabled") || !strings.Contains(out, "Mon 05 Oct 09:00") {
		t.Errorf("%d %s", code, out)
	}
	for _, args := range [][]string{{"schedule", "rm", "7"}, {"schedule", "enable", "7"}, {"schedule", "disable", "x"}} {
		if code, _, errText := h.run(args...); code == 0 || !strings.Contains(errText, "aja schedule list") {
			t.Errorf("%v: %d %s", args, code, errText)
		}
	}
}

func TestStatusSaysNothingAboutTheScheduleWithoutWindows(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	if _, out, _ := h.run("status"); strings.Contains(out, "next opening") {
		t.Errorf("%s", out)
	}
}
