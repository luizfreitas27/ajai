package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

// scheduleTab is the Schedule tab: the schedule windows with their next
// opening (or the opening under way); a window is added in a form (checked
// like `ajai schedule add`), turned on or off, or deleted.
type scheduleTab struct {
	loaded   bool
	windows  []schedule.Window
	history  map[int64]windowHistory // each window's latest opening, by id
	now      time.Time               // when the windows were read
	problem  string
	cursor   int
	selected int64 // the selected window: the selection follows it when the list is read again
	top      int
	height   int
	width    int
	form     *windowForm
	forms    int // forms opened so far: a result belongs to the form it came from
	deleting *schedule.Window
	notice   notice
	daemon   daemonSection
}

// The add form's fields.
const (
	fieldWindowDays = iota
	fieldWindowStart
	fieldWindowEnd
	fieldWindowMarket
	fieldWindowMode
	windowFields
)

// windowForm is the add form.
type windowForm struct {
	id         int // which form this is (scheduleTab.forms)
	field      int
	day        int // the weekday under the cursor, an index into schedule.Week
	days       [7]bool
	start, end textinput.Model
	market     int // index into marketChoices
	dry        bool
	saving     bool
	refusal    string
	askingLive bool // Enter on a live window asks first: its runs submit applications for real
}

type windowsLoadedMsg struct {
	windows []schedule.Window
	history map[int64]windowHistory
	now     time.Time
	problem string
}

// windowHistory is what a window last did: its latest handled opening and,
// when that started a run, the run.
type windowHistory struct {
	opening *store.OpeningRecord
	run     *store.Run
}

// nextOpeningsShown is how many of a window's next openings its details list.
const nextOpeningsShown = 3

type windowChangedMsg struct {
	notice  notice
	refusal string // the window was refused
	form    int    // the form it came from, 0 for the list's own actions
}

func loadWindows(d Deps) tea.Cmd {
	return func() tea.Msg {
		windows, err := d.Store.ListWindows()
		msg := windowsLoadedMsg{windows: windows, history: map[int64]windowHistory{}, now: d.Now()}
		if err != nil {
			msg.problem = "Could not read the schedule windows: " + err.Error()
		}
		for _, w := range windows {
			var h windowHistory
			if h.opening, err = d.Store.LastOpening(w.ID); err == nil && h.opening != nil && h.opening.RunID > 0 {
				h.run, err = d.Store.GetRun(h.opening.RunID)
			}
			if err != nil && msg.problem == "" {
				msg.problem = "Could not read the windows' openings: " + err.Error()
			}
			msg.history[w.ID] = h
		}
		return msg
	}
}

func (t scheduleTab) editing() bool { return t.form != nil || t.deleting != nil || t.daemon.editing() }

func (t scheduleTab) current() (schedule.Window, bool) {
	if !t.loaded || t.cursor < 0 || t.cursor >= len(t.windows) { // a move past the top is settled after the details are measured
		return schedule.Window{}, false
	}
	return t.windows[t.cursor], true
}

func (t scheduleTab) resized(width, height int) scheduleTab {
	t.width, t.height = width, height
	return t.settled()
}

// scheduleDetailLines is the least room kept under the list (on a terminal
// too narrow for the details beside it) for the selected window.
const scheduleDetailLines = 3

// settled keeps the selection in the list and in view, and remembers whose
// it is.
func (t scheduleTab) settled() scheduleTab {
	t.cursor, t.top = t.frame(measure, t.now, daemonProc{}).keep(t.cursor, t.top, t.width, t.height)
	t.selected = 0
	if w, ok := t.current(); ok {
		t.selected = w.ID
	}
	return t
}

