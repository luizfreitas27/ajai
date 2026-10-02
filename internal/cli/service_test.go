package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/service"
)

// fakeService replaces systemctl and the executable path for one test.
func fakeService(t *testing.T, answers map[string]string, fail string) *[]string {
	t.Helper()
	var calls []string
	savedCtl, savedExe := systemctl, executable
	t.Cleanup(func() { systemctl, executable = savedCtl, savedExe })
	systemctl = func(args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		calls = append(calls, cmd)
		if cmd == fail {
			return "Failed to enable unit", errors.New("exit status 1")
		}
		return answers[cmd], nil
	}
	executable = func() (string, error) { return "/usr/local/bin/aja", nil }
	return &calls
}

func TestDaemonInstallStatusAndUninstall(t *testing.T) {
	h := newHarness(t)
	h.env["XDG_CONFIG_HOME"] = t.TempDir()
	calls := fakeService(t, map[string]string{
		"is-active graphical-session.target": "active", "show-environment": "WAYLAND_DISPLAY=wayland-1",
		"is-enabled aja.service": "enabled", "is-active aja.service": "active",
	}, "")
	unit := filepath.Join(h.env["XDG_CONFIG_HOME"], "systemd", "user", "aja.service")

	if _, out, _ := h.run("daemon", "status"); !strings.Contains(out, "not installed (aja daemon install)") || !strings.Contains(out, "not running") {
		t.Errorf("%s", out)
	}
	code, out, errText := h.run("daemon", "install")
	if code != 0 || !strings.Contains(out, "installed") || !strings.Contains(out, unit) || !strings.Contains(out, "ran: systemctl --user enable aja.service") {
		t.Fatalf("%d %s %s", code, out, errText)
	}
	data, _ := os.ReadFile(unit)
	if !strings.Contains(string(data), "ExecStart=/usr/local/bin/aja daemon") || !strings.Contains(string(data), "Environment=AJA_HOME="+h.home) {
		t.Errorf("the service runs this binary with this AJA_HOME:\n%s", data)
	}
	if _, out, _ := h.run("daemon", "status"); !strings.Contains(out, "enabled, active ("+unit+")") {
		t.Errorf("%s", out)
	}
	*calls = nil
	if code, out, _ := h.run("daemon", "uninstall"); code != 0 || !strings.Contains(out, "removed") || !strings.Contains(strings.Join(*calls, ","), "disable --now aja.service") {
		t.Errorf("%d %s %q", code, out, *calls)
	}
	if _, err := os.Stat(unit); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unit must be gone")
	}
	if code, out, _ := h.run("daemon", "uninstall"); code != 0 || !strings.Contains(out, "not installed") {
		t.Errorf("%d %s", code, out)
	}
}

func TestDaemonInstallPassesTheConfigFlagAndReportsFailures(t *testing.T) {
	h := newHarness(t)
	h.env["XDG_CONFIG_HOME"] = t.TempDir()
	fakeService(t, map[string]string{"is-active graphical-session.target": "inactive"}, "")
	cfg := filepath.Join(t.TempDir(), "my config.yaml")
	code, out, errText := h.run("--config", cfg, "daemon", "install")
	if code != 0 {
		t.Fatalf("%s", errText)
	}
	data, _ := os.ReadFile(filepath.Join(h.env["XDG_CONFIG_HOME"], "systemd", "user", "aja.service"))
	if !strings.Contains(string(data), `daemon --config "`+cfg+`"`) {
		t.Errorf("%s", data)
	}
	if !strings.Contains(out, "graphical-session.target") {
		t.Errorf("the warning must reach the user:\n%s", out)
	}

	fakeService(t, nil, "enable "+service.UnitName)
	if code, _, errText := h.run("daemon", "install"); code == 0 || !strings.Contains(errText, "systemctl --user enable aja.service") {
		t.Errorf("%d %s", code, errText)
	}
}
