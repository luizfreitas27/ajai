package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/schedule"
	"github.com/luizfreitas27/aja/internal/store"
)

// scheduleTab is the Schedule tab: the schedule windows with their next
// opening (or the opening under way); a window is added in a form (checked
// like `aja schedule add`), turned on or off, or deleted.
type scheduleTab struct {
	loaded   bool
	windows  []schedule.Window
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
}

type windowsLoadedMsg struct {
	windows []schedule.Window
	problem string
}

type windowChangedMsg struct {
	notice  notice
	refusal string // the window was refused
	form    int    // the form it came from, 0 for the list's own actions
}

func loadWindows(d Deps) tea.Cmd {
	return func() tea.Msg {
		windows, err := d.Store.ListWindows()
		msg := windowsLoadedMsg{windows: windows}
		if err != nil {
			msg.problem = "Could not read the schedule windows: " + err.Error()
		}
		return msg
	}
}

func (t scheduleTab) editing() bool { return t.form != nil || t.deleting != nil || t.daemon.editing() }

func (t scheduleTab) current() (schedule.Window, bool) {
	if !t.loaded || t.cursor >= len(t.windows) {
		return schedule.Window{}, false
	}
	return t.windows[t.cursor], true
}

func (t scheduleTab) resized(width, height int) scheduleTab {
	t.width, t.height = width, height
	return t.settled()
}

// listHeight is the room for windows: the tab's less its title, the notice,
// the column names and the delete question.
func (t scheduleTab) listHeight() int {
	used := 2 + len(wrap(t.notice.text, max(1, t.width))) + len(t.daemon.lines(measure, t.width, daemonProc{}))
	if t.deleting != nil {
		used += 3
	}
	return max(1, t.height-used)
}

// settled keeps the selection in the list and in view, and remembers whose
// it is.
func (t scheduleTab) settled() scheduleTab {
	t.cursor = max(0, min(t.cursor, len(t.windows)-1))
	n := t.listHeight()
	if t.cursor < t.top {
		t.top = t.cursor
	}
	if t.cursor >= t.top+n {
		t.top = t.cursor - n + 1
	}
	t.selected = 0
	if w, ok := t.current(); ok {
		t.selected = w.ID
	}
	return t
}

func (t scheduleTab) update(msg tea.Msg, d Deps) (scheduleTab, tea.Cmd) {
	switch msg := msg.(type) {
	case windowsLoadedMsg:
		t.loaded, t.windows, t.problem = true, msg.windows, msg.problem
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
	switch msg.String() {
	case "up", "k":
		t.cursor--
	case "down", "j":
		t.cursor++
	case "a":
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
		if a, ok := daemonKeys[msg.String()]; ok {
			t.daemon = t.daemon.ask(a)
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

// windowRow lays out the list: it fits the smallest terminal.
const windowRow = "  %-3s %-16s %-11s %-13s %-7s %-5s %s"

// readUnit reads the daemon's unit, unless that is under way already.
func (t scheduleTab) readUnit(d Deps) (scheduleTab, tea.Cmd) {
	var cmd tea.Cmd
	t.daemon, cmd = t.daemon.readUnit(d)
	return t, cmd
}

func (t scheduleTab) view(s styles, width int, now time.Time, proc daemonProc) string {
	head := s.section.Render("Schedule")
	if t.loaded {
		if o, ok := schedule.Current(t.windows, now); ok {
			head += "  " + s.ok.Render(fmt.Sprintf("window %d open now, until %s", o.Window.ID, o.Window.End))
		} else if o, ok := schedule.Next(t.windows, now); ok {
			head += "  " + s.dim.Render(fmt.Sprintf("next opening %s, window %d", o.Start.Format(dayTime), o.Window.ID))
		}
	}
	lines := []string{head}
	switch {
	case t.problem != "":
		lines = append(lines, s.fail.Render(t.problem))
	case t.notice.text != "":
		lines = append(lines, wrapStyled(s.byLevel(t.notice.level), t.notice.text, width)...)
	}
	if t.form != nil {
		return strings.Join(append(append(lines, ""), t.form.lines(s, width)...), "\n")
	}
	if !t.loaded {
		return strings.Join(append(lines, "Reading…"), "\n")
	}
	bottom := t.daemon.lines(s, width, proc)
	if len(t.windows) == 0 {
		lines = append(lines, "", s.dim.Render("No schedule windows: a adds one. Runs start in them only while the daemon runs."))
		return strings.Join(append(pad(lines, t.height-len(bottom)), bottom...), "\n")
	}
	lines = append(lines, s.dim.Render(fmt.Sprintf(windowRow, "#", "days", "hours", "market", "mode", "state", "next opening")))
	n := t.listHeight()
	for i := t.top; i < len(t.windows) && i < t.top+n; i++ {
		w := t.windows[i]
		mark := "  "
		if i == t.cursor {
			mark = s.key.Render("> ")
		}
		market := w.Market
		if market == "" {
			market = "the config's"
		}
		mode := "live"
		if w.DryRun {
			mode = "dry run"
		}
		state, next := "on", ""
		switch o, ok := schedule.Current([]schedule.Window{w}, now); {
		case !w.Enabled:
			state = "off"
		case ok:
			next = "open until " + o.Window.End.String()
		default:
			if o, ok := schedule.Next([]schedule.Window{w}, now); ok {
				next = o.Start.Format(dayTime)
			}
		}
		lines = append(lines, mark+strings.TrimPrefix(fmt.Sprintf(windowRow, fmt.Sprint(w.ID), clip(schedule.DaysString(w.Days), 16),
			w.Start.String()+"-"+w.End.String(), market, mode, state, next), "  "))
	}
	if t.deleting != nil {
		lines = append(lines, "", s.warn.Render(fmt.Sprintf("Delete window %d (%s)?", t.deleting.ID, t.deleting)),
			s.key.Render("y")+" deletes it · "+s.key.Render("n")+" keeps it")
	}
	return strings.Join(append(pad(lines, t.height-len(bottom)), bottom...), "\n")
}

// pad adds empty lines to reach n, so what follows sits at the bottom.
func pad(lines []string, n int) []string {
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
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
	mode := "live"
	if f.dry {
		mode = "dry run"
	}
	out := []string{
		s.section.Render("Add a schedule window") + "  " + s.dim.Render("local time; 24:00 ends one at midnight"),
		field(fieldWindowDays, "Days", strings.Join(days, " ")),
		field(fieldWindowStart, "Start", f.start.View()),
		field(fieldWindowEnd, "End", f.end.View()),
		field(fieldWindowMarket, "Market", "‹ "+marketLabel(f.market)+" ›"),
		field(fieldWindowMode, "Mode", "‹ "+mode+" ›"),
	}
	if f.saving {
		out = append(out, s.dim.Render("Adding…"))
	}
	if f.refusal != "" {
		out = append(out, wrapStyled(s.fail, "Refused: "+f.refusal, width)...)
	}
	return out
}

// wrapStyled wraps text to width and styles each line.
func wrapStyled(st interface{ Render(...string) string }, text string, width int) []string {
	var out []string
	for _, l := range wrap(text, width) {
		out = append(out, st.Render(l))
	}
	return out
}
