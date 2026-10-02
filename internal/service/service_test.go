package service

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden unit files")

func TestTheUnitMatchesItsGoldenFile(t *testing.T) {
	cases := map[string]Options{
		"aja.service": {Binary: "/home/u/.local/bin/aja"},
		"aja-home-config.service": {Binary: "/opt/my apps/aja", ConfigFile: "/home/u/aja 100%/config.yaml",
			Env: map[string]string{"AJA_HOME": "/home/u/aja 100%", "XDG_DATA_HOME": "/data/u"}},
	}
	for name, o := range cases {
		got := Unit(o)
		path := filepath.Join("testdata", name)
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs (go test ./internal/service -update to accept):\n%s", name, got)
		}
	}
}

func TestQuoteForUnitFiles(t *testing.T) {
	cases := []struct{ in, value, exec string }{
		{"/usr/bin/aja", "/usr/bin/aja", "/usr/bin/aja"},
		{"/opt/my apps/aja", `"/opt/my apps/aja"`, `"/opt/my apps/aja"`},
		{"/a/50%", "/a/50%%", "/a/50%%"},
		{`/a/"b" c`, `"/a/\"b\" c"`, `"/a/\"b\" c"`},
		{"/a/$HOME", "/a/$HOME", "/a/$$HOME"},
		{"/a\nb", `"/a\nb"`, `"/a\nb"`},
	}
	for _, tc := range cases {
		if got := quote(tc.in); got != tc.value {
			t.Errorf("quote %q: %q, want %q", tc.in, got, tc.value)
		}
		if got := execQuote(tc.in); got != tc.exec {
			t.Errorf("execQuote %q: %q, want %q", tc.in, got, tc.exec)
		}
	}
}

func TestTheUnitDirIsWhereSystemdLooks(t *testing.T) {
	if got := UnitDir("HOME=/home/u\nXDG_CONFIG_HOME=/cfg/u", "/home/u"); got != "/cfg/u/systemd/user" {
		t.Errorf("%q", got)
	}
	for _, env := range []string{"HOME=/home/u", "XDG_CONFIG_HOME=relative", ""} {
		if got := UnitDir(env, "/home/u"); got != "/home/u/.config/systemd/user" {
			t.Errorf("%q: %q", env, got)
		}
	}
}

// fakeSystemctl records commands and answers from a script.
type fakeSystemctl struct {
	calls   []string
	answers map[string]string
	fail    string // a command that fails
}

func (f *fakeSystemctl) run(args ...string) (string, error) {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if cmd == f.fail {
		return "", errors.New("exit status 1: Failed to connect to bus")
	}
	return f.answers[cmd], nil
}

func desktop() map[string]string {
	return map[string]string{"is-active graphical-session.target": "active", "show-environment": "HOME=/home/u\nWAYLAND_DISPLAY=wayland-1",
		"is-active aja.service": "active", "is-enabled aja.service": "enabled"}
}

func TestInstallWritesEnablesStartsAndChecksTheDaemonStaysUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "systemd", "user")
	f := &fakeSystemctl{answers: desktop()}
	m := Manager{UnitDir: dir, Systemctl: f.run, Settle: time.Millisecond}
	result, err := m.Install(Options{Binary: "/usr/bin/aja"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "aja.service"))
	if string(data) != result.Unit || !strings.Contains(result.Unit, "ExecStart=/usr/bin/aja daemon") || !strings.Contains(result.Unit, "KillMode=mixed") {
		t.Errorf("%s", data)
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable aja.service", "systemctl --user restart aja.service"}
	if !slices.Equal(result.Commands, want) || !result.Started || len(result.Warnings) != 0 {
		t.Errorf("%+v", result)
	}
	// Again, after the binary moved: the unit is replaced, nothing fails.
	if _, err := m.Install(Options{Binary: "/home/u/bin/aja"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "aja.service")); !strings.Contains(string(data), "ExecStart=/home/u/bin/aja daemon") {
		t.Errorf("%s", data)
	}
}

