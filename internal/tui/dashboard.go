package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/daemon"
	"github.com/luizfreitas27/ajai/internal/logging"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

const (
	// lastRunsRead is how many recent runs the Dashboard reads; fewer are
	// shown on a short terminal, so the log keeps minLogLines.
	lastRunsRead = 5
	minLogLines  = 4
	// logKept is how many log lines the Dashboard keeps to show.
	logKept = 500
	// dayTime is how the Dashboard writes a moment.
	dayTime = "Mon 02 Jan 15:04"
)

// snapshot is what the Dashboard shows, read in one go.
type snapshot struct {
	activity run.Activity
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
	s.activity, err = run.ReadActivity(d.Paths.DataDir)
	problem("run", err)
	s.runs, err = d.Store.ListRuns(lastRunsRead)
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
	switched  bool // another log is followed now: drop the old lines
	log       []byte
	startExit *startExit // a run the TUI started ended before holding the run lock
}

// dashboard is the Dashboard tab: the run in progress, the last runs and a
// log: the daemon's while a run the daemon started holds the run lock, the
// terminal runs' while one of those does, and the last one followed while no
// run does (so a run's last lines stay when it ends).
type dashboard struct {
	snap      snapshot
	loaded    bool // a first snapshot is in
	tail      *logging.Tail
	daemonLog bool
	log       logLines
}

// refresh reads a snapshot and the followed log, and whether watch, a run the
// TUI started, ended. The tail is used by one refresh at a time (Model.Update
// chains them).
func (dash dashboard) refresh(d Deps, watch *startedRun) tea.Cmd {
	tail, daemonLog := dash.tail, dash.daemonLog
	return func() tea.Msg {
		exit := watchStart(d, watch) // before the snapshot: a run that ended has let the lock go
		now := d.Now()
		snap := readSnapshot(d)
		act := snap.activity
		switched := tail == nil || (act.Held && act.Holder.ByDaemon() != daemonLog)
		if switched {
			daemonLog = act.Held && act.Holder.ByDaemon()
			name := logging.FileName
			if daemonLog {
				name = logging.DaemonFileName
			}
			tail = logging.NewTail(name, d.Paths.LogDir, now, 0)
		}
		data, err := tail.Read(now)
		if err != nil {
			snap.problems = append(snap.problems, "log: "+err.Error())
		}
		return refreshedMsg{snap: snap, tail: tail, daemonLog: daemonLog, switched: switched, log: data, startExit: exit}
	}
}

func (dash dashboard) apply(msg refreshedMsg) dashboard {
	dash.snap, dash.loaded, dash.tail, dash.daemonLog = msg.snap, true, msg.tail, msg.daemonLog
	if msg.switched {
		dash.log = logLines{}
	}
	dash.log = dash.log.add(msg.log)
	return dash
}

func (dash dashboard) view(s styles, width, height int, now time.Time, setup notice) string {
	if !dash.loaded {
		return "Reading…"
	}
	var top []string
	add := func(label, text string) {
		top = append(top, s.section.Render(fmt.Sprintf("%-12s", label))+"  "+text)
	}
	snap := dash.snap
	for _, p := range snap.problems {
		add("Problem", s.warn.Render(p))
	}
	runView(s, snap.activity, add)

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
			add("Next opening", fmt.Sprintf("%s, window %d (%s)", o.Start.Format(dayTime), o.Window.ID, o.Window))
		} else {
			add("Next opening", s.dim.Render("none: every schedule window is disabled"))
		}
	}
	switch {
	case snap.daemonUp && !snap.daemon.Started.IsZero():
		add("Daemon", s.ok.Render("running")+" since "+snap.daemon.Started.Local().Format(dayTime))
	case snap.daemonUp:
		add("Daemon", s.ok.Render("starting"))
	case len(snap.windows) > 0:
		add("Daemon", s.warn.Render("not running")+": schedule windows start runs only while it runs")
	default:
		add("Daemon", "not running")
	}
	if setup.text != "" {
		add("Setup", s.byLevel(setup.level).Render(setup.text))
	}

	// The runs table takes what the log can spare.
	rows := min(len(snap.runs), height-len(top)-3-2-minLogLines)
	top = append(top, "", s.section.Render("Last runs"))
	switch {
	case len(snap.runs) == 0:
		top = append(top, s.dim.Render("  none yet"))
	case rows > 0:
		top = append(top, s.dim.Render(fmt.Sprintf(runColumns, "started", "market", "mode", "appl", "ext", "fail", "skip", "ended")))
		for _, r := range snap.runs[:rows] {
			top = append(top, runLine(r))
		}
	}

	logTitle := "Today's log"
	if dash.daemonLog {
		logTitle = "Today's daemon log"
	}
	top = append(top, "", s.section.Render(logTitle))
	room := height - len(top)
	lines := dash.log.lines
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

// runView describes the run in progress.
func runView(s styles, act run.Activity, add func(label, text string)) {
	from := ""
	if f := act.From(); f != "" {
		from = ", started from " + f
	}
	st := act.State
	switch act.Kind {
	case run.Idle:
		add("Run", "No run in progress.")
	case run.Starting:
		add("Run", s.ok.Render("starting")+from)
	case run.Stale:
		add("Run", s.warn.Render("the last run did not exit cleanly")+fmt.Sprintf(" (it started at %s)", st.Started.Local().Format(dayTime)))
	case run.Waiting:
		add("Run", s.ok.Render("waiting")+fmt.Sprintf(": non-stop session, next run at %s%s", st.NextRun.Local().Format("15:04"), from))
		add("", fmt.Sprintf("market %s, %s", st.Market, run.Mode(st.DryRun)))
	case run.Running:
		add("Run", s.ok.Render("running")+fmt.Sprintf(" since %s%s", st.Started.Local().Format("15:04"), from))
		add("", fmt.Sprintf("market %s, %s", st.Market, run.Mode(st.DryRun)))
		if st.Term != "" {
			add("", "searching "+st.Term)
		}
		add("", fmt.Sprintf("applied %d · external %d · failed %d · skipped %d · would apply %d", st.Applied, st.External, st.Failed, st.Skipped, st.WouldApply))
	}
}

// runColumns lays out the last runs table: it leaves the ending reason 20
// cells on the smallest terminal.
const runColumns = "  %-12s  %-13s  %-7s %4v %4v %4v %4v  %s"

func runLine(r store.Run) string {
	when := r.StartedAt
	if t, ok := store.ParseTime(r.StartedAt); ok {
		when = t.Local().Format("02 Jan 15:04")
	}
	ended := r.Reason
	switch {
	case r.EndedAt == "" && ended == "":
		ended = "in progress, or never recorded an end"
	case ended == "":
		ended = "ended"
	}
	return fmt.Sprintf(runColumns, when, r.Market, run.Mode(r.DryRun), r.Applied, r.External, r.Failed, r.Skipped, ended)
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
