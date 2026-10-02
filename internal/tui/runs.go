package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/store"
)

const (
	// reasonStopped is the ending reason of a run stopped from the TUI.
	reasonStopped = "stopped from the TUI"
	// startWait is how long a run the TUI started may take to hold the run
	// lock before the TUI says it has not begun.
	startWait = 20 * time.Second
)

// marketChoices are the markets a form offers (Start, a schedule window); ""
// is the config's own.
var marketChoices = append([]string{""}, config.MarketNameList()...)

// marketLabel names marketChoices[i].
func marketLabel(i int) string {
	if m := marketChoices[i]; m != "" {
		return m
	}
	return "the config's"
}

// formLine is one field of a form: a marker when selected, the label, the value.
func formLine(s styles, selected bool, label, value string) string {
	mark := "  "
	if selected {
		mark = s.key.Render("> ")
	}
	return mark + fmt.Sprintf("%-9s", label) + " " + value
}

// runControl starts and stops runs from the Dashboard and says how that went.
// A run it starts is `aja run --unattended` in a process of its own: closing
// the TUI never ends it.
type runControl struct {
	form     *runForm
	confirm  *run.Holder // the run Stop asks about
	started  *startedRun // a run this TUI started, until its process ends
	stopping string      // the run a stop was asked of, until it lets the run lock go
	lastEnd  int64       // the newest run seen ended: later ones are announced
	seen     bool        // lastEnd is known (the first refresh read it)
	notice   notice
}

// startedRun is a run process this TUI started.
type startedRun struct {
	pid    int
	at     time.Time
	exited <-chan error // its end, while the TUI runs
	begun  bool         // it has held the run lock
	slow   bool         // the TUI said it had not begun yet
}

// The Start form's fields.
const (
	fieldMarket = iota
	fieldMode
	fieldMax
	formFields
)

// runForm is the Start form.
type runForm struct {
	field   int
	market  int // index into marketChoices
	dry     bool
	max     textinput.Model
	refusal string
}

type runStartedMsg struct {
	run     *startedRun
	refusal string
}

type stopAskedMsg struct {
	holder run.Holder
	err    error
}

// startExit is how a run this TUI started ended.
type startExit struct {
	err    error
	reason string // why it could not start, from its log, when it said so
}

// errRunChanged: the run in progress is not the one Stop asked about.
var errRunChanged = errors.New("another run is in progress now")

// editing reports whether keys go to the form or the confirmation.
func (c runControl) editing() bool { return c.form != nil || c.confirm != nil }

func newRunForm() *runForm {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "0"
	in.CharLimit = 4
	in.Width = 6
	in.Cursor.SetMode(cursor.CursorStatic)
	return &runForm{dry: true, max: in} // dry run unless chosen otherwise
}

// args are `aja run`'s for the form's choices.
func (f runForm) args() ([]string, error) {
	args := []string{"run", "--unattended", "--origin", string(run.OriginTUI)}
	if m := marketChoices[f.market]; m != "" {
		args = append(args, "--market", m)
	}
	if f.dry {
		args = append(args, "--dry-run")
	}
	if text := strings.TrimSpace(f.max.Value()); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			return nil, errors.New("the maximum is a whole number (0: the pacing cap)")
		}
		if n > 0 {
			args = append(args, "--max", text)
		}
	}
	return args, nil
}

// start opens the Start form, unless a run this TUI started has not begun.
func (c runControl) start() runControl {
	if c.started != nil && !c.started.begun {
		c.notice = notice{"A run is starting already (pid " + strconv.Itoa(c.started.pid) + ").", levelWarn}
		return c
	}
	c.form, c.confirm = newRunForm(), nil
	return c
}

// stop asks to confirm stopping the run activity shows.
func (c runControl) stop(act run.Activity) runControl {
	if !act.Held {
		c.notice = notice{text: "No run in progress."}
		return c
	}
	h := act.Holder
	c.confirm, c.form = &h, nil
	return c
}

