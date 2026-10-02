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

// weekdays in the order the form shows them.
var weekdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// scheduleTab is the Schedule tab: the schedule windows with their next
// opening; a window is added in a form (checked like `aja schedule add`),
// turned on or off, or deleted.
type scheduleTab struct {
	loaded   bool
	windows  []schedule.Window
	problem  string
	cursor   int
	selected int64 // the selected window: the selection follows it when the list is read again
	form     *windowForm
	deleting *schedule.Window
	notice   notice
}

// The add form's fields.
const (
	fieldDays = iota
	fieldStart
	fieldEnd
	fieldWindowMarket
	fieldWindowMode
	windowFields
)

// windowForm is the add form.
type windowForm struct {
	field      int
	day        int // the weekday under the cursor, an index into weekdays
	days       [7]bool
	start, end textinput.Model
	market     int // index into marketChoices
	dry        bool
	refusal    string
}

type windowsLoadedMsg struct {
	windows []schedule.Window
	problem string
}

type windowChangedMsg struct {
	notice   notice
	refusal  string // the window was refused: keep the form
	fromForm bool
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

func (t scheduleTab) editing() bool { return t.form != nil || t.deleting != nil }

func (t scheduleTab) current() (schedule.Window, bool) {
	if !t.loaded || t.cursor >= len(t.windows) {
		return schedule.Window{}, false
	}
	return t.windows[t.cursor], true
}

// settled keeps the selection in the list and remembers whose it is.
func (t scheduleTab) settled() scheduleTab {
	t.cursor = max(0, min(t.cursor, len(t.windows)-1))
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
	case windowChangedMsg:
		if msg.fromForm && msg.refusal != "" && t.form != nil {
			f := *t.form
			f.refusal = msg.refusal
			t.form = &f
			return t, nil
		}
		if msg.fromForm {
			t.form = nil
		}
		t.notice = msg.notice
		return t, loadWindows(d)
	case tea.KeyMsg:
		switch {
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
		t.form, t.notice = newWindowForm(), notice{}
	case " ":
		if w, ok := t.current(); ok {
			return t, toggleWindow(d, w)
		}
	case "d":
		if w, ok := t.current(); ok {
			t.deleting = &w
		}
	}
	return t.settled(), nil
}

func (t scheduleTab) deleteKey(msg tea.KeyMsg, d Deps) (scheduleTab, tea.Cmd) {
	switch msg.String() {
	case "y":
		w := *t.deleting
		t.deleting = nil
		return t, deleteWindow(d, w)
	case "n", "esc":
		t.deleting = nil
	}
	return t, nil
}

func newWindowForm() *windowForm {
	clock := func(placeholder string) textinput.Model {
		in := textinput.New()
		in.Prompt = ""
		in.Placeholder = placeholder
		in.CharLimit = 5
		in.Width = 6
		in.Cursor.SetMode(cursor.CursorStatic)
		return in
	}
	f := &windowForm{start: clock("09:00"), end: clock("11:00")}
	for i := range 5 { // Monday to Friday until changed
		f.days[i] = true
	}
	return f
}

func (t scheduleTab) formKey(msg tea.KeyMsg, d Deps) (scheduleTab, tea.Cmd) {
	f := *t.form
	var cmd tea.Cmd
	switch key := msg.String(); {
	case key == "esc":
		t.form = nil
		return t, nil
	case key == "enter":
		w, err := f.window()
		if err != nil {
			f.refusal = err.Error()
			t.form = &f
			return t, nil
		}
		f.refusal = ""
		t.form = &f
		return t, addWindow(d, w)
	case key == "up" || key == "shift+tab":
		f.field = (f.field + windowFields - 1) % windowFields
	case key == "down" || key == "tab":
		f.field = (f.field + 1) % windowFields
	case f.field == fieldStart:
		f.start, cmd = f.start.Update(msg)
	case f.field == fieldEnd:
		f.end, cmd = f.end.Update(msg)
	case key == "left" || key == "h":
		f = f.change(-1)
	case key == "right" || key == "l":
		f = f.change(1)
	case key == " ":
		switch f.field {
		case fieldDays:
			f.days[f.day] = !f.days[f.day]
		case fieldWindowMode:
			f.dry = !f.dry
		case fieldWindowMarket:
			f = f.change(1)
		}
	}
	f.start.Blur()
	f.end.Blur()
	switch f.field {
	case fieldStart:
		f.start.Focus()
	case fieldEnd:
		f.end.Focus()
	}
	t.form = &f
	return t, cmd
}

// change moves the field's choice by step: the weekday under the cursor, the
// market, the mode.
func (f windowForm) change(step int) windowForm {
	switch f.field {
	case fieldDays:
		f.day = (f.day + step + len(weekdays)) % len(weekdays)
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
			w.Days = append(w.Days, weekdays[i])
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

func addWindow(d Deps, w schedule.Window) tea.Cmd {
	return func() tea.Msg {
		id, err := d.Store.AddValidWindow(w, config.MarketNameList())
		if err != nil {
			return windowChangedMsg{fromForm: true, refusal: err.Error()}
		}
		d.logger().Info("schedule window added from the TUI", "id", id, "window", w.String())
		return windowChangedMsg{fromForm: true, notice: notice{text: fmt.Sprintf("Added window %d: %s.", id, w)}}
	}
}

// toggleWindow turns w off, or on if it is off (checked against the others).
func toggleWindow(d Deps, w schedule.Window) tea.Cmd {
	return func() tea.Msg {
		now, err := d.Store.SetWindowEnabledValid(w.ID, !w.Enabled, config.MarketNameList())
		switch {
		case errors.Is(err, store.ErrNoWindow):
			return windowChangedMsg{notice: notice{fmt.Sprintf("Window %d is gone.", w.ID), levelWarn}}
		case err != nil:
			return windowChangedMsg{notice: notice{fmt.Sprintf("Window %d stays off: %v.", w.ID, err), levelWarn}}
		}
		verb := "off"
		if now.Enabled {
			verb = "on"
		}
		d.logger().Info("schedule window turned "+verb+" from the TUI", "id", w.ID, "window", now.String())
		return windowChangedMsg{notice: notice{text: fmt.Sprintf("Window %d is %s.", w.ID, verb)}}
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

// windowColumns lays out the list.
const windowColumns = "  %-4s %-22s %-12s %-13s %-8s %-6s %s"

func (t scheduleTab) view(s styles, width int, now time.Time) string {
	head := s.section.Render("Schedule")
	if o, ok := schedule.Next(t.windows, now); ok && t.loaded {
		head += "  " + s.dim.Render(fmt.Sprintf("next opening: %s, window %d", o.Start.Format(dayTime), o.Window.ID))
	}
	lines := []string{head}
	switch {
	case t.problem != "":
		lines = append(lines, s.fail.Render(t.problem))
	case t.notice.text != "":
		lines = append(lines, wrapStyled(s.byLevel(t.notice.level), t.notice.text, width)...)
	}
	if t.form != nil {
		return strings.Join(append(append(lines, ""), t.form.lines(s)...), "\n")
	}
	if !t.loaded {
		return strings.Join(append(lines, "Reading…"), "\n")
	}
	if len(t.windows) == 0 {
		return strings.Join(append(lines, "", s.dim.Render("No schedule windows: a adds one. Runs start in them only while the daemon runs.")), "\n")
	}
	lines = append(lines, s.dim.Render(fmt.Sprintf(windowColumns, "#", "days", "hours", "market", "mode", "state", "next opening")))
	for i, w := range t.windows {
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
		if !w.Enabled {
			state = "off"
		} else if o, ok := schedule.Next([]schedule.Window{w}, now); ok {
			next = o.Start.Format(dayTime)
		}
		lines = append(lines, mark+strings.TrimPrefix(fmt.Sprintf(windowColumns, fmt.Sprint(w.ID), schedule.DaysString(w.Days),
			w.Start.String()+"-"+w.End.String(), market, mode, state, next), "  "))
	}
	if t.deleting != nil {
		lines = append(lines, "", s.warn.Render(fmt.Sprintf("Delete window %d (%s)?", t.deleting.ID, t.deleting)),
			s.key.Render("y")+" deletes it · "+s.key.Render("n")+" keeps it")
	}
	return strings.Join(lines, "\n")
}

func (f windowForm) lines(s styles) []string {
	field := func(i int, label, value string) string {
		mark := "  "
		if i == f.field {
			mark = s.key.Render("> ")
		}
		return mark + fmt.Sprintf("%-8s", label) + " " + value
	}
	var days []string
	for i, wd := range weekdays {
		box := "[ ]"
		if f.days[i] {
			box = "[x]"
		}
		day := box + " " + wd.String()[:3]
		if f.field == fieldDays && i == f.day {
			day = s.key.Render(day)
		}
		days = append(days, day)
	}
	market := "the config's"
	if m := marketChoices[f.market]; m != "" {
		market = m
	}
	mode := "live"
	if f.dry {
		mode = "dry run"
	}
	out := []string{
		s.section.Render("Add a schedule window") + "  " + s.dim.Render("local time; a window cannot cross midnight (24:00 ends one)"),
		field(fieldDays, "Days", strings.Join(days, "  ")),
		field(fieldStart, "Start", f.start.View()),
		field(fieldEnd, "End", f.end.View()),
		field(fieldWindowMarket, "Market", "‹ "+market+" ›"),
		field(fieldWindowMode, "Mode", "‹ "+mode+" ›"),
	}
	if f.refusal != "" {
		out = append(out, s.fail.Render("Refused: "+f.refusal))
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
