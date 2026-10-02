// Package tui is the TUI: the full-screen terminal interface `aja` opens with
// no command. It reads the files and the database the subcommands use, every
// second, and acts through the same packages they do. A Model is built from
// Deps; New gives one ready for Run, and tests drive it with messages.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/store"
)

// Deps is what the TUI works on.
type Deps struct {
	Paths paths.Paths
	Store *store.Store
	Now   func() time.Time
}

// The smallest terminal the TUI lays out in.
const (
	minWidth  = 80
	minHeight = 24
)

// refreshEvery is how often the TUI re-reads what it shows.
const refreshEvery = time.Second

// tab is an index into tabNames, the tabs in the order of their number keys.
type tab int

var tabNames = []string{"Dashboard"}

// Model is the TUI.
type Model struct {
	deps      Deps
	styles    styles
	tickAfter func(time.Duration, func(time.Time) tea.Msg) tea.Cmd // tea.Tick; tests send ticks themselves

	width, height int
	tab           tab
	help          bool
	dash          dashboard
}

// New builds the TUI; r renders its colours (see Run).
func New(d Deps, r *lipgloss.Renderer) Model {
	return Model{deps: d, styles: newStyles(r), tickAfter: tea.Tick}
}

// Run shows the TUI on the terminal in and out until the user quits or ctx
// ends. noColor renders it without colour.
func Run(ctx context.Context, d Deps, in io.Reader, out io.Writer, noColor bool) error {
	r := lipgloss.NewRenderer(out)
	if noColor {
		r.SetColorProfile(termenv.Ascii)
	}
	_, err := tea.NewProgram(New(d, r), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

type tickMsg struct{}

// Init reads what the Dashboard shows.
func (m Model) Init() tea.Cmd { return m.dash.refresh(m.deps) }

// Update handles keys, the terminal's size and the refresh cycle: a refresh
// reads, and the next one is scheduled only once its result is in, so two never
// overlap.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch key := msg.String(); key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.help = !m.help
		case "esc":
			m.help = false
		default:
			if len(key) == 1 && key[0] >= '1' && int(key[0]-'1') < len(tabNames) {
				m.tab, m.help = tab(key[0]-'1'), false
			}
		}
	case tickMsg:
		return m, m.dash.refresh(m.deps)
	case refreshedMsg:
		m.dash = m.dash.apply(msg)
		return m, m.tickAfter(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
	}
	return m, nil
}

// View lays out the tab bar, the screen and the key hints.
func (m Model) View() string {
	if m.width == 0 {
		return "" // the size is not known yet
	}
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("aja needs a terminal of at least %d×%d; this one is %d×%d.\nMake it larger, or press q to quit.",
			minWidth, minHeight, m.width, m.height)
	}
	rule := m.styles.dim.Render(strings.Repeat("─", m.width))
	height := m.height - 4 // tab bar, two rules, key hints
	body := m.dash.view(m.styles, m.width, height, m.deps.Now())
	if m.help {
		body = m.helpView()
	}
	return strings.Join([]string{m.tabBar(), rule, fit(body, m.width, height), rule, m.hints()}, "\n")
}

func (m Model) tabBar() string {
	parts := []string{m.styles.brand.Render("aja")}
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if tab(i) == m.tab && !m.help {
			parts = append(parts, m.styles.activeTab.Render(label))
		} else {
			parts = append(parts, m.styles.tab.Render(label))
		}
	}
	return strings.Join(parts, "   ")
}

func (m Model) hints() string {
	return m.styles.dim.Render("? keys · q quit")
}

func (m Model) helpView() string {
	lines := []string{m.styles.section.Render("Keys"), ""}
	row := func(key, what string) {
		lines = append(lines, "  "+m.styles.key.Render(fmt.Sprintf("%-11s", key))+" "+what)
	}
	for i, name := range tabNames {
		row(fmt.Sprint(i+1), name)
	}
	row("?", "this list; Esc closes it")
	row("q, Ctrl-C", "quit (a run in progress keeps running)")
	return strings.Join(lines, "\n")
}

// fit cuts or pads text to exactly height lines of at most width cells.
func fit(text string, width, height int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
