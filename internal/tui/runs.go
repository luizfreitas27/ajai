package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// reasonStopped is the ending reason of a run stopped from the TUI.
const reasonStopped = "stopped from the TUI"

// marketChoices are the Start form's markets; "" is the config's own.
var marketChoices = append([]string{""}, config.MarketNameList()...)

// runControl starts and stops runs from the Dashboard and says how that went.
// A run it starts is `aja run --unattended` in a process of its own: closing
// the TUI never ends it.
type runControl struct {
	form     *runForm
	confirm  *run.Holder // the run Stop asks about
	started  *startedRun // a run this TUI started, until it holds the run lock or ends
	stopping bool
	notice   notice
}

// startedRun is a run process this TUI started.
type startedRun struct {
	pid    int
	at     time.Time
	exited <-chan error // its end, while the TUI runs
}

// runForm is the Start form.
type runForm struct {
	field   int // 0 market, 1 mode, 2 maximum
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

// startExit is how a run this TUI started ended before it held the run lock.
type startExit struct {
	err    error
	reason string // from its log line, when it said why
}

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
	args := []string{"run", "--unattended", "--origin", "tui"}
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

// start opens the Start form.
func (c runControl) start() runControl {
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
		case msg.err != nil:
			c.notice = notice{"Could not ask the run to stop: " + msg.err.Error(), levelFail}
		default:
			c.stopping = true
			c.notice = notice{"Stopping " + msg.holder.String() + ": it ends once the job in hand is done.", levelWarn}
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
		c.confirm = nil
		return c, stopRun(d)
	case "n", "esc":
		c.confirm = nil
	}
	return c, nil
}

func (c runControl) formKey(msg tea.KeyMsg, d Deps) (runControl, tea.Cmd) {
	f := *c.form
	switch key := msg.String(); key {
	case "esc":
		c.form = nil
		return c, nil
	case "enter":
		args, err := f.args()
		if err != nil {
			f.refusal = err.Error()
			c.form = &f
			return c, nil
		}
		c.form = nil
		return c, startRun(d, args)
	case "up", "shift+tab":
		f.field = (f.field + 2) % 3
	case "down", "tab":
		f.field = (f.field + 1) % 3
	case "left", "right", "h", "l", " ":
		step := 1
		if key == "left" || key == "h" {
			step = len(marketChoices) - 1
		}
		switch f.field {
		case 0:
			f.market = (f.market + step) % len(marketChoices)
		case 1:
			f.dry = !f.dry
		default:
			var cmd tea.Cmd
			f.max, cmd = f.max.Update(msg)
			c.form = &f
			return c, cmd
		}
	default:
		if f.field == 2 {
			var cmd tea.Cmd
			f.max, cmd = f.max.Update(msg)
			c.form = &f
			return c, cmd
		}
	}
	if f.field == 2 {
		f.max.Focus()
	} else {
		f.max.Blur()
	}
	c.form = &f
	return c, nil
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

// stopRun asks the run in progress, whoever started it, to end.
func stopRun(d Deps) tea.Cmd {
	return func() tea.Msg {
		h, err := run.RequestStop(d.Paths.DataDir, reasonStopped)
		if err == nil {
			d.logger().Info("stop requested from the TUI", "run", h.String())
		}
		return stopAskedMsg{holder: h, err: err}
	}
}

// watchStart is, for a run this TUI started, its end if it came before it held
// the run lock (refresh reads it; never blocks).
func watchStart(d Deps, s *startedRun) *startExit {
	if s == nil {
		return nil
	}
	select {
	case err := <-s.exited:
		return &startExit{err: err, reason: startFailure(d.Paths.LogDir, s.at)}
	default:
		return nil
	}
}

// startFailure is the error the run log's last "could not start" line since
// since gives, or "".
func startFailure(logDir string, since time.Time) string {
	f, err := os.Open(logging.FileName(logDir, since))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	reason := ""
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 64*1024), 1<<20)
	for lines.Scan() {
		var line struct {
			Time  time.Time `json:"time"`
			Msg   string    `json:"msg"`
			Error string    `json:"error"`
		}
		if json.Unmarshal(lines.Bytes(), &line) == nil && line.Msg == run.StartFailedMsg && !line.Time.Before(since.Add(-time.Second)) {
			reason = line.Error
		}
	}
	return reason
}

// observe follows the run lock from one refresh to the next: a run this TUI
// started holding it (or ending first), and any run ending.
func (c runControl) observe(before, now run.Activity, exit *startExit, runs []store.Run) runControl {
	if s := c.started; s != nil {
		switch {
		case now.Held && now.Holder.PID == s.pid:
			c.started, c.notice = nil, notice{text: fmt.Sprintf("Started: the run is in progress (pid %d).", s.pid)}
		case exit != nil:
			c.started = nil
			why := exit.reason
			if why == "" {
				why = fmt.Sprintf("aja ended (%v) before it began; today's log may say more", exit.err)
			}
			c.notice = notice{"The run did not start: " + why, levelFail}
		}
	}
	if before.Held && !now.Held {
		c.stopping = false
		if len(runs) > 0 {
			r := runs[0]
			reason := r.Reason
			if reason == "" {
				reason = "it never recorded an end"
			}
			c.notice = notice{text: fmt.Sprintf("The run ended (%s): applied %d · external %d · failed %d · skipped %d.",
				reason, r.Applied, r.External, r.Failed, r.Skipped)}
		}
	}
	return c
}

// lines are what the control shows above the Dashboard: the form, the
// confirmation, or the last outcome.
func (c runControl) lines(s styles, width int) []string {
	switch {
	case c.confirm != nil:
		h := *c.confirm
		what := "the run that is just starting"
		if f := (run.Activity{Held: true, Holder: h}).From(); f != "" {
			what = fmt.Sprintf("the run started from %s at %s", f, h.Started.Local().Format("15:04"))
		}
		return []string{s.warn.Render("Stop " + what + "? It ends once the job in hand is done."),
			s.key.Render("y") + " stops it · " + s.key.Render("n") + " keeps it", ""}
	case c.form != nil:
		return c.form.lines(s)
	case c.notice.text != "":
		var out []string
		for _, l := range wrap(c.notice.text, width) {
			out = append(out, s.byLevel(c.notice.level).Render(l))
		}
		return append(out, "")
	}
	return nil
}

func (f runForm) lines(s styles) []string {
	field := func(i int, label, value string) string {
		mark := "  "
		if i == f.field {
			mark = s.key.Render("> ")
		}
		return mark + fmt.Sprintf("%-9s", label) + " " + value
	}
	market := "the config's"
	if m := marketChoices[f.market]; m != "" {
		market = m
	}
	mode := "live: applies for real"
	if f.dry {
		mode = "dry run: fills every form, sends none"
	}
	out := []string{
		s.section.Render("Start a run") + "  " + s.dim.Render("unattended, in a process of its own: closing the TUI never ends it"),
		field(0, "Market", "‹ "+market+" ›"),
		field(1, "Mode", "‹ "+mode+" ›"),
		field(2, "Maximum", f.max.View()+s.dim.Render("  applications; 0 or blank: the pacing cap")),
	}
	if f.refusal != "" {
		out = append(out, s.fail.Render("Refused: "+f.refusal))
	}
	return append(out, "")
}
