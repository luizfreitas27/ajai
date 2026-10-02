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
	"log/slog"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/doctor"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/store"
)

// Deps is what the TUI works on.
type Deps struct {
	Paths    paths.Paths
	Store    *store.Store
	Env      config.Env // the process environment: secrets and AJA_CFG_ overrides
	Logger   *slog.Logger
	Now      func() time.Time
	Probe    doctor.Probe // the machine, as `aja doctor` looks at it
	Launcher Launcher
	Home     string // the user's home directory, shown as ~
}

// tilde writes the home directory in text as ~: in ~/… and on its own, never
// inside a longer name (/home/ana2 stays as it is for /home/ana).
func (d Deps) tilde(text string) string {
	if d.Home == "" {
		return text
	}
	var b strings.Builder
	for {
		i := strings.Index(text, d.Home)
		if i < 0 {
			return b.String() + text
		}
		rest := text[i+len(d.Home):]
		b.WriteString(text[:i])
		if rest == "" || strings.ContainsRune("/ ,;:)", rune(rest[0])) {
			b.WriteString("~")
		} else {
			b.WriteString(d.Home)
		}
		text = rest
	}
}

// Launcher runs aja itself.
type Launcher interface {
	// InTerminal runs the aja subcommand args in the terminal, the TUI
	// stepping aside until it ends; done turns its outcome into a message.
	InTerminal(args []string, done func(error) tea.Msg) tea.Cmd
	// Start runs aja args in a process of its own, which outlives the TUI;
	// exited gets its end if it comes while the TUI runs.
	Start(args []string) (pid int, exited <-chan error, err error)
}

func (d Deps) logger() *slog.Logger {
	if d.Logger == nil {
		return logging.Discard
	}
	return d.Logger
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

const (
	tabDashboard tab = iota
	tabConfig
)

var tabNames = []string{"Dashboard", "Config"}

// Model is the TUI.
type Model struct {
	deps      Deps
	styles    styles
	tickAfter func(time.Duration, func(time.Time) tea.Msg) tea.Cmd // tea.Tick; tests send ticks themselves

	width, height int
	tab           tab
	help          bool
	setupShown    bool // the setup checklist covers the tabs
	setupOpened   bool // it has opened by itself once
	doctorBusy    bool // the checks are running
	doctorAgain   bool // and must run again once they end
	refreshes     int
	dash          dashboard
	runs          runControl
	conf          configTab
	checks        checklist
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

// Init reads what the Dashboard shows and runs the setup checks (Update sees
// doctorBusy false then, so a request made meanwhile only asks for one more run).
func (m Model) Init() tea.Cmd { return tea.Batch(m.dash.refresh(m.deps, nil), checkAgain) }

// checkAgain runs the setup checks, or, while they run, asks for one more run
// once they end: two never overlap, so an older report never wins.
func (m Model) checkAgain() (Model, tea.Cmd) {
	if m.doctorBusy {
		m.doctorAgain = true
		return m, nil
	}
	m.doctorBusy = true
	return m, runDoctor(m.deps)
}

// Update handles keys, the terminal's size and the refresh cycle: a refresh
// reads, and the next one is scheduled only once its result is in, so two never
// overlap. While a tab edits a value, every key but Ctrl-C goes to it.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.conf = m.conf.resized(m.width, m.bodyHeight())
	case tea.KeyMsg:
		return m.key(msg)
	case tickMsg:
		return m, m.dash.refresh(m.deps, m.runs.started)
	case refreshedMsg:
		before := m.dash.snap.activity
		m.dash = m.dash.apply(msg)
		m.runs = m.runs.observe(before, m.dash.snap.activity, msg.startExit, m.dash.snap.runs, m.deps.Now())
		next := m.tickAfter(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
		if m.refreshes++; m.refreshes%doctorEvery == 0 {
			var check tea.Cmd
			m, check = m.checkAgain()
			return m, tea.Batch(next, check)
		}
		return m, next
	case configSavedMsg:
		var cmd, check tea.Cmd
		m.conf, cmd = m.conf.update(msg, m.deps)
		if msg.refusal == "" { // a setting or a secret changed: so may the checks
			m, check = m.checkAgain()
		}
		return m, tea.Batch(cmd, check)
	case configLoadedMsg:
		var cmd tea.Cmd
		m.conf, cmd = m.conf.update(msg, m.deps)
		return m, cmd
	case checkAgainMsg:
		return m.checkAgain()
	case doctorMsg:
		m.checks, _ = m.checks.update(msg, m.deps)
		m.doctorBusy = false
		// Something a run needs is missing: open on the checklist, once, never
		// over a value being typed.
		if !m.setupOpened && blocking(msg.report) && !m.conf.editing() && !m.runs.editing() {
			m.setupOpened, m.setupShown, m.help = true, true, false
		}
		if m.doctorAgain {
			m.doctorAgain = false
			return m.checkAgain()
		}
		return m, nil
	case subcommandDoneMsg:
		var cmd tea.Cmd
		m.checks, cmd = m.checks.update(msg, m.deps)
		return m, tea.Batch(cmd, loadConfig(m.deps)) // the subcommand may have written the config
	case openConfigMsg:
		m.setupShown, m.tab = false, tabConfig
		return m, loadConfig(m.deps)
	case runStartedMsg, stopAskedMsg:
		var cmd tea.Cmd
		m.runs, cmd = m.runs.update(msg, m.deps)
		return m, cmd
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch {
	case m.setupShown && m.checks.editing():
		var cmd tea.Cmd
		m.checks, cmd = m.checks.update(msg, m.deps)
		return m, cmd
	case !m.setupShown && m.tab == tabConfig && m.conf.editing():
		var cmd tea.Cmd
		m.conf, cmd = m.conf.update(msg, m.deps)
		return m, cmd
	case !m.setupShown && m.tab == tabDashboard && m.runs.editing():
		var cmd tea.Cmd
		m.runs, cmd = m.runs.update(msg, m.deps)
		return m, cmd
	}
	switch key {
	case "q":
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "esc":
		switch {
		case m.help:
			m.help = false
		case m.setupShown && !m.checks.noConfig:
			m.setupShown = false
		}
		return m, nil
	case "s":
		if !m.setupShown && m.tab == tabDashboard {
			m.setupShown, m.help = true, false
			return m.checkAgain()
		}
	case "r":
		if !m.setupShown && !m.help && m.tab == tabDashboard {
			m.runs = m.runs.start()
			return m, nil
		}
	case "x":
		if !m.setupShown && !m.help && m.tab == tabDashboard {
			m.runs = m.runs.stop(m.dash.snap.activity)
			return m, nil
		}
	}
	if len(key) == 1 && key[0] >= '1' && int(key[0]-'1') < len(tabNames) {
		t := tab(key[0] - '1')
		if m.checks.noConfig && t != tabConfig { // nothing else works without a config
			m.tab, m.setupShown, m.help = t, true, false // shown once the checklist is left
			return m, nil
		}
		m.tab, m.help, m.setupShown = t, false, false
		if m.tab == tabConfig {
			return m, loadConfig(m.deps) // the file may have changed meanwhile
		}
		return m, nil
	}
	switch {
	case m.help:
	case m.setupShown:
		var cmd tea.Cmd
		m.checks, cmd = m.checks.update(msg, m.deps)
		return m, cmd
	case m.tab == tabConfig:
		var cmd tea.Cmd
		m.conf, cmd = m.conf.update(msg, m.deps)
		return m, cmd
	}
	return m, nil
}

// bodyHeight is the room between the tab bar and the key hints.
func (m Model) bodyHeight() int { return m.height - 4 } // tab bar, two rules, key hints

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
	height := m.bodyHeight()
	var body string
	switch {
	case m.help:
		body = m.helpView()
	case m.setupShown:
		body = m.checks.view(m.styles, m.deps, m.width)
	case m.tab == tabConfig:
		body = m.conf.view(m.styles, m.deps, m.width)
	default:
		top := m.runs.lines(m.styles, m.width)
		body = strings.Join(append(top, m.dash.view(m.styles, m.width, height-len(top), m.deps.Now(), m.checks.summary())), "\n")
	}
	return strings.Join([]string{m.tabBar(), rule, fit(body, m.width, height), rule, m.hints()}, "\n")
}

func (m Model) tabBar() string {
	parts := []string{m.styles.brand.Render("aja")}
	if m.setupShown {
		parts = append(parts, m.styles.activeTab.Render("Setup"))
	}
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if tab(i) == m.tab && !m.help && !m.setupShown {
			parts = append(parts, m.styles.activeTab.Render(label))
		} else {
			parts = append(parts, m.styles.tab.Render(label))
		}
	}
	return strings.Join(parts, "   ")
}