func (t scheduleTab) update(msg tea.Msg, d Deps) (scheduleTab, tea.Cmd) {
	switch msg := msg.(type) {
	case windowsLoadedMsg:
		t.loaded, t.windows, t.history, t.now, t.problem = true, msg.windows, msg.history, msg.now, msg.problem
		for i, w := range t.windows {
			if w.ID == t.selected {
				t.cursor = i
			}
		}
		return t.settled(), nil
	case unitReadMsg, daemonDoneMsg:
		var cmd tea.Cmd
		t.daemon, cmd = t.daemon.update(msg, d)
		return t.settled(), cmd
	case windowChangedMsg:
		mine := t.form != nil && msg.form == t.form.id
		switch {
		case mine && msg.refusal != "":
			f := *t.form
			f.saving, f.refusal = false, msg.refusal
			t.form = &f
			return t, nil
		case msg.refusal != "": // its form is closed: say it anyway
			t.notice = notice{"Not added: " + msg.refusal, levelWarn}
		default:
			if mine {
				t.form = nil
			}
			t.notice = msg.notice
		}
		return t.settled(), loadWindows(d)
	case tea.KeyMsg:
		switch {
		case t.daemon.editing():
			var cmd tea.Cmd
			t.daemon, cmd = t.daemon.update(msg, d)
			return t.settled(), cmd
		case t.deleting != nil:
			return t.deleteKey(msg, d)
		case t.form != nil:
			return t.formKey(msg, d)
		}
		return t.browseKey(msg, d)
	}
	return t, nil
}

func (t scheduleTab) browseKey(msg tea.KeyMsg, d Deps) (scheduleTab, tea.Cmd) {
	if cursor, ok := moved(msg.String(), t.cursor, len(t.windows)); ok {
		t.cursor = cursor
		return t.settled(), nil
	}
	switch msg.String() {
	case "n":
		t.forms++
		t.form, t.notice = newWindowForm(t.forms), notice{}
	case " ":
		if w, ok := t.current(); ok {
			return t, toggleWindow(d, w)
		}
	case "d":
		if w, ok := t.current(); ok {
			t.deleting = &w
		}
	default:
		for _, k := range t.daemon.keys() {
			if k.key == msg.String() {
				t.daemon = t.daemon.ask(k.action)
			}
		}
	}
	return t.settled(), nil
}

func (t scheduleTab) deleteKey(msg tea.KeyMsg, d Deps) (scheduleTab, tea.Cmd) {
	switch msg.String() {
	case "y":
		w := *t.deleting
		t.deleting = nil
		return t.settled(), deleteWindow(d, w)
	case "n", "esc":
		t.deleting = nil
	}
	return t.settled(), nil
}

func newWindowForm(id int) *windowForm {
	clock := func(value string) textinput.Model {
		in := textinput.New()
		in.Prompt = ""
		in.SetValue(value) // a value, not a placeholder: the form is a whole window as it opens
		in.CharLimit = 5
		in.Width = 6
		in.Cursor.SetMode(cursor.CursorStatic)
		return in
	}
	f := &windowForm{id: id, start: clock("09:00"), end: clock("11:00")}
	for i := range 5 { // Monday to Friday until changed
		f.days[i] = true
	}
	return f
}

func (t scheduleTab) formKey(msg tea.KeyMsg, d Deps) (scheduleTab, tea.Cmd) {
	f := *t.form
	if f.saving && msg.String() != "esc" {
		return t, nil // one add at a time
	}
	var cmd tea.Cmd
	switch key := msg.String(); {
	case f.askingLive: // before Esc: here it goes back to the form
		switch key {
		case "y":
			w, _ := f.window() // checked when the question was asked
			f.saving, f.askingLive = true, false
			t.form = &f
			return t, addWindow(d, w, f.id)
		case "n", "esc":
			f.askingLive = false // back to the form, every choice kept
		}
		t.form = &f
		return t, nil
	case key == "esc":
		t.form = nil
		return t.settled(), nil
	case key == "enter":
		w, err := f.window()
		if err != nil {
			f.refusal = err.Error()
			t.form = &f
			return t, nil
		}
		if !f.dry { // a live window's runs submit applications: one more, deliberate step
			f.askingLive, f.refusal = true, ""
			t.form = &f
			return t, nil
		}
		f.saving, f.refusal = true, ""
		t.form = &f
		return t, addWindow(d, w, f.id)
	case key == "up" || key == "shift+tab":
		f.field = (f.field + windowFields - 1) % windowFields
	case key == "down" || key == "tab":
		f.field = (f.field + 1) % windowFields
	case f.field == fieldWindowStart:
		f.start, cmd = f.start.Update(msg)
	case f.field == fieldWindowEnd:
		f.end, cmd = f.end.Update(msg)
	case key == "left" || key == "h":
		f = f.change(-1)
	case key == "right" || key == "l":
		f = f.change(1)
	case key == " " && f.field == fieldWindowDays:
		f.days[f.day] = !f.days[f.day]
	case key == " ":
		f = f.change(1)
	}
	f.start.Blur()
	f.end.Blur()
	switch f.field {
	case fieldWindowStart:
		f.start.Focus()
	case fieldWindowEnd:
		f.end.Focus()
	}
	t.form = &f
	return t, cmd
}

