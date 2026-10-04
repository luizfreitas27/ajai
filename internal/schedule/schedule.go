// Package schedule is the pure part of schedule windows: parsing and validating
// them, and deciding, for a given moment, which window is open, whether its
// opening is still due, and when the next one starts. The daemon owns the clock
// and the runs; the store keeps the windows and the openings already handled.
//
// Windows are local wall-clock time on chosen weekdays. A window never crosses
// midnight (split it in two; "24:00" ends one at midnight), so each opening
// belongs to exactly one date.
package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Clock is a time of day in minutes since midnight: 0 is 00:00, 1440 is 24:00
// (allowed only as an end).
type Clock int

// EndOfDay is "24:00", the end of a window that runs until midnight.
const EndOfDay Clock = 24 * 60

var clockRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// ParseClock reads "HH:MM" (24-hour; "9:30" too). "24:00" is EndOfDay.
func ParseClock(s string) (Clock, error) {
	m := clockRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("%q is not a time of day (HH:MM, 24-hour)", s)
	}
	hh, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	if mm > 59 || hh > 24 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("%q is not a time of day (HH:MM, 24-hour)", s)
	}
	return Clock(hh*60 + mm), nil
}

// String is "HH:MM"; EndOfDay is "24:00".
func (c Clock) String() string { return fmt.Sprintf("%02d:%02d", int(c)/60, int(c)%60) }

// on is the moment this clock time falls on date's day, in date's location.
func (c Clock) on(date time.Time) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, 0, int(c), 0, 0, date.Location())
}

// Window is one schedule window.
type Window struct {
	ID      int64
	Days    []time.Weekday // sorted, no repeats
	Start   Clock
	End     Clock  // after Start, at most EndOfDay
	Market  string // "" runs the configured active market
	DryRun  bool
	Enabled bool
}

// dayNames are the stored and typed names, indexed by time.Weekday.
var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

var errNoDays = errors.New("a window needs at least one weekday")

