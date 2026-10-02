package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/daemon"
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
	h.shows("Installed ~/systemd/aja.service; the daemon runs, and starts with your desktop session.", "systemd unit: enabled, active")
	if got := h.systemctlSince(before); !slices.Contains(got, "daemon-reload") || !slices.Contains(got, "enable aja.service") {
		t.Errorf("install ran %q", got)
	}
	if !strings.Contains(h.logs.String(), "daemon unit installed from the TUI") {
		t.Errorf("not logged: %s", h.logs)
	}

	h.press("i")
	h.shows("Reinstall the daemon's unit for this aja and restart it?")
	h.press("esc")

	before = len(h.systemd.calls)
	h.press("t", "y")
	h.shows("Stopped the daemon.")
	h.press("s", "y")
	h.shows("Started the daemon.")
	if got := h.systemctlSince(before); !slices.Equal(got, []string{"stop aja.service", "start aja.service"}) {
		t.Errorf("ran %q", got)
	}

	before = len(h.systemd.calls)
	h.press("u")
	h.shows("Stop the daemon and remove its unit? Schedule windows stop opening runs.")
	h.press("y")
	h.shows("Removed the daemon's unit; the daemon is stopped.", "systemd unit: not installed")
	if got := h.systemctlSince(before); !slices.Equal(got, []string{"disable --now aja.service", "daemon-reload"}) {
		t.Errorf("uninstall ran %q", got)
	}
}

func TestDaemonFailuresAndWarningsAreSaid(t *testing.T) {
	h := newHarness(t)
	h.systemd.answers["is-active graphical-session.target"] = "inactive" // no desktop session: enabled, not started
	h.open(100, 30)
	h.scheduleTab()
	h.press("i", "y")
	h.shows("Installed ~/systemd/aja.service, but the daemon did not start.")

	h.systemd.fail = "start aja.service"
	h.press("s", "y")
	h.shows("Could not start the daemon: systemctl --user start aja.service: exit status 1")
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

	reads := func() int { return strings.Count(strings.Join(h.systemd.calls, "\n"), "is-enabled aja.service") }
	if _, err := h.deps().Service.Install(); err != nil { // installed elsewhere: `aja daemon install`
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