// change moves the field's choice by step: the weekday under the cursor, the
// market, the mode.
func (f windowForm) change(step int) windowForm {
	switch f.field {
	case fieldWindowDays:
		f.day = (f.day + step + len(schedule.Week)) % len(schedule.Week)
	case fieldWindowMarket:
		f.market = (f.market + step + len(marketChoices)) % len(marketChoices)
	case fieldWindowMode:
		f.dry = !f.dry
	}
	return f
}

// window is the form's window, its own fields checked; Validate checks it
// against the others when it is added.
func (f windowForm) window() (schedule.Window, error) {
	w := schedule.Window{Market: marketChoices[f.market], DryRun: f.dry, Enabled: true}
	for i, on := range f.days {
		if on {
			w.Days = append(w.Days, schedule.Week[i])
		}
	}
	if len(w.Days) == 0 {
		return w, errors.New("pick at least one weekday (Space)")
	}
	var err error
	if w.Start, err = schedule.ParseClock(f.start.Value()); err != nil {
		return w, fmt.Errorf("start: %w", err)
	}
	if w.End, err = schedule.ParseClock(f.end.Value()); err != nil {
		return w, fmt.Errorf("end: %w", err)
	}
	return w, nil
}

func addWindow(d Deps, w schedule.Window, form int) tea.Cmd {
	return func() tea.Msg {
		id, err := d.Store.AddValidWindow(w, config.MarketNameList())
		if err != nil {
			return windowChangedMsg{form: form, refusal: err.Error()}
		}
		d.logger().Info("schedule window added from the TUI", "id", id, "window", w.String())
		return windowChangedMsg{form: form, notice: notice{text: fmt.Sprintf("Added window %d: %s.", id, w)}}
	}
}

// toggleWindow turns w off, or on if it is off (checked against the others).
func toggleWindow(d Deps, w schedule.Window) tea.Cmd {
	return func() tea.Msg {
		updated, err := d.Store.SetWindowEnabledValid(w.ID, !w.Enabled, config.MarketNameList())
		switch {
		case errors.Is(err, store.ErrNoWindow):
			return windowChangedMsg{notice: notice{fmt.Sprintf("Window %d is gone.", w.ID), levelWarn}}
		case err != nil && w.Enabled:
			return windowChangedMsg{notice: notice{fmt.Sprintf("Could not turn window %d off: %v.", w.ID, err), levelFail}}
		case err != nil:
			return windowChangedMsg{notice: notice{fmt.Sprintf("Window %d stays off: %v.", w.ID, err), levelWarn}}
		}
		verb, state := "disabled", "off"
		if updated.Enabled {
			verb, state = "enabled", "on"
		}
		d.logger().Info("schedule window "+verb+" from the TUI", "id", w.ID, "window", updated.String())
		return windowChangedMsg{notice: notice{text: fmt.Sprintf("Window %d is %s.", w.ID, state)}}
	}
}

func deleteWindow(d Deps, w schedule.Window) tea.Cmd {
	return func() tea.Msg {
		removed, err := d.Store.DeleteWindow(w.ID)
		switch {
		case err != nil:
			return windowChangedMsg{notice: notice{"Could not delete the window: " + err.Error(), levelFail}}
		case !removed:
			return windowChangedMsg{notice: notice{fmt.Sprintf("Window %d was gone already.", w.ID), levelWarn}}
		}
		d.logger().Info("schedule window deleted from the TUI", "id", w.ID, "window", w.String())
		return windowChangedMsg{notice: notice{text: fmt.Sprintf("Deleted window %d: %s.", w.ID, w)}}
	}
}

