package schedule

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var markets = []string{"brasil", "internacional"}

func mustDays(t *testing.T, s string) []time.Weekday {
	t.Helper()
	d, err := ParseDays(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func clock(t *testing.T, s string) Clock {
	t.Helper()
	c, err := ParseClock(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseClock(t *testing.T) {
	good := map[string]Clock{"00:00": 0, "09:00": 540, "9:30": 570, "23:59": 1439, "24:00": EndOfDay}
	for s, want := range good {
		if got, err := ParseClock(s); err != nil || got != want {
			t.Errorf("%q: %v %v", s, got, err)
		}
	}
	for _, s := range []string{"", "9", "25:00", "24:01", "12:60", "12:5", "ab:cd", "-1:00"} {
		if _, err := ParseClock(s); err == nil || !strings.Contains(err.Error(), "HH:MM") {
			t.Errorf("%q must be refused: %v", s, err)
		}
	}
}

func TestParseDaysAndWriteThemBack(t *testing.T) {
	cases := []struct{ in, out string }{
		{"mon,tue,wed,thu,fri", "Mon-Fri"},
		{"mon-fri", "Mon-Fri"},
		{"weekdays", "Mon-Fri"},
		{"Monday, Wednesday,fri", "Mon, Wed, Fri"},
		{"weekend", "Sat, Sun"},
		{"fri-mon", "Mon, Fri-Sun"}, // across the weekend
		{"daily", "daily"},
		{"sat,sat,sun", "Sat, Sun"},
		{"tue-thu,sat", "Tue-Thu, Sat"},
	}
	for _, tc := range cases {
		if got := DaysString(mustDays(t, tc.in)); got != tc.out {
			t.Errorf("%q: %q, want %q", tc.in, got, tc.out)
		}
	}
	for _, bad := range []string{"", " , ", "funday", "mon-xyz", "mo"} {
		if _, err := ParseDays(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if _, err := ParseDays("funday"); err == nil || !strings.Contains(err.Error(), `unknown weekday "funday"`) {
		t.Errorf("the message names the bad day: %v", err)
	}
}

func TestValidate(t *testing.T) {
	existing := Window{ID: 1, Days: mustDays(t, "mon-fri"), Start: clock(t, "09:00"), End: clock(t, "11:00"), Enabled: true}
	disabled := Window{ID: 2, Days: mustDays(t, "daily"), Start: clock(t, "00:00"), End: EndOfDay, Enabled: false}
	others := []Window{existing, disabled}
	w := func(days, start, end, market string) Window {
		return Window{Days: mustDays(t, days), Start: clock(t, start), End: clock(t, end), Market: market, Enabled: true}
	}
	cases := []struct {
		name string
		w    Window
		want string // "" = valid
	}{
		{"afternoon on the same days", w("mon-fri", "14:00", "16:00", "brasil"), ""},
		{"right after the other", w("mon-fri", "11:00", "12:00", ""), ""},
		{"until midnight", w("sat", "22:00", "24:00", "internacional"), ""},
		{"same hours another day", w("sat,sun", "09:00", "11:00", ""), ""},
		{"overlap", w("fri,sat", "10:30", "12:00", ""), "overlaps window 1 (Mon-Fri 09:00-11:00) on Friday"},
		{"crosses midnight", w("fri", "22:00", "02:00", ""), "crosses midnight: split it into two windows, e.g. 22:00-24:00 and 00:00-02:00"},
		{"empty", w("mon", "09:00", "09:00", ""), "end 09:00 is not after start 09:00"},
		{"unknown market", w("mon", "13:00", "14:00", "usa"), `unknown market "usa" (use brasil or internacional)`},
		{"no days", Window{Start: 60, End: 120, Enabled: true}, "at least one weekday"},
		{"starts at 24:00", Window{Days: mustDays(t, "mon"), Start: EndOfDay, End: EndOfDay, Enabled: true}, "start 24:00 is not a time of day"},
	}
	for _, tc := range cases {
		err := Validate(tc.w, others, markets)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
	// A window is not checked against itself (enable, edit).
	if err := Validate(existing, others, markets); err != nil {
		t.Errorf("itself: %v", err)
	}
}

func TestCurrentDueAndNext(t *testing.T) {
	loc := time.FixedZone("BRT", -3*3600)
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	// 2026-10-02 is a Friday.
	morning := Window{ID: 1, Days: mustDays(t, "mon-fri"), Start: clock(t, "09:00"), End: clock(t, "11:00"), Enabled: true}
	evening := Window{ID: 2, Days: mustDays(t, "tue,thu"), Start: clock(t, "19:00"), End: clock(t, "21:00"), Market: "internacional", Enabled: true}
	off := Window{ID: 3, Days: mustDays(t, "daily"), Start: clock(t, "12:00"), End: clock(t, "13:00"), Enabled: false}
	windows := []Window{morning, evening, off}
	handledToday := map[Key]bool{{WindowID: 1, Date: "2026-10-02"}: true}

	cases := []struct {
		name    string
		now     string
		handled map[Key]bool
		current int64 // 0: none
		due     int64
		next    int64
		nextAt  string
	}{
		{"opening due at the start", "2026-10-02 09:00", nil, 1, 1, 1, "2026-10-05 09:00"},
		{"daemon starting mid-window", "2026-10-02 10:15", nil, 1, 1, 1, "2026-10-05 09:00"},
		{"already handled", "2026-10-02 10:15", handledToday, 1, 0, 1, "2026-10-05 09:00"},
		{"handled yesterday is not today", "2026-10-02 09:30", map[Key]bool{{WindowID: 1, Date: "2026-10-01"}: true}, 1, 1, 1, "2026-10-05 09:00"},
		{"window closed (end excluded)", "2026-10-02 11:00", nil, 0, 0, 1, "2026-10-05 09:00"},
		{"before the first", "2026-10-02 08:59", nil, 0, 0, 1, "2026-10-02 09:00"},
		{"disabled windows never open", "2026-10-02 12:30", nil, 0, 0, 1, "2026-10-05 09:00"},
		{"across the weekend", "2026-10-03 15:00", nil, 0, 0, 1, "2026-10-05 09:00"},
		{"the evening one comes first on Thursday", "2026-10-01 11:30", nil, 0, 0, 2, "2026-10-01 19:00"},
		{"Sunday night to Monday", "2026-10-04 23:59", nil, 0, 0, 1, "2026-10-05 09:00"},
	}
	for _, tc := range cases {
		now := at(tc.now)
		cur, ok := Current(windows, now)
		if got := idOf(cur, ok); got != tc.current {
			t.Errorf("%s: current %d, want %d", tc.name, got, tc.current)
		}
		due, ok := Due(windows, tc.handled, now)
		if got := idOf(due, ok); got != tc.due {
			t.Errorf("%s: due %d, want %d", tc.name, got, tc.due)
		}
		next, ok := Next(windows, now)
		if got := idOf(next, ok); got != tc.next || !next.Start.Equal(at(tc.nextAt)) {
			t.Errorf("%s: next %d at %s, want %d at %s", tc.name, got, next.Start, tc.next, tc.nextAt)
		}
	}
	if d, _ := Due(windows, nil, at("2026-10-02 10:00")); d.Key() != (Key{WindowID: 1, Date: "2026-10-02"}) || !d.End.Equal(at("2026-10-02 11:00")) {
		t.Errorf("the opening carries its key and end: %+v", d)
	}
	if _, ok := Next([]Window{off}, at("2026-10-02 10:00")); ok {
		t.Error("no enabled window, no next opening")
	}
}

func TestOpeningsFollowTheWallClockAcrossDaylightSavingTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	w := Window{ID: 1, Days: []time.Weekday{time.Sunday}, Start: 9 * 60, End: 11 * 60, Enabled: true}
	// 2026-11-01 is the Sunday clocks go back in New York.
	next, ok := Next([]Window{w}, time.Date(2026, 10, 31, 12, 0, 0, 0, ny))
	if !ok || next.Start.Hour() != 9 || next.End.Hour() != 11 || next.End.Sub(next.Start) != 2*time.Hour {
		t.Errorf("%v-%v", next.Start, next.End)
	}
}

func idOf(o Opening, ok bool) int64 {
	if !ok {
		return 0
	}
	return o.Window.ID
}

func TestWindowString(t *testing.T) {
	w := Window{Days: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday}, Start: 540, End: 660, Market: "brasil", DryRun: true}
	if got := w.String(); got != "Mon-Wed 09:00-11:00, market brasil, dry run" {
		t.Errorf("%q", got)
	}
	if !slices.Equal(mustDays(t, "sun,mon"), []time.Weekday{time.Sunday, time.Monday}) {
		t.Error("days are sorted")
	}
}