func (m Model) hints() string {
	var hint string
	switch {
	case m.help:
		hint = "esc closes the list · q quit"
	case m.setupShown && m.checks.editing():
		hint = "enter imports · esc cancels"
	case m.setupShown && m.checks.noConfig:
		hint = "↑↓ move · enter fix · i import · r check again · 2 config · q quit"
	case m.setupShown:
		hint = "↑↓ move · enter fix · i import · r check again · esc back · q quit"
	case m.tab == tabConfig && m.conf.editing():
		hint = "enter saves · esc cancels"
	case m.tab == tabConfig:
		hint = "↑↓ move · enter edit · ? keys · q quit"
	case m.runs.form != nil:
		hint = "↑↓ field · ←→ change · enter starts · esc cancels"
	case m.runs.confirm != nil:
		hint = "y stops the run · n keeps it"
	default:
		hint = "r run · x stop · s setup · ? keys · q quit"
	}
	return m.styles.dim.Render(hint)
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
	lines = append(lines, "", m.styles.section.Render("Dashboard"), "")
	row("r", "start a run: market, dry run or live, a maximum")
	row("x", "stop the run in progress, whoever started it")
	row("s", "the setup checklist")
	lines = append(lines, "", m.styles.section.Render("Setup checklist"), "")
	row("Enter", "fix the selected check: the init wizard, LinkedIn sign-in, or the Config tab")
	row("i", "import from the Python bot")
	row("r", "check again")
	row("Esc", "back to the tabs (once there is a config file)")
	lines = append(lines, "", m.styles.section.Render("Config"), "")
	row("↑ ↓, k j", "move; PgUp PgDn, g G jump")
	row("Enter", "edit the setting or secret, then Enter saves")
	row("Esc", "cancel the edit")
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