func TestInstallOutsideADesktopSessionEnablesButDoesNotStart(t *testing.T) {
	answers := desktop()
	answers["is-active graphical-session.target"] = "inactive"
	answers["show-environment"] = "HOME=/home/u"
	f := &fakeSystemctl{answers: answers}
	m := Manager{UnitDir: t.TempDir(), Systemctl: f.run, Settle: time.Millisecond}
	result, err := m.Install(Options{Binary: "/tmp/go-build123/b001/exe/aja"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Started || slices.Contains(f.calls, "restart aja.service") {
		t.Errorf("not started outside a desktop session: %+v", result)
	}
	joined := strings.Join(result.Warnings, "\n")
	for _, want := range []string{"temporary build", "graphical-session.target", "exec-once = systemctl --user start aja.service", "import-environment WAYLAND_DISPLAY DISPLAY"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, result.Warnings)
		}
	}
}

func TestInstallReportsAFailingCommandOrADaemonThatDoesNotStayUp(t *testing.T) {
	f := &fakeSystemctl{answers: desktop(), fail: "enable aja.service"}
	m := Manager{UnitDir: t.TempDir(), Systemctl: f.run, Settle: time.Millisecond}
	if _, err := m.Install(Options{Binary: "/usr/bin/aja"}); err == nil || !strings.Contains(err.Error(), "systemctl --user enable aja.service: exit status 1: Failed to connect to bus") {
		t.Errorf("%v", err)
	}
	if slices.Contains(f.calls, "restart aja.service") {
		t.Error("no restart after a failed enable")
	}

	answers := desktop()
	answers["is-active aja.service"] = "failed"
	f = &fakeSystemctl{answers: answers}
	m.Systemctl = f.run
	if _, err := m.Install(Options{Binary: "/usr/bin/aja"}); err == nil || !strings.Contains(err.Error(), "journalctl --user -u aja.service") {
		t.Errorf("%v", err)
	}
}

func TestUninstallAndStatus(t *testing.T) {
	f := &fakeSystemctl{answers: desktop()}
	m := Manager{UnitDir: t.TempDir(), Systemctl: f.run, Settle: time.Millisecond}
	if st := m.Status(); st.Installed {
		t.Errorf("%+v", st)
	}
	if _, err := m.Uninstall(); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("%v", err)
	}
	if _, err := m.Install(Options{Binary: "/usr/bin/aja"}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); !st.Installed || st.Enabled != "enabled" || st.Active != "active" {
		t.Errorf("%+v", st)
	}
	f.fail = "is-enabled aja.service" // no user bus (ssh, sudo): say so, not stderr as a state
	if st := m.Status(); !strings.HasPrefix(st.Enabled, "unknown (exit status 1: Failed to connect to bus") {
		t.Errorf("%+v", st)
	}
	f.fail, f.calls = "", nil
	commands, err := m.Uninstall()
	if err != nil || !slices.Equal(f.calls, []string{"disable --now aja.service", "daemon-reload"}) || len(commands) != 2 {
		t.Errorf("%v %q %q", err, f.calls, commands)
	}
	if _, err := os.Stat(m.UnitPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unit must be removed")
	}
}

func TestStartAndStopTheInstalledUnit(t *testing.T) {
	f := &fakeSystemctl{answers: desktop()}
	m := Manager{UnitDir: t.TempDir(), Systemctl: f.run, Settle: time.Millisecond}
	if _, err := m.Start(); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("nothing to start: %v", err)
	}
	if _, err := m.Install(Options{Binary: "/usr/bin/aja"}); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if ran, err := m.Stop(); err != nil || !slices.Equal(ran, []string{"systemctl --user stop aja.service"}) {
		t.Errorf("%q %v", ran, err)
	}
	f.fail = "start aja.service"
	if _, err := m.Start(); err == nil || !strings.Contains(err.Error(), "systemctl --user start aja.service") {
		t.Errorf("the failing command is named: %v", err)
	}
	if !slices.Equal(f.calls, []string{"stop aja.service", "start aja.service"}) {
		t.Errorf("%q", f.calls)
	}
}
