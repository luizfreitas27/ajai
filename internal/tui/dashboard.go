package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/schedule"
	"github.com/luizfreitas27/aja/internal/store"
)

const (
	// lastRunsShown is how many recent runs the Dashboard lists.
	lastRunsShown = 5
	// logKept is how many log lines the Dashboard keeps to show.
	logKept = 500
)

// snapshot is what the Dashboard shows, read in one go.
type snapshot struct {
	holder   run.Holder
	held     bool
	state    run.State
	hasState bool
	runs     []store.Run
	pending  int
	windows  []schedule.Window
	daemon   daemon.Process
	daemonUp bool
	problems []string // what could not be read
}

func readSnapshot(d Deps) snapshot {
	var s snapshot
	problem := func(what string, err error) {
		if err != nil {
			s.problems = append(s.problems, what+": "+err.Error())
		}
	}
	var err error
	s.holder, s.held, err = run.ReadLock(d.Paths.DataDir)
	problem("run lock", err)
	s.state, err = run.ReadState(run.StatePath(d.Paths.DataDir))
	s.hasState = err == nil
	if !errors.Is(err, run.ErrNoState) {
		problem("run state", err)
	}
	s.runs, err = d.Store.ListRuns(lastRunsShown)
	problem("runs", err)
	s.pending, err = d.Store.CountAnswers("pending")
	problem("answers", err)
	s.windows, err = d.Store.ListWindows()
	problem("schedule", err)
	s.daemon, s.daemonUp, err = daemon.ReadLock(d.Paths.DataDir)
	problem("daemon lock", err)
	return s
}

// refreshedMsg is a fresh snapshot and what the followed log gained.
type refreshedMsg struct {
	snap      snapshot
	tail      *logging.Tail
	daemonLog bool // the daemon's log is followed, not the terminal runs' one
	restarted bool // a different log is followed now: drop the old lines
	log       []byte
}

// dashboard is the Dashboard tab: the run in progress, the last runs and the
// log of the run in progress (the daemon's log for a run the daemon started).
type dashboard struct {
	snap      snapshot
	read      bool // a first snapshot is in
	tail      *logging.Tail
	daemonLog bool
	log       logLines
}

// refresh reads a snapshot and the followed log. The tail is used by one
// refresh at a time (Model.Update chains them).
func (b dashboard) refresh(d Deps) tea.Cmd {
	tail, daemonLog := b.tail, b.daemonLog
	return func() tea.Msg {
		now := d.Now()
		snap := readSnapshot(d)
		wantDaemon := snap.held && (snap.holder.Origin == run.OriginSchedule || snap.holder.Origin == run.OriginTelegram)
		restarted := tail == nil || wantDaemon != daemonLog
		if restarted {
			name := logging.FileName
			if wantDaemon {
				name = logging.DaemonFileName
			}
			tail = logging.NewTail(name, d.Paths.LogDir, now, 0)
		}
		data, err := tail.Read(now)
		if err != nil {
			snap.problems = append(snap.problems, "log: "+err.Error())
		}
		return refreshedMsg{snap: snap, tail: tail, daemonLog: wantDaemon, restarted: restarted, log: data}
	}
}

func (b dashboard) apply(msg refreshedMsg) dashboard {
	b.snap, b.read, b.tail, b.daemonLog = msg.snap, true, msg.tail, msg.daemonLog
	if msg.restarted {
		b.log = logLines{}
	}
	b.log = b.log.add(msg.log)
	return b
}

func (b dashboard) view(s styles, width, height int, now time.Time) string {
	if !b.read {
		return "Reading…"
	}
	var top []string
	add := func(label, text string) {
		if label != "" {
			label = s.section.Render(fmt.Sprintf("%-12s", label))
		} else {
			label = strings.Repeat(" ", 12)
		}
		top = append(top, label+"  "+text)
	}
	snap := b.snap
	for _, p := range snap.problems {
		add("Problem", s.warn.Render(p))
	}
	b.runView(s, add)

	pending := "none"
	if snap.pending > 0 {
		pending = s.warn.Render(fmt.Sprintf("%d to review", snap.pending))
	}
	add("Pending", pending)
	switch {
	case len(snap.windows) == 0:
		add("Schedule", s.dim.Render("no schedule windows"))
	default:
		if o, ok := schedule.Current(snap.windows, now); ok {
			add("Schedule", fmt.Sprintf("window %d open until %s (%s)", o.Window.ID, o.Window.End, o.Window))
		}
		if o, ok := schedule.Next(snap.windows, now); ok {
			add("Next window", fmt.Sprintf("%s, window %d (%s)", o.Start.Format("Mon 02 Jan 15:04"), o.Window.ID, o.Window))
		} else {
			add("Next window", s.dim.Render("every schedule window is disabled"))
		}
	}
	switch {
	case snap.daemonUp:
		add("Daemon", s.ok.Render("running")+" since "+snap.daemon.Started.Local().Format("Mon 02 Jan 15:04"))
	case len(snap.windows) > 0:
		add("Daemon", s.warn.Render("not running")+": schedule windows start runs only while it runs")
	default:
		add("Daemon", "not running")
	}

	top = append(top, "", s.section.Render("Last runs"))
	if len(snap.runs) == 0 {
		top = append(top, s.dim.Render("  none yet"))
	} else {
		top = append(top, s.dim.Render(fmt.Sprintf(runColumns, "#", "started", "market", "mode", "appl", "ext", "fail", "skip", "ended")))
	}
	for _, r := range snap.runs {
		top = append(top, runLine(r))
	}

	logTitle := "Today's log"
	if b.daemonLog {
		logTitle = "Today's daemon log"
	}
	top = append(top, "", s.section.Render(logTitle))
	room := height - len(top)
	lines := b.log.lines
	if room <= 0 {
		return strings.Join(top, "\n")
	}
	if len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	if len(lines) == 0 {
		top = append(top, s.dim.Render("  nothing yet"))
	}
	for _, l := range lines {
		top = append(top, "  "+prettyLog(l))
	}
	return strings.Join(top, "\n")
}