func (c runControl) update(msg tea.Msg, d Deps) (runControl, tea.Cmd) {
	switch msg := msg.(type) {
	case runStartedMsg:
		if msg.refusal != "" {
			c.notice = notice{msg.refusal, levelWarn}
			return c, nil
		}
		c.started = msg.run
		c.notice = notice{text: fmt.Sprintf("Starting a run (pid %d)…", msg.run.pid)}
	case stopAskedMsg:
		switch {
		case errors.Is(msg.err, run.ErrNoRun):
			c.notice = notice{text: "No run in progress."}
		case errors.Is(msg.err, errRunChanged):
			c.notice = notice{"Not stopped: " + msg.holder.String() + " is in progress now; x stops it.", levelWarn}
		case msg.err != nil:
			c.notice = notice{"Could not ask the run to stop: " + msg.err.Error(), levelFail}
		default:
			c.stopping = msg.holder.String()
			c.notice = notice{}
		}
	case tea.KeyMsg:
		switch {
		case c.confirm != nil:
			return c.confirmKey(msg, d)
		case c.form != nil:
			return c.formKey(msg, d)
		}
	}
	return c, nil
}

func (c runControl) confirmKey(msg tea.KeyMsg, d Deps) (runControl, tea.Cmd) {
	switch msg.String() {
	case "y":
		h := *c.confirm
		c.confirm = nil
		return c, stopRun(d, h)
	case "n", "esc":
		c.confirm = nil
	}
	return c, nil
}

func (c runControl) formKey(msg tea.KeyMsg, d Deps) (runControl, tea.Cmd) {
	f := *c.form
	var cmd tea.Cmd
	switch key := msg.String(); {
	case key == "esc":
		c.form = nil
		return c, nil
	case key == "enter":
		args, err := f.args()
		if err != nil {
			f.refusal = err.Error()
			c.form = &f
			return c, nil
		}
		c.form = nil
		return c, startRun(d, args)
	case key == "up" || key == "shift+tab":
		f.field = (f.field + formFields - 1) % formFields
	case key == "down" || key == "tab":
		f.field = (f.field + 1) % formFields
	case f.field == fieldMax:
		f.max, cmd = f.max.Update(msg)
	case key == "left" || key == "h":
		f = f.change(-1)
	case key == "right" || key == "l" || key == " ":
		f = f.change(1)
	}
	if f.field == fieldMax {
		f.max.Focus()
	} else {
		f.max.Blur()
	}
	c.form = &f
	return c, cmd
}

// change moves the market or the mode choice by step.
func (f runForm) change(step int) runForm {
	switch f.field {
	case fieldMarket:
		f.market = (f.market + step + len(marketChoices)) % len(marketChoices)
	case fieldMode:
		f.dry = !f.dry
	}
	return f
}

// startRun starts `aja args` in a process of its own, unless a run holds the
// run lock (it would refuse anyway, with nobody to read why).
func startRun(d Deps, args []string) tea.Cmd {
	return func() tea.Msg {
		if act, _ := run.ReadActivity(d.Paths.DataDir); act.Held {
			return runStartedMsg{refusal: "Not started: " + act.Holder.String() + " is in progress; x stops it."}
		}
		at := d.Now()
		pid, exited, err := d.Launcher.Start(args)
		if err != nil {
			return runStartedMsg{refusal: "Could not start aja: " + err.Error()}
		}
		d.logger().Info("run started from the TUI", "pid", pid, "args", strings.Join(args[1:], " "))
		return runStartedMsg{run: &startedRun{pid: pid, at: at, exited: exited}}
	}
}

// stopRun asks the run h, the one the user confirmed, to end; another run
// holding the lock by now is left alone.
func stopRun(d Deps, h run.Holder) tea.Cmd {
	return func() tea.Msg {
		if now, held, _ := run.ReadLock(d.Paths.DataDir); held && (now.PID != h.PID || !now.Started.Equal(h.Started)) {
			return stopAskedMsg{holder: now, err: errRunChanged}
		}
		holder, err := run.RequestStop(d.Paths.DataDir, reasonStopped)
		if err == nil {
			d.logger().Info("stop requested from the TUI", "run", holder.String())
		}
		return stopAskedMsg{holder: holder, err: err}
	}
}

// watchStart is how s, a run this TUI started, ended, if it has (refresh reads
// it; it never blocks).
func watchStart(d Deps, s *startedRun) *startExit {
	if s == nil {
		return nil
	}
	select {
	case err := <-s.exited:
		return &startExit{err: err, reason: run.ReadStartFailure(logging.FileName(d.Paths.LogDir, s.at), s.at)}
	default:
		return nil
	}
}