// windowRow is a row of the list width wide: the window's number, its days
// (what the width leaves), hours, market, mode, state and next opening.
func windowRow(width int, cells ...string) string {
	const id, hours, market, mode, state, next = 3, 11, 13, 7, 5, 16
	days := max(4, width-id-hours-market-mode-state-next-6*2)
	return columns([]int{id, days, hours, market, mode, state, next}, cells...)
}

// frame is the tab for the frame to lay out: the windows, the daemon's strip
// under the title, the selected window in full.
func (t scheduleTab) frame(s styles, now time.Time, proc daemonProc) listFrame {
	summary := ""
	if t.loaded {
		if o, ok := schedule.Current(t.windows, now); ok {
			summary = fmt.Sprintf("window %d open now, until %s", o.Window.ID, o.Window.End)
		} else if o, ok := schedule.Next(t.windows, now); ok {
			summary = fmt.Sprintf("next opening %s, window %d", o.Start.Format(dayTime), o.Window.ID)
		}
	}
	return listFrame{title: "Schedule", summary: summary,
		notes:   append(t.daemon.strip(s, t.width, proc), listNotes(s, t.problem, t.notice, t.width)...),
		waiting: waitText(t.loaded, "Reading…"), empty: "No schedule windows: n adds one. Runs start in them only while the daemon runs.",
		rows: len(t.windows), minDetails: scheduleDetailLines,
		heading: func(width int) string {
			return windowRow(width, "#", "days", "hours", "market", "mode", "state", "next opening")
		},
		row: func(i, width int) string {
			w := t.windows[i]
			next := ""
			if o, ok := schedule.Current([]schedule.Window{w}, now); ok {
				next = "open until " + o.Window.End.String()
			} else if o, ok := schedule.Next([]schedule.Window{w}, now); ok {
				next = o.Start.Format(dayTime)
			}
			return windowRow(width, fmt.Sprint(w.ID), schedule.DaysString(w.Days), w.Hours(), marketName(w.Market), run.Mode(w.DryRun), windowState(w), next)
		},
		details: func(s styles, width int, beside bool) []string { return t.detail(s, now, beside) },
	}
}

// windowState is whether a window is on.
func windowState(w schedule.Window) string {
	if w.Enabled {
		return "on"
	}
	return "off"
}

// detail is the selected window in full: when it opens, its next openings,
// what its last opening did and, when it started a run, how that run went;
// or the question deleting it.
func (t scheduleTab) detail(s styles, now time.Time, beside bool) []string {
	if t.deleting != nil {
		return []string{s.warn.Render(fmt.Sprintf("Delete window %d (%s)?", t.deleting.ID, t.deleting)),
			s.key.Render("y") + " deletes it · " + s.key.Render("n") + " keeps it"}
	}
	w, ok := t.current()
	if !ok {
		return nil
	}
	var next []string
	for _, o := range schedule.Upcoming([]schedule.Window{w}, now, nextOpeningsShown) {
		next = append(next, o.Start.Format(dayTime))
	}
	if len(next) == 0 {
		next = []string{"none: the window is off"}
	}
	lastOpening := []string{"none handled yet"} // what it did, then the run it started
	if h := t.history[w.ID]; h.opening != nil {
		date := h.opening.Date
		if d, err := time.ParseInLocation(time.DateOnly, date, time.Local); err == nil {
			date = d.Format("Mon 02 Jan")
		}
		lastOpening = []string{date + ": " + openingOutcome(*h.opening)}
		if h.run != nil {
			lastOpening = append(lastOpening, runCounts(*h.run)+" · "+runEnding(*h.run))
		}
	}
	if !beside {
		lines := []string{fmt.Sprintf("Window %d · %s %s · %s · %s · %s", w.ID, schedule.DaysString(w.Days), w.Hours(), marketName(w.Market), run.Mode(w.DryRun), windowState(w)),
			labelled(s, "next", strings.Join(next, " · ")), labelled(s, "last opening", lastOpening[0])}
		for _, l := range lastOpening[1:] {
			lines = append(lines, "  "+l)
		}
		return lines
	}
	lines := []string{fmt.Sprintf("Window %d", w.ID), labelled(s, "days", schedule.DaysString(w.Days)), labelled(s, "hours", w.Hours()),
		labelled(s, "market", marketName(w.Market)), labelled(s, "mode", run.Mode(w.DryRun)), labelled(s, "state", windowState(w)), "", s.dim.Render("next openings")}
	for _, n := range next {
		lines = append(lines, "  "+n)
	}
	lines = append(lines, "", s.dim.Render("last opening"))
	for _, l := range lastOpening {
		lines = append(lines, "  "+l)
	}
	return lines
}

