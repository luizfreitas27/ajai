package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/service"
)

// fakeSystemd replaces systemctl, the executable path and the settle wait for
// one test. Its user manager keeps units under configHome.
func fakeSystemd(t *testing.T, configHome string, answers map[string]string, fail string) *[]string {
	t.Helper()
	var calls []string
	savedCtl, savedExe, savedSettle := systemctl, executable, unitSettle
	t.Cleanup(func() { systemctl, executable, unitSettle = savedCtl, savedExe, savedSettle })
	all := map[string]string{"show-environment": "HOME=/home/u\nXDG_CONFIG_HOME=" + configHome + "\nWAYLAND_DISPLAY=wayland-1",
		"is-active graphical-session.target": "active", "is-enabled aja.service": "enabled", "is-active aja.service": "active"}
	for k, v := range answers {
		all[k] = v
	}
	systemctl = func(args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		calls = append(calls, cmd)
		if cmd == fail {
			return "", errors.New("exit status 1: Failed to enable unit")
		}
		return all[cmd], nil
	}
	executable = func() (string, error) { return "/usr/local/bin/aja", nil }
	unitSettle = time.Millisecond
	return &calls
}

func TestDaemonInstallStatusAndUninstall(t *testing.T) {
	h := newHarness(t)
	configHome := t.TempDir()
	h.env["XDG_DATA_HOME"] = "relative/data" // pinned into the unit, made absolute
	calls := fakeSystemd(t, configHome, map[string]string{"is-active aja.service": "inactive"}, "")
	unit := filepath.Join(configHome, "systemd", "user", "aja.service")

	if _, out, _ := h.run("daemon", "status"); !strings.Contains(out, "not installed (aja daemon install)") || !strings.Contains(out, "not running") {
		t.Errorf("%s", out)
	}
	fakeSystemd(t, configHome, nil, "") // once started, the unit is active
	code, out, errText := h.run("daemon", "install")
	if code != 0 || !strings.Contains(out, "wrote") || !strings.Contains(out, unit) || !strings.Contains(out, "ExecStart=/usr/local/bin/aja daemon") ||
		!strings.Contains(out, "ran: systemctl --user restart aja.service") || !strings.Contains(out, "started") {
		t.Fatalf("%d %s %s", code, out, errText)
	}
	data, _ := os.ReadFile(unit)
	abs, _ := filepath.Abs("relative/data")
	if !strings.Contains(string(data), "Environment=AJA_HOME="+h.home) || !strings.Contains(string(data), "Environment=XDG_DATA_HOME="+abs) {
		t.Errorf("the unit finds the same config and data as this shell:\n%s", data)
	}
	if _, out, _ := h.run("daemon", "status"); !strings.Contains(out, "enabled, active ("+unit+")") {
		t.Errorf("%s", out)
	}
	_ = calls
	calls = fakeSystemd(t, configHome, nil, "")
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

func TestDaemonInstallRefusesBesideAHandStartedDaemonAndReportsFailures(t *testing.T) {
	h := newHarness(t)
	configHome := t.TempDir()
	fakeSystemd(t, configHome, map[string]string{"is-active aja.service": "inactive"}, "")
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := daemon.AcquireLock(h.home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code, _, errText := h.run("daemon", "install"); code == 0 || !strings.Contains(errText, "aja daemon is already running outside systemd") {
		t.Errorf("%d %s", code, errText)
	}
	lock.Release()

	cfg := filepath.Join(t.TempDir(), "my config.yaml")
	fakeSystemd(t, configHome, nil, "")
	if code, _, errText := h.run("--config", cfg, "daemon", "install"); code != 0 {
		t.Fatalf("%s", errText)
	}
	data, _ := os.ReadFile(filepath.Join(configHome, "systemd", "user", "aja.service"))
	if !strings.Contains(string(data), `daemon --config "`+cfg+`"`) {
		t.Errorf("%s", data)
	}

	fakeSystemd(t, configHome, nil, "enable "+service.UnitName)
	if code, _, errText := h.run("daemon", "install"); code == 0 || !strings.Contains(errText, "systemctl --user enable aja.service: exit status 1") {
		t.Errorf("%d %s", code, errText)
	}
}

func TestASecondDaemonExitsWithTheStatusTheUnitDoesNotRetry(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := daemon.AcquireLock(h.home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if code, _, errText := h.run("daemon"); code != service.ExitAlreadyRunning || !strings.Contains(errText, "already running") {
		t.Errorf("%d %s", code, errText)
	}
}
