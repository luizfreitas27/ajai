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
	for _, s := range []string{"", "9", "25:00", "24:01", "12:60", "12:5", "ab:cd", "-1:00", "-0:30", "+9:30", "009:30", "9:30:00"} {
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
		{"crosses midnight", w("fri", "22:00", "02:00", ""), "22:00-02:00 ends before it starts; a window cannot cross midnight, so split one that does in two: 22:00-24:00, and 00:00-02:00"},
		{"a typo reads the same", w("fri", "10:00", "09:00", ""), "10:00-09:00 ends before it starts"},
		{"midnight as 00:00", w("fri", "22:00", "00:00", ""), "22:00-00:00: write 24:00 for a window that ends at midnight"},
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

func TestWindowsFollowTheWallClockAcrossDaylightSavingTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	sunday := []time.Weekday{time.Sunday}
	at := func(y int, m time.Month, d, h, min int, offset int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, time.FixedZone("", offset*3600)).In(ny)
	}
	// 2026-03-08: clocks jump from 02:00 EST to 03:00 EDT. A 02:30-03:30 window is
	// open from 03:00 (the first minute its clock times are on the wall) to 03:30.
	spring := []Window{{ID: 1, Days: sunday, Start: 150, End: 210, Enabled: true}}
	for _, tc := range []struct {
		now  time.Time
		open bool
	}{
		{at(2026, 3, 8, 1, 59, -5), false}, // 01:59 EST
		{at(2026, 3, 8, 3, 0, -4), true},   // 03:00 EDT, a minute later
		{at(2026, 3, 8, 3, 29, -4), true},
		{at(2026, 3, 8, 3, 30, -4), false},
	} {
		if _, ok := Current(spring, tc.now); ok != tc.open {
			t.Errorf("spring %s: open %v", tc.now, ok)
		}
	}
	// 2026-11-01: 01:00-02:00 happens twice; the window is open both times.
	fall := []Window{{ID: 1, Days: sunday, Start: 60, End: 120, Enabled: true}}
	for _, now := range []time.Time{at(2026, 11, 1, 1, 30, -4), at(2026, 11, 1, 1, 30, -5)} {
		o, ok := Current(fall, now)
		if !ok || o.Date != "2026-11-01" {
			t.Errorf("fall %s: %v %+v", now, ok, o)
		}
	}
	// An ordinary morning window is the same two hours either side of the change.
	morning := []Window{{ID: 1, Days: sunday, Start: 9 * 60, End: 11 * 60, Enabled: true}}
	next, ok := Next(morning, time.Date(2026, 10, 31, 12, 0, 0, 0, ny))
	if !ok || next.Date != "2026-11-01" || next.Start.Hour() != 9 || next.End.Sub(next.Start) != 2*time.Hour {
		t.Errorf("%+v", next)
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

func TestUpcomingListsTheNextOpeningsInOrder(t *testing.T) {
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	w := Window{ID: 1, Days: []time.Weekday{time.Tuesday, time.Thursday}, Start: 14 * 60, End: 16 * 60, Enabled: true}
	got := Upcoming([]Window{w}, at("2026-10-06 15:00"), 3) // a Tuesday, inside the window: the next one is Thursday's
	var starts []string
	for _, o := range got {
		starts = append(starts, o.Start.Format("Mon 02 15:04"))
	}
	if want := "Thu 08 14:00, Tue 13 14:00, Thu 15 14:00"; strings.Join(starts, ", ") != want {
		t.Errorf("%v, want %s", starts, want)
	}
	w.Enabled = false
	if got := Upcoming([]Window{w}, at("2026-10-06 15:00"), 3); len(got) != 0 {
		t.Errorf("a window turned off has no openings: %v", got)
	}
}