// observe follows one refresh to the next: a run ending (each run of a
// non-stop session too), a run this TUI started beginning or not, and the
// end of a stop.
func (c runControl) observe(before, now run.Activity, exit *startExit, runs []store.Run, at time.Time) runControl {
	newest := store.Run{}
	if len(runs) > 0 {
		newest = runs[0]
	}
	switch {
	case !c.seen: // what ended before the TUI opened is not news
		c.seen, c.lastEnd = true, endedID(newest)
	case newest.EndedAt != "" && newest.ID != c.lastEnd:
		c.lastEnd = newest.ID
		c.notice = notice{text: fmt.Sprintf("The run ended (%s): applied %d · external %d · failed %d · skipped %d.",
			firstOf(newest.Reason, "no reason recorded"), newest.Applied, newest.External, newest.Failed, newest.Skipped)}
	case before.Held && !now.Held && !recordedSince(newest, before.Holder.Started):
		c.notice = notice{"The run " + strings.TrimPrefix(before.Holder.String(), "a run ") + " ended before it recorded anything; today's log says why.", levelWarn}
	}
	if !now.Held {
		c.stopping = ""
	}
	s := c.started
	if s == nil {
		return c
	}
	switch {
	case exit != nil && exit.reason != "":
		c.started, c.notice = nil, notice{"The run did not start: " + exit.reason, levelFail}
	case exit != nil && !s.begun && exit.err != nil:
		c.started, c.notice = nil, notice{fmt.Sprintf("The run did not start: aja ended (%v) before it began; today's log may say why.", exit.err), levelFail}
	case exit != nil:
		c.started = nil // it ran and ended: the run's own line says how
	case now.Held && now.Holder.PID == s.pid && !s.begun:
		begun := *s
		begun.begun = true
		c.started, c.notice = &begun, notice{text: fmt.Sprintf("Started: the run is in progress (pid %d).", s.pid)}
	case !s.begun && !s.slow && at.Sub(s.at) > startWait:
		slow := *s
		slow.slow = true
		c.started, c.notice = &slow, notice{fmt.Sprintf("The run (pid %d) has not begun after %s; today's log may say why.", s.pid, startWait), levelWarn}
	}
	return c
}

func endedID(r store.Run) int64 {
	if r.EndedAt == "" {
		return 0
	}
	return r.ID
}

// recordedSince reports whether r is a run that started at or after since.
func recordedSince(r store.Run, since time.Time) bool {
	t, ok := store.ParseTime(r.StartedAt)
	return ok && !t.Before(since.Add(-time.Second))
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// lines are what the control shows above the Dashboard: the form, the
// confirmation, a stop under way, the last outcome.
func (c runControl) lines(s styles, width int) []string {
	switch {
	case c.confirm != nil:
		return []string{s.warn.Render("Stop " + c.confirm.String() + "? It ends once the job in hand is done."),
			s.key.Render("y") + " stops it · " + s.key.Render("n") + " keeps it", ""}
	case c.form != nil:
		return c.form.lines(s)
	}
	var out []string
	say := func(n notice) {
		for _, l := range wrap(n.text, width) {
			out = append(out, s.byLevel(n.level).Render(l))
		}
	}
	if c.stopping != "" {
		say(notice{"Stopping " + c.stopping + ": it ends once the job in hand is done.", levelWarn})
	}
	if c.notice.text != "" {
		say(c.notice)
	}
	if len(out) > 0 {
		out = append(out, "")
	}
	return out
}

func (f runForm) lines(s styles) []string {
	field := func(i int, label, value string) string { return formLine(s, i == f.field, label, value) }
	market := marketLabel(f.market)
	mode := "live: applies for real"
	if f.dry {
		mode = "dry run: fills every form, sends none"
	}
	out := []string{
		s.section.Render("Start a run") + "  " + s.dim.Render("unattended, in a process of its own: closing the TUI never ends it"),
		field(fieldMarket, "Market", "‹ "+market+" ›"),
		field(fieldMode, "Mode", "‹ "+mode+" ›"),
		field(fieldMax, "Maximum", f.max.View()+s.dim.Render("  applications; 0 or blank: the pacing cap")),
	}
	if f.refusal != "" {
		out = append(out, s.fail.Render("Refused: "+f.refusal))
	}
	return append(out, "")
}