// openingOutcome says what the scheduler did with an opening.
func openingOutcome(r store.OpeningRecord) string {
	switch r.Outcome {
	case schedule.OutcomeStarted:
		return "started a run"
	case schedule.OutcomeSkipped:
		return strings.Join(nonEmpty("skipped", r.Detail), ": ")
	case schedule.OutcomeFailed:
		return strings.Join(nonEmpty("the run could not start", r.Detail), ": ")
	}
	return string(r.Outcome)
}

// readUnit reads the daemon's unit, unless that is under way already.
func (t scheduleTab) readUnit(d Deps) (scheduleTab, tea.Cmd) {
	var cmd tea.Cmd
	t.daemon, cmd = t.daemon.readUnit(d)
	return t, cmd
}

func (t scheduleTab) view(s styles, width int, now time.Time, proc daemonProc) string {
	f := t.frame(s, now, proc)
	if t.form != nil { // the add form takes the list's place
		lines := append(f.headerLines(s, width), f.notes...)
		return strings.Join(append(append(lines, ""), t.form.lines(s, width)...), "\n")
	}
	return f.view(s, width, t.height, t.cursor, t.top)
}

func (f windowForm) lines(s styles, width int) []string {
	field := func(i int, label, value string) string { return formLine(s, i == f.field, label, value) }
	var days []string
	for i, wd := range schedule.Week {
		box := "[ ]"
		if f.days[i] {
			box = "[x]"
		}
		day := box + " " + wd.String()[:3]
		if f.field == fieldWindowDays && i == f.day {
			day = s.key.Render(day)
		}
		days = append(days, day)
	}
	mode := s.fail.Render(choice("live")) // stands out from the default dry run
	if f.dry {
		mode = choice("dry run")
	}
	out := []string{
		s.section.Render("Add a schedule window") + "  " + s.dim.Render("local time; 24:00 ends one at midnight"),
		field(fieldWindowDays, "Days", strings.Join(days, " ")),
		field(fieldWindowStart, "Start", f.start.View()),
		field(fieldWindowEnd, "End", f.end.View()),
		field(fieldWindowMarket, "Market", choice(marketName(marketChoices[f.market]))),
		field(fieldWindowMode, "Mode", mode),
	}
	if f.askingLive {
		out = append(out, s.warn.Render("This window starts live runs: they submit applications for real. Add it?"),
			s.key.Render("y")+" adds it · "+s.key.Render("n")+" goes back")
	}
	if f.saving {
		out = append(out, s.dim.Render("Adding…"))
	}
	if f.refusal != "" {
		out = append(out, wrapStyled(s.fail, "Refused: "+f.refusal, width)...)
	}
	return out
}

// wrapStyled wraps text to width, a word longer than the width (a link, a
// path) broken where it must be, and styles each line.
func wrapStyled(st interface{ Render(...string) string }, text string, width int) []string {
	var out []string
	for _, l := range wrap(text, width) {
		for _, part := range strings.Split(ansi.Wrap(l, max(1, width), "/-"), "\n") {
			out = append(out, st.Render(part))
		}
	}
	return out
}
