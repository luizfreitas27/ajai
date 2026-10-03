package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/daemon"
)

// systemctlSince is what the fake systemctl was asked after the first n calls,
// status reads left out.
func (h *harness) systemctlSince(n int) []string {
	var out []string
	for _, c := range h.systemd.calls[n:] {
		if !strings.HasPrefix(c, "is-") && c != "show-environment" {
			out = append(out, c)
		}
	}
	return out
}

func TestTheDaemonIsInstalledStoppedStartedAndUninstalledAfterAsking(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.scheduleTab()
	h.shows("Daemon  not running · systemd unit: not installed", "i install · u uninstall · s start · t stop")

	h.press("s")
	h.shows("The daemon's unit is not installed: i installs it.")
	h.press("i")
	h.shows("Install the daemon as a systemd user unit? It starts now and with your desktop session.", "y does it · n leaves it")
	h.golden("daemon-question")
	h.press("n")
	if got := h.systemctlSince(0); len(got) != 0 {
		t.Fatalf("n changes nothing: %q", got)
	}

	before := len(h.systemd.calls)
	h.press("i", "y")
	h.shows("Installed ~/systemd/ajai.service; the daemon runs, and starts with your desktop session.", "systemd unit: enabled, active")
	if got := h.systemctlSince(before); !slices.Contains(got, "daemon-reload") || !slices.Contains(got, "enable ajai.service") {
		t.Errorf("install ran %q", got)
	}
	if !strings.Contains(h.logs.String(), "daemon unit installed from the TUI") {
		t.Errorf("not logged: %s", h.logs)
	}

	h.press("i")
	h.shows("Reinstall the daemon's unit for this ajai and restart it?")
	h.press("esc")

	before = len(h.systemd.calls)
	h.press("t", "y")
	h.shows("Stopped the daemon.")
	h.press("s", "y")
	h.shows("Started the daemon.")
	if got := h.systemctlSince(before); !slices.Equal(got, []string{"stop ajai.service", "start ajai.service"}) {
		t.Errorf("ran %q", got)
	}

	before = len(h.systemd.calls)
	h.press("u")
	h.shows("Stop the daemon and remove its unit? Schedule windows stop opening runs.")
	h.press("y")
	h.shows("Removed the daemon's unit.", "systemd unit: not installed")
	if got := h.systemctlSince(before); !slices.Equal(got, []string{"disable --now ajai.service", "daemon-reload"}) {
		t.Errorf("uninstall ran %q", got)
	}
}

func TestDaemonFailuresAndWarningsAreSaid(t *testing.T) {
	h := newHarness(t)
	h.systemd.answers["is-active graphical-session.target"] = "inactive" // no desktop session: enabled, not started
	h.open(100, 30)
	h.scheduleTab()
	h.press("i", "y")
	h.shows("Installed ~/systemd/ajai.service, but the daemon did not start. Your session has not reached", // the warning, capitalised
		"graphical-session.target")

	h.systemd.fail = "start ajai.service"
	h.press("s", "y")
	h.shows("Could not start the daemon: systemctl --user start ajai.service: exit status 1")
	if !strings.Contains(h.logs.String(), `"msg":"could not start the daemon from the TUI"`) {
		t.Errorf("a failure is logged too: %s", h.logs)
	}

	h.systemd.fail, h.systemd.answers["is-active ajai.service"] = "", "failed" // it starts, then exits
	h.press("s", "y")
	h.shows("Could not start the daemon: the daemon did not stay up")
}

func TestTheUnitIsReadOneReadAtATimeAndAskedOnlyOnceKnown(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	m := h.m.(Model)
	m.tab = tabSchedule
	m, first := m.loadTab()
	m, second := m.loadTab() // shown again before the first read came back
	if first == nil || !m.sched.daemon.reading {
		t.Fatal("the first read is under way")
	}
	var msgs []tea.Msg
	switch msg := second().(type) { // tea.Batch of one command is that command
	case tea.BatchMsg:
		for _, c := range msg {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
	default:
		msgs = append(msgs, msg)
	}
	for _, msg := range msgs {
		if _, isRead := msg.(unitReadMsg); isRead {
			t.Fatal("a second read while one is under way")
		}
	}
	m.sched = m.sched.resized(100, 26)
	m.sched, _ = m.sched.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")}, h.deps())
	if m.sched.daemon.editing() || !strings.Contains(m.sched.daemon.notice.text, "Still reading the unit's state") {
		t.Errorf("no question before the unit is known: %+v", m.sched.daemon)
	}
}

func TestTheDaemonProcessShowsAndTheUnitIsReadEveryFewSeconds(t *testing.T) {
	h := newHarness(t)
	d, err := daemon.AcquireLock(h.p.DataDir, h.now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Release()
	h.open(100, 30)
	h.scheduleTab()
	h.shows("Daemon  running since Mon 05 Oct 08:30")

	reads := func() int { return strings.Count(strings.Join(h.systemd.calls, "\n"), "is-enabled ajai.service") }
	if _, err := h.deps().Unit.Install(); err != nil { // installed elsewhere: `ajai daemon install`
		t.Fatal(err)
	}
	start := reads()
	for range unitEvery - 1 {
		h.refresh()
	}
	h.refresh()
	h.refresh()
	if got := reads() - start; got < 1 || got > 2 {
		t.Errorf("read %d times in %d refreshes", got, unitEvery+1)
	}
	h.shows("systemd unit: enabled, active")
}
