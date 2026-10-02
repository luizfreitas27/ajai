package tui

import (
	"bytes"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/doctor"
	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden screens in testdata")

// harness drives the TUI the way Bubble Tea would, synchronously: messages go
// to Update and the commands it returns run at once, their messages fed back.
// Ticks never fire by themselves; refresh sends one. Everything lives in a
// temporary AJA_HOME with a real store and, to start with, a valid config; the
// clock is h.now, the environment h.env, and the log goes to h.logs. The
// machine is a fake: Chrome is found, the LinkedIn session is h.session, and
// the aja subcommands run in the terminal are recorded in h.ran (and acted out
// by h.onRun).
type harness struct {
	t       *testing.T
	m       tea.Model
	p       paths.Paths
	st      *store.Store
	now     time.Time
	env     map[string]string
	logs    *bytes.Buffer
	session bool
	ran     [][]string
	onRun   func(args []string)
	quit    bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	p := paths.Resolve("", func(k string) string {
		if k == "AJA_HOME" {
			return dir
		}
		return ""
	})
	if err := p.EnsureDataDirs(); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, p: p, now: time.Date(2026, 10, 5, 9, 30, 0, 0, time.Local), env: map[string]string{}, logs: &bytes.Buffer{}, session: true}
	st, err := store.Open(p.DBFile)
	if err != nil {
		t.Fatal(err)
	}
	h.st = st.WithClock(func() time.Time { return h.now })
	t.Cleanup(func() { _ = st.Close() })
	h.writeConfig()
	if err := os.MkdirAll(p.ChromeProfileDir, 0o700); err != nil { // as `aja login` leaves it
		t.Fatal(err)
	}
	return h
}

// writeConfig writes a valid config file and its resume, as `aja init` would.
func (h *harness) writeConfig() {
	h.t.Helper()
	resume := filepath.Join(h.p.ResumeDir, "cv.pdf")
	if err := os.WriteFile(resume, []byte("%PDF"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	cfg := config.Defaults()
	err := cfg.ApplyEssentials(config.Essentials{FirstName: "Ana", LastName: "Silva", Terms: "Dev Python, QA", Location: "Brasil",
		Market: "brasil", DesiredSalary: 9000, Resume: resume})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := config.Write(h.p.ConfigFile, cfg); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) removeConfig() {
	h.t.Helper()
	if err := os.Remove(h.p.ConfigFile); err != nil {
		h.t.Fatal(err)
	}
}

// InTerminal is the fake launcher: it records args, acts them out, and ends.
func (h *harness) InTerminal(args []string, done func(error) tea.Msg) tea.Cmd {
	h.ran = append(h.ran, args)
	return func() tea.Msg {
		if h.onRun != nil {
			h.onRun(args)
		}
		return done(nil)
	}
}

func (h *harness) probe() doctor.Probe {
	return doctor.Probe{
		FindChrome:         func(string) (string, error) { return "/usr/bin/chromium", nil },
		ChromeVersion:      func(string) string { return "Chromium 152" },
		EverydayProfileDir: func(string) string { return "/home/you/.config/chromium" },
		HasLinkedInSession: func(string) (bool, error) { return h.session, nil },
	}
}

// open shows the TUI on a width×height terminal, colour off.
func (h *harness) open(width, height int) {
	h.t.Helper()
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	d := Deps{Paths: h.p, Store: h.st, Env: config.MapEnv(h.env), Logger: slog.New(slog.NewJSONHandler(h.logs, nil)),
		Now: func() time.Time { return h.now }, Probe: h.probe(), Launcher: h, Home: h.p.DataDir}
	m := New(d, r)
	m.tickAfter = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	h.m = m
	h.do(m.Init())
	h.send(tea.WindowSizeMsg{Width: width, Height: height})
}

func (h *harness) send(msg tea.Msg) {
	var cmd tea.Cmd
	h.m, cmd = h.m.Update(msg)
	h.do(cmd)
}

func (h *harness) do(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.do(c)
		}
	case tea.QuitMsg:
		h.quit = true
	default:
		h.send(msg)
	}
}

// press sends keys: names like "esc", "enter", "ctrl+c", or text typed as is.
func (h *harness) press(keys ...string) {
	special := map[string]tea.KeyType{"esc": tea.KeyEsc, "enter": tea.KeyEnter, "ctrl+c": tea.KeyCtrlC, "ctrl+u": tea.KeyCtrlU, "tab": tea.KeyTab,
		"backspace": tea.KeyBackspace, "pgdown": tea.KeyPgDown, "pgup": tea.KeyPgUp,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "space": tea.KeySpace}
	for _, k := range keys {
		if t, ok := special[k]; ok {
			h.send(tea.KeyMsg{Type: t})
			continue
		}
		h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
}

// refresh is the one-second tick.
func (h *harness) refresh() { h.send(tickMsg{}) }

func (h *harness) screen() string { return h.m.View() }

// shows fails unless the screen has every text.
func (h *harness) shows(texts ...string) {
	h.t.Helper()
	screen := h.screen()
	for _, text := range texts {
		if !strings.Contains(screen, text) {
			h.t.Errorf("the screen lacks %q:\n%s", text, screen)
		}
	}
}

// golden compares the screen with testdata/name.golden (go test -update rewrites it).
func (h *harness) golden(name string) {
	h.t.Helper()
	path := filepath.Join("testdata", name+".golden")
	screen := strings.ReplaceAll(h.screen(), h.p.DataDir, "$AJA_HOME") // a new temporary directory each run (most show as ~)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(screen), 0o644); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("%v (run go test ./internal/tui -update)", err)
	}
	if screen != string(want) {
		h.t.Errorf("the %s screen changed (go test ./internal/tui -update rewrites it):\n--- got\n%s\n--- want\n%s", name, screen, want)
	}
}
