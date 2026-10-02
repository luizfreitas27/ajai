package service

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden unit files")

func TestTheUnitMatchesItsGoldenFile(t *testing.T) {
	cases := map[string]Options{
		"aja.service":             {Binary: "/home/u/.local/bin/aja"},
		"aja-home-config.service": {Binary: "/opt/my apps/aja", ConfigFile: "/home/u/aja 100%/config.yaml", Home: "/home/u/aja 100%"},
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
	cases := map[string]string{
		"/usr/bin/aja":     "/usr/bin/aja",
		"/opt/my apps/aja": `"/opt/my apps/aja"`,
		"/a/50%":           "/a/50%%",
		`/a/"b" c`:         `"/a/\"b\" c"`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
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
		return "boom", errors.New("exit status 1")
	}
	return f.answers[cmd], nil
}

func TestInstallWritesTheUnitThenEnablesAndRestartsIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "systemd", "user")
	f := &fakeSystemctl{answers: map[string]string{"is-active graphical-session.target": "active", "show-environment": "HOME=/home/u\nWAYLAND_DISPLAY=wayland-1"}}
	m := Manager{UnitDir: dir, Systemctl: f.run}
	in, err := m.Install(Options{Binary: "/usr/bin/aja"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "aja.service"))
	if !strings.Contains(string(data), "ExecStart=/usr/bin/aja daemon") || in.Path != filepath.Join(dir, "aja.service") {
		t.Errorf("%s", data)
	}
	want := []string{"daemon-reload", "enable aja.service", "restart aja.service"}
	if !slices.Equal(f.calls[:3], want) || len(in.Commands) != 3 || len(in.Warnings) != 0 {
		t.Errorf("%q %q %q", f.calls, in.Commands, in.Warnings)
	}
	// Again, after the binary moved: the unit is replaced, nothing fails.
	if _, err := m.Install(Options{Binary: "/home/u/bin/aja"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "aja.service")); !strings.Contains(string(data), "ExecStart=/home/u/bin/aja daemon") {
		t.Errorf("%s", data)
	}
}

func TestInstallWarnsWhenTheServiceCouldNotShowChrome(t *testing.T) {
	f := &fakeSystemctl{answers: map[string]string{"is-active graphical-session.target": "inactive", "show-environment": "HOME=/home/u"}}
	m := Manager{UnitDir: t.TempDir(), Systemctl: f.run}
	in, err := m.Install(Options{Binary: "/tmp/go-build123/b001/exe/aja"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(in.Warnings, "\n")
	for _, want := range []string{"temporary build", "graphical-session.target", "exec-once = systemctl --user start aja.service", "import-environment WAYLAND_DISPLAY DISPLAY"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, in.Warnings)
		}
	}
}

func TestInstallStopsAtTheFirstFailingCommand(t *testing.T) {
	f := &fakeSystemctl{fail: "enable aja.service"}
	m := Manager{UnitDir: t.TempDir(), Systemctl: f.run}
	if _, err := m.Install(Options{Binary: "/usr/bin/aja"}); err == nil || !strings.Contains(err.Error(), "systemctl --user enable aja.service") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("%v", err)
	}
	if slices.Contains(f.calls, "restart aja.service") {
		t.Error("no restart after a failed enable")
	}
}

func TestUninstallAndStatus(t *testing.T) {
	f := &fakeSystemctl{answers: map[string]string{"is-enabled aja.service": "enabled", "is-active aja.service": "active", "is-active graphical-session.target": "active", "show-environment": "DISPLAY=:0"}}
	m := Manager{UnitDir: t.TempDir(), Systemctl: f.run}
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
	f.calls = nil
	commands, err := m.Uninstall()
	if err != nil || !slices.Equal(f.calls, []string{"disable --now aja.service", "daemon-reload"}) || len(commands) != 2 {
		t.Errorf("%v %q %q", err, f.calls, commands)
	}
	if _, err := os.Stat(m.UnitPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unit must be removed")
	}
}