// runView describes the run in progress, as `aja status` does.
func (b dashboard) runView(s styles, add func(label, text string)) {
	snap := b.snap
	from := ""
	if snap.held {
		from = "started from " + snap.holder.From()
	}
	switch {
	case !snap.hasState && snap.held:
		add("Run", s.ok.Render("starting")+", "+from)
	case !snap.hasState:
		add("Run", "No run in progress.")
	case !snap.state.Alive():
		add("Run", s.warn.Render("the last run did not exit cleanly")+fmt.Sprintf(" (it started at %s)", snap.state.Started.Local().Format("Mon 02 Jan 15:04")))
	case !snap.state.NextRun.IsZero():
		add("Run", s.ok.Render("waiting")+fmt.Sprintf(": non-stop session, next run at %s; %s", snap.state.NextRun.Local().Format("15:04"), from))
		add("", fmt.Sprintf("market %s, %s", snap.state.Market, run.Mode(snap.state.DryRun)))
	default:
		add("Run", s.ok.Render("running")+fmt.Sprintf(" since %s, %s", snap.state.Started.Local().Format("15:04"), from))
		add("", fmt.Sprintf("market %s, %s", snap.state.Market, run.Mode(snap.state.DryRun)))
		if snap.state.Term != "" {
			add("", "searching "+snap.state.Term)
		}
		st := snap.state
		add("", fmt.Sprintf("applied %d · external %d · failed %d · skipped %d · would apply %d", st.Applied, st.External, st.Failed, st.Skipped, st.WouldApply))
	}
}

// runColumns lays out the last runs table.
const runColumns = "  %-5s %-16s  %-13s  %-7s %5v %5v %5v %5v  %s"

func runLine(r store.Run) string {
	when := r.StartedAt
	if t, ok := store.ParseTime(r.StartedAt); ok {
		when = t.Local().Format("Mon 02 Jan 15:04")
	}
	ended := r.Reason
	switch {
	case r.EndedAt == "" && ended == "":
		ended = "in progress, or never recorded an end"
	case ended == "":
		ended = "ended"
	}
	return fmt.Sprintf(runColumns, fmt.Sprint("#", r.ID), when, r.Market, run.Mode(r.DryRun), r.Applied, r.External, r.Failed, r.Skipped, ended)
}

// logLines are the last lines of the followed log; partial is a line still
// being written.
type logLines struct {
	lines   []string
	partial string
}

func (l logLines) add(data []byte) logLines {
	if len(data) == 0 {
		return l
	}
	parts := strings.Split(l.partial+string(data), "\n")
	l.partial = parts[len(parts)-1]
	lines := append(append([]string(nil), l.lines...), parts[:len(parts)-1]...)
	if len(lines) > logKept {
		lines = lines[len(lines)-logKept:]
	}
	l.lines = lines
	return l
}

// prettyLog shows one JSON log line as "15:04:05 INFO message key=value …";
// anything else is shown as it is.
func prettyLog(line string) string {
	dec := json.NewDecoder(strings.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return line
	}
	var when, level, msg string
	var attrs []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return line
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return line
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			text = string(raw)
		}
		switch key {
		case "time":
			if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
				when = t.Local().Format("15:04:05")
			}
		case "level":
			level = text
		case "msg":
			msg = text
		default:
			if strings.ContainsAny(text, " =\"") {
				text = fmt.Sprintf("%q", text)
			}
			attrs = append(attrs, key+"="+text)
		}
	}
	return strings.Join(append([]string{when, fmt.Sprintf("%-5s", level), msg}, attrs...), " ")
}
