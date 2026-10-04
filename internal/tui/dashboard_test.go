package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/daemon"
	"github.com/luizfreitas27/ajai/internal/logging"
	"github.com/luizfreitas27/ajai/internal/memory"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

// logAt appends a JSON log line, as ajai writes them, to the day's log name gives.
func (h *harness) logAt(name logging.FileNamer, at time.Time, msg string, attrs string) {
	h.t.Helper()
	f, err := os.OpenFile(name(h.p.LogDir, at), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if attrs != "" {
		attrs = "," + attrs
	}
	_, _ = fmt.Fprintf(f, `{"time":%q,"level":"INFO","msg":%q%s}`+"\n", at.Format(time.RFC3339Nano), msg, attrs)
}

// holdRun takes the run lock as holder.
func (h *harness) holdRun(holder run.Holder) {
	h.t.Helper()
	lock, err := run.AcquireLock(h.p.DataDir, holder)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(lock.Release)
}

// pastRun records a finished run that started at start.
func (h *harness) pastRun(start time.Time, market string, dry bool, r store.Run) {
	h.t.Helper()
	saved := h.now
	h.now = start
	id, err := h.st.StartRun(context.Background(), market, dry)
	if err != nil {
		h.t.Fatal(err)
	}
	h.now = start.Add(40 * time.Minute)
	r.ID = id
	if err := h.st.FinishRun(context.Background(), r); err != nil {
		h.t.Fatal(err)
	}
	h.now = saved
}

func TestTheDashboardWithNothingYet(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.shows("1 Dashboard", "No run in progress.", "Pending", "none", "no schedule windows", "not running", "none yet", "nothing yet")
	h.golden("dashboard-empty")
}

func TestTheDashboardFollowsARunLive(t *testing.T) {
	h := newHarness(t)
	h.pastRun(time.Date(2026, 10, 4, 14, 0, 0, 0, time.Local), "brasil", true,
		store.Run{Applied: 0, Skipped: 4, Reason: "the search ran out of jobs"})
	h.pastRun(time.Date(2026, 10, 4, 19, 0, 0, 0, time.Local), "internacional", false,
		store.Run{Applied: 3, External: 1, Failed: 1, Skipped: 9, Reason: "stopped from Telegram"})
	if _, err := memory.New(h.st, nil).Remember("Years of Go?", "text", "3", "ai", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Monday, time.Wednesday}, Start: 14 * 60, End: 16 * 60, Market: "brasil", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	started := h.now.Add(-20 * time.Minute)
	h.holdRun(run.Holder{PID: os.Getpid(), Origin: run.OriginTerminal, Started: started})
	state := run.State{PID: os.Getpid(), Started: started, Market: "brasil", DryRun: true, Applied: 2, Skipped: 5, WouldApply: 2, Term: "Go developer"}
	if err := run.WriteState(run.StatePath(h.p.DataDir), state); err != nil {
		t.Fatal(err)
	}
	h.logAt(logging.FileName, h.now.Add(-time.Minute), "searching", `"term":"Go developer","page":2`)
	h.logAt(logging.FileName, h.now.Add(-30*time.Second), "skipped a job", `"reason":"already applied"`)

	h.open(100, 30)
	h.shows("running since 09:10, started from the terminal", "market brasil, dry run", "searching Go developer",
		"applied 2 · external 0 · failed 0 · skipped 5 · would apply 2", "1 to review",
		"Mon 05 Oct 14:00, window 1 (Mon, Wed 14:00-16:00, market brasil)",
		"not running: schedule windows start runs only while it runs",
		"04 Oct 19:00  internacional  live       3    1    1    9  stopped from Telegram",
		`09:29:00 INFO  searching term="Go developer" page=2`, `09:29:30 INFO  skipped a job reason="already applied"`)
	h.golden("dashboard-running")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.golden("dashboard-smallest")
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	// A second later: more done, a line half written then finished.
	h.now = h.now.Add(time.Second)
	state.Applied, state.Term = 3, "Golang"
	if err := run.WriteState(run.StatePath(h.p.DataDir), state); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(logging.FileName(h.p.LogDir, h.now), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"time":"2026-10-05T09:30:01-03:00","level":"INFO","msg":"appl`)
	h.refresh()
	h.shows("searching Golang", "applied 3 ·")
	if screen := h.screen(); strings.Contains(screen, "appl\n") {
		t.Errorf("a line still being written is not shown:\n%s", screen)
	}
	_, _ = f.WriteString(`ied","job":"42"}` + "\n")
	_ = f.Close()
	h.refresh()
	h.shows(`INFO  applied job=42`)
}

func TestTheDashboardShowsAWaitingSessionAStartAndADeadRun(t *testing.T) {
	h := newHarness(t)
	h.holdRun(run.Holder{PID: os.Getpid(), Origin: run.OriginTerminal, Started: h.now})
	h.open(100, 30)
	h.shows("starting, started from the terminal")

	state := run.State{PID: os.Getpid(), Started: h.now.Add(-time.Hour), Market: "internacional", NextRun: h.now.Add(45 * time.Minute)}
	if err := run.WriteState(run.StatePath(h.p.DataDir), state); err != nil {
		t.Fatal(err)
	}
	h.refresh()
	h.shows("waiting: non-stop session, next run at 10:15, started from the terminal", "market internacional, live")

	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Skip("no true(1):", err)
	}
	state.PID, state.NextRun = gone.Process.Pid, time.Time{}
	if err := run.WriteState(run.StatePath(h.p.DataDir), state); err != nil {
		t.Fatal(err)
	}
	h.refresh()
	h.shows("the last run did not exit cleanly (it started at Mon 05 Oct 08:30)")
}

func TestARunTheDaemonStartedIsFollowedInTheDaemonLog(t *testing.T) {
	h := newHarness(t)
	d, err := daemon.AcquireLock(h.p.DataDir, h.now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Release)
	h.logAt(logging.FileName, h.now.Add(-time.Hour), "a terminal run", "")
	h.logAt(logging.DaemonFileName, h.now.Add(-time.Minute), "window opened", `"window":1`)
	h.open(100, 30)
	h.shows("Daemon", "running since Mon 05 Oct 07:30", "Today's log", "a terminal run")

	lock, err := run.AcquireLock(h.p.DataDir, run.Holder{PID: os.Getpid(), Origin: run.OriginSchedule, Window: 1, Started: h.now})
	if err != nil {
		t.Fatal(err)
	}
	h.refresh()
	h.shows("starting, started from schedule window 1", "Today's daemon log", "window opened window=1")
	if strings.Contains(h.screen(), "a terminal run") {
		t.Errorf("the terminal runs' log is no longer shown:\n%s", h.screen())
	}

	// The run ends: its last lines stay until another run starts.
	h.logAt(logging.DaemonFileName, h.now, "run ended", "")
	lock.Release()
	h.refresh()
	h.shows("No run in progress.", "Today's daemon log", "window opened", "run ended")
	h.holdRun(run.Holder{PID: os.Getpid(), Origin: run.OriginTerminal, Started: h.now})
	h.refresh()
	h.shows("Today's log", "a terminal run")
}

func TestTheLogIsFollowedPastMidnight(t *testing.T) {
	h := newHarness(t)
	h.now = time.Date(2026, 10, 5, 23, 59, 30, 0, time.Local)
	h.logAt(logging.FileName, h.now, "before midnight", "")
	h.open(100, 30)
	h.shows("before midnight")
	h.now = h.now.Add(time.Minute)
	h.logAt(logging.FileName, h.now, "after midnight", "")
	h.refresh()
	h.shows("before midnight", "00:00:30 INFO  after midnight")
}

func TestKeysHelpQuitAndATooSmallTerminal(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("?")
	h.shows("Keys", "Everywhere", "1-5, tab    show a tab; shift+tab goes back", "this list; Esc closes it",
		"quit, as Ctrl-C does (a run in progress keeps running)")
	h.golden("help")
	h.press("esc")
	h.shows("No run in progress.")
	h.press("?", "1")
	h.shows("No run in progress.")

	h.send(tea.WindowSizeMsg{Width: 70, Height: 20})
	h.shows("ajai needs a terminal of at least 80×24; this one is 70×20.")
	h.press("q")
	if !h.quit {
		t.Error("q quits")
	}

	h2 := newHarness(t)
	h2.open(100, 30)
	h2.press("ctrl+c")
	if !h2.quit {
		t.Error("ctrl+c quits")
	}
}

func TestPrettyLog(t *testing.T) {
	at := time.Date(2026, 10, 5, 9, 1, 2, 0, time.Local).Format(time.RFC3339Nano)
	for line, want := range map[string]string{
		`{"time":"` + at + `","level":"WARN","msg":"slow page","ms":1200,"url":"https://x/y?a=b"}`: `09:01:02 WARN  slow page ms=1200 url="https://x/y?a=b"`,
		`{"time":"` + at + `","level":"INFO","msg":"hi","who":"me"}`:                               `09:01:02 INFO  hi who=me`,
		`not json at all`: `not json at all`,
		`{"broken":`:      `{"broken":`,
	} {
		if got := prettyLog(line); got != want {
			t.Errorf("prettyLog(%s) = %q, want %q", line, got, want)
		}
	}
}