func parseDay(s string) (time.Weekday, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, n := range dayNames {
		if s == n || (len(s) > 3 && strings.HasPrefix(time.Weekday(i).String(), strings.ToUpper(s[:1])+s[1:])) {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

// ParseDays reads weekdays: "mon,wed,fri", ranges such as "mon-fri" (or
// "fri-mon", across the weekend), "weekdays", "weekend" or "daily".
func ParseDays(s string) ([]time.Weekday, error) {
	var days []time.Weekday
	for _, part := range strings.Split(strings.ToLower(s), ",") {
		part = strings.TrimSpace(part)
		switch part {
		case "":
			continue
		case "weekdays":
			days = append(days, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday)
			continue
		case "weekend":
			days = append(days, time.Saturday, time.Sunday)
			continue
		case "daily", "everyday", "every day":
			for d := time.Sunday; d <= time.Saturday; d++ {
				days = append(days, d)
			}
			continue
		}
		from, to, isRange := strings.Cut(part, "-")
		first, ok1 := parseDay(from)
		last, ok2 := first, true
		if isRange {
			last, ok2 = parseDay(to)
		}
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("unknown weekday %q: use mon, tue, wed, thu, fri, sat, sun, a range such as mon-fri, weekdays, weekend or daily", part)
		}
		for d := first; ; d = (d + 1) % 7 {
			days = append(days, d)
			if d == last {
				break
			}
		}
	}
	if len(days) == 0 {
		return nil, errNoDays
	}
	slices.Sort(days)
	return slices.Compact(days), nil
}

// EncodeDays writes days as ParseDays reads them, for storage: "mon,wed,fri".
func EncodeDays(days []time.Weekday) string {
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = dayNames[d]
	}
	return strings.Join(names, ",")
}

// Week is the weekdays Monday first, the order windows are written in.
var Week = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// DaysString writes days back compactly: "Mon-Fri", "Mon, Wed, Fri", "daily".
func DaysString(days []time.Weekday) string {
	if len(days) == 7 {
		return "daily"
	}
	// Runs of consecutive days, Monday first so Mon-Fri reads naturally.
	order := Week
	var parts []string
	for i := 0; i < len(order); {
		if !slices.Contains(days, order[i]) {
			i++
			continue
		}
		j := i
		for j+1 < len(order) && slices.Contains(days, order[j+1]) {
			j++
		}
		name := func(d time.Weekday) string { return d.String()[:3] }
		switch {
		case j-i >= 2:
			parts = append(parts, name(order[i])+"-"+name(order[j]))
		case j == i+1:
			parts = append(parts, name(order[i]), name(order[j]))
		default:
			parts = append(parts, name(order[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// String describes the window: "Mon-Fri 09:00-11:00, market brasil, dry run".
func (w Window) String() string {
	s := DaysString(w.Days) + " " + w.Hours()
	if w.Market != "" {
		s += ", market " + w.Market
	}
	if w.DryRun {
		s += ", dry run"
	}
	return s
}

// Hours is the window's start and end, "14:00-16:00".
func (w Window) Hours() string { return w.Start.String() + "-" + w.End.String() }

// Validate checks a window on its own and against the other enabled windows (a
// window overlapping another on the same weekday is refused). markets are the
// valid market names.
func Validate(w Window, others []Window, markets []string) error {
	if len(w.Days) == 0 {
		return errNoDays
	}
	switch {
	case w.Start < 0 || w.Start >= EndOfDay:
		return fmt.Errorf("start %s is not a time of day", w.Start)
	case w.End == 0 && w.Start > 0:
		return fmt.Errorf("%s-00:00: write 24:00 for a window that ends at midnight", w.Start)
	case w.End < w.Start:
		return fmt.Errorf("%s-%s ends before it starts; a window cannot cross midnight, so split one that does in two: %s-24:00, and 00:00-%s on the next day",
			w.Start, w.End, w.Start, w.End)
	case w.End == w.Start:
		return fmt.Errorf("end %s is not after start %s", w.End, w.Start)
	case w.End > EndOfDay:
		return fmt.Errorf("end %s is not a time of day", w.End)
	}
	if w.Market != "" && !slices.Contains(markets, w.Market) {
		return fmt.Errorf("unknown market %q (use %s)", w.Market, strings.Join(markets, " or "))
	}
	for _, o := range others {
		if o.ID == w.ID || !o.Enabled {
			continue
		}
		if w.Start < o.End && o.Start < w.End {
			for _, d := range w.Days {
				if slices.Contains(o.Days, d) {
					return fmt.Errorf("overlaps window %d (%s) on %s", o.ID, o, d)
				}
			}
		}
	}
	return nil
}

// Key identifies one opening: a window on a local date ("2006-01-02").
type Key struct {
	WindowID int64
	Date     string
}

// Opening is one occurrence of a window: the window on one local date. Start
// and End are the instants its clock times fall on that day, for display and
// ordering; whether it is open is decided on the wall clock (Current).
type Opening struct {
	Window     Window
	Date       string // "2006-01-02"
	Start, End time.Time
}

// Key of this opening.
func (o Opening) Key() Key { return Key{WindowID: o.Window.ID, Date: o.Date} }

func openingOn(w Window, date time.Time) Opening {
	return Opening{Window: w, Date: date.Format(time.DateOnly), Start: w.Start.on(date), End: w.End.on(date)}
}

// Current returns the opening of an enabled window that contains now, if any.
// Windows are wall-clock time, as the Python bot had them: a window is open
// while the local clock reads from its start (included) to its end (excluded),
// so daylight-saving days hold no surprises beyond the clock's own (an hour
// that never happens, or happens twice). A disabled or deleted window has no
// current opening.
func Current(windows []Window, now time.Time) (Opening, bool) {
	minute := Clock(now.Hour()*60 + now.Minute())
	for _, w := range windows {
		if w.Enabled && slices.Contains(w.Days, now.Weekday()) && minute >= w.Start && minute < w.End {
			return openingOn(w, now), true
		}
	}
	return Opening{}, false
}

// Due returns the opening that should get its run now: the current one, unless
// it was already handled (its run started, or it was skipped). A daemon that
// starts in the middle of a window it has not handled finds it due.
func Due(windows []Window, handled map[Key]bool, now time.Time) (Opening, bool) {
	o, ok := Current(windows, now)
	if !ok || handled[o.Key()] {
		return Opening{}, false
	}
	return o, true
}

// Next returns the first opening of an enabled window that starts after now.
func Next(windows []Window, now time.Time) (Opening, bool) {
	var best Opening
	found := false
	for i := 0; i <= 7; i++ {
		date := now.AddDate(0, 0, i)
		for _, w := range windows {
			if !w.Enabled || !slices.Contains(w.Days, date.Weekday()) {
				continue
			}
			o := openingOn(w, date)
			if o.Start.After(now) && (!found || o.Start.Before(best.Start)) {
				best, found = o, true
			}
		}
		if found {
			return best, true
		}
	}
	return Opening{}, false
}

// Upcoming returns the next n openings of enabled windows that start after
// now, in order.
func Upcoming(windows []Window, now time.Time, n int) []Opening {
	var out []Opening
	for len(out) < n {
		o, ok := Next(windows, now)
		if !ok {
			break
		}
		out, now = append(out, o), o.Start
	}
	return out
}

// Outcome is what the scheduler did with an opening.
type Outcome string

// The outcomes of an opening.
const (
	OutcomeStarted Outcome = "started" // its run started
	OutcomeSkipped Outcome = "skipped" // another run was in progress
	OutcomeFailed  Outcome = "failed"  // its run could not start
)
