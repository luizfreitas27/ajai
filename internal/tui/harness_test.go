package tui

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden screens in testdata")

// harness drives the TUI the way Bubble Tea would, synchronously: messages go
// to Update and the commands it returns run at once, their messages fed back.
// Ticks never fire by themselves; refresh sends one. Everything lives in a
// temporary AJA_HOME with a real store, and the clock is h.now.
type harness struct {
	t    *testing.T
	m    tea.Model
	p    paths.Paths
	st   *store.Store
	now  time.Time
	quit bool
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
	h := &harness{t: t, p: p, now: time.Date(2026, 10, 5, 9, 30, 0, 0, time.Local)}
	st, err := store.Open(p.DBFile)
	if err != nil {
		t.Fatal(err)
	}
	h.st = st.WithClock(func() time.Time { return h.now })
	t.Cleanup(func() { _ = st.Close() })
	return h
}

// open shows the TUI on a width×height terminal, colour off.
func (h *harness) open(width, height int) {
	h.t.Helper()
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	m := New(Deps{Paths: h.p, Store: h.st, Now: func() time.Time { return h.now }}, r)
	m.every = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
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
	special := map[string]tea.KeyType{"esc": tea.KeyEsc, "enter": tea.KeyEnter, "ctrl+c": tea.KeyCtrlC, "tab": tea.KeyTab,
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
	screen := h.screen()
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
