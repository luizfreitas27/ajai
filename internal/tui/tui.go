// Package tui is the TUI: the full-screen terminal interface `ajai` opens with
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
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/doctor"
	"github.com/luizfreitas27/ajai/internal/logging"
	"github.com/luizfreitas27/ajai/internal/paths"
	"github.com/luizfreitas27/ajai/internal/store"
)

// Deps is what the TUI works on.
type Deps struct {
	Paths    paths.Paths
	Store    *store.Store
	Env      config.Env // the process environment: secrets and AJAI_CFG_ overrides
	Logger   *slog.Logger
	Now      func() time.Time
	Probe    doctor.Probe // the machine, as `ajai doctor` looks at it
	Launcher Launcher
	Unit     Unit               // the daemon's systemd unit; nil: not handled here
	OpenURL  func(string) error // opens a link in the default browser
	Home     string             // the user's home directory, shown as ~
}

// tilde writes the home directory in text as ~: in ~/… and on its own, never
// inside a longer name (/home/ana2 stays as it is for /home/ana).
func (d Deps) tilde(text string) string { return tildeUnder(d.Home, text) }

// tildeUnder writes home in text as ~ (see Deps.tilde); text as it is when
// home is "".
func tildeUnder(home, text string) string {
	if home == "" {
		return text
	}
	var b strings.Builder
	for {
		i := strings.Index(text, home)
		if i < 0 {
			return b.String() + text
		}
		rest := text[i+len(home):]
		b.WriteString(text[:i])
		if rest == "" || strings.ContainsRune("/ ,;:)", rune(rest[0])) {
			b.WriteString("~")
		} else {
			b.WriteString(home)
		}
		text = rest
	}
}

// Launcher runs ajai itself.
type Launcher interface {
	// InTerminal runs the ajai subcommand args in the terminal, the TUI
	// stepping aside until it ends; done turns its outcome into a message.
	InTerminal(args []string, done func(error) tea.Msg) tea.Cmd
	// Start runs ajai args in a process of its own, which outlives the TUI;
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
	tabAnswers
	tabSchedule
	tabJobs
	tabConfig
)

var tabNames = []string{"Dashboard", "Answers", "Schedule", "Jobs", "Config"}

// tabKey is the number key that shows t.
func tabKey(t tab) string { return fmt.Sprint(int(t) + 1) }

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
	ans           answersTab
	sched         scheduleTab
	jobs          jobsTab
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
		m.ans = m.ans.resized(m.width, m.bodyHeight())
		m.sched = m.sched.resized(m.width, m.bodyHeight())
		m.jobs = m.jobs.resized(m.width, m.bodyHeight())
		m.checks = m.checks.resized(m.width, m.bodyHeight(), m.deps)
	case tea.KeyMsg:
		return m.key(msg)
	case tickMsg:
		return m, m.dash.refresh(m.deps, m.runs.started)
	case refreshedMsg:
		before := m.dash.snap.activity
		m.dash = m.dash.apply(msg)
		m.runs = m.runs.observe(before, m.dash.snap.activity, msg.startExit, m.dash.snap.runs, m.deps.Now())
		next := m.tickAfter(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
		switch { // what other processes change: runs and Telegram the answers, `ajai schedule` the windows
		case m.setupShown:
		case m.tab == tabAnswers && !m.ans.editing():
			next = tea.Batch(next, loadAnswers(m.deps, m.ans.shownState()))
		case m.tab == tabSchedule && !m.sched.editing():
			next = tea.Batch(next, loadWindows(m.deps))
			if m.refreshes%unitEvery == 0 {
				var read tea.Cmd
				m.sched, read = m.sched.readUnit(m.deps)
				next = tea.Batch(next, read)
			}
		case m.tab == tabJobs && !m.jobs.editing() && m.refreshes%jobsEvery == 0: // a run records jobs as it goes
			var read tea.Cmd
			m.jobs, read = m.jobs.read(m.deps, readAgain)
			next = tea.Batch(next, read)
		}
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
	case answersLoadedMsg, answerChangedMsg:
		var cmd tea.Cmd
		m.ans, cmd = m.ans.update(msg, m.deps)
		return m, cmd
	case windowsLoadedMsg, windowChangedMsg, unitReadMsg, daemonDoneMsg:
		var cmd tea.Cmd
		m.sched, cmd = m.sched.update(msg, m.deps)
		return m, cmd
	case jobsLoadedMsg, jobOpenedMsg:
		var cmd tea.Cmd
		m.jobs, cmd = m.jobs.update(msg, m.deps)
		return m, cmd
	case checkAgainMsg:
		return m.checkAgain()
	case doctorMsg:
		m.checks, _ = m.checks.update(msg, m.deps)
		m.doctorBusy = false
		// Something a run needs is missing: open on the checklist, once, never
		// over a value being typed.
		if !m.setupOpened && blocking(msg.report) && !m.typing() {
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
		return m.loadTab()
	case runStartedMsg, stopAskedMsg:
		var cmd tea.Cmd
		m.runs, cmd = m.runs.update(msg, m.deps)
		return m, cmd
	}
	return m, nil
}

// typing reports whether a tab holds a value being typed or a question asked,
// which every key but Ctrl-C goes to.
func (m Model) typing() bool {
	return m.conf.editing() || m.runs.editing() || m.ans.editing() || m.sched.editing() || m.jobs.editing() || m.checks.editing()
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
	case !m.setupShown && m.tab == tabAnswers && m.ans.editing():
		var cmd tea.Cmd
		m.ans, cmd = m.ans.update(msg, m.deps)
		return m, cmd
	case !m.setupShown && m.tab == tabSchedule && m.sched.editing():
		var cmd tea.Cmd
		m.sched, cmd = m.sched.update(msg, m.deps)
		return m, cmd
	case !m.setupShown && m.tab == tabJobs && m.jobs.editing():
		var cmd tea.Cmd
		m.jobs, cmd = m.jobs.update(msg, m.deps)
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
			return m, nil
		case m.setupShown:
			if !m.checks.noConfig {
				m.setupShown = false
				return m.loadTab() // it may never have loaded, or changed meanwhile
			}
			return m, nil
		} // otherwise the tab's: Jobs clears its search
	case "tab", "shift+tab": // the next tab, or the one before, round the bar
		step := 1
		if key == "shift+tab" {
			step = len(tabNames) - 1
		}
		return m.show(tab((int(m.tab) + step) % len(tabNames)))
	case "s":
		if !m.setupShown {
			m.setupShown, m.help = true, false
			return m.checkAgain()
		}
	case "r", "x": // a run is started and stopped on the Dashboard, from any tab
		if m.checks.noConfig { // no run without a config: the checklist says what to do
			return m.show(tabDashboard)
		}
		if !m.setupShown {
			m.tab, m.help = tabDashboard, false
			if key == "r" {
				m.runs = m.runs.start()
			} else {
				m.runs = m.runs.stop(m.dash.snap.activity)
			}
			return m.loadTab()
		}
	}
	if len(key) == 1 && key[0] >= '1' && int(key[0]-'1') < len(tabNames) {
		return m.show(tab(key[0] - '1'))
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
	case m.tab == tabAnswers:
		var cmd tea.Cmd
		m.ans, cmd = m.ans.update(msg, m.deps)
		return m, cmd
	case m.tab == tabSchedule:
		var cmd tea.Cmd
		m.sched, cmd = m.sched.update(msg, m.deps)
		return m, cmd
	case m.tab == tabJobs:
		var cmd tea.Cmd
		m.jobs, cmd = m.jobs.update(msg, m.deps)
		return m, cmd
	}
	return m, nil
}

// show shows tab t, leaving the help and the checklist; without a config
// file only the Config tab is there, the checklist in front of the others.
func (m Model) show(t tab) (Model, tea.Cmd) {
	if m.checks.noConfig && t != tabConfig { // nothing else works without a config
		m.tab, m.setupShown, m.help = t, true, false // shown once the checklist is left
		return m, nil
	}
	m.tab, m.help, m.setupShown = t, false, false
	return m.loadTab()
}

// loadTab reads what the tab shown displays: it may have changed since it was
// last shown, or never have been read.
func (m Model) loadTab() (Model, tea.Cmd) {
	switch m.tab {
	case tabConfig:
		return m, loadConfig(m.deps)
	case tabAnswers:
		return m, loadAnswers(m.deps, m.ans.shownState())
	case tabSchedule:
		var read tea.Cmd
		m.sched, read = m.sched.readUnit(m.deps)
		return m, tea.Batch(loadWindows(m.deps), read)
	case tabJobs:
		var read tea.Cmd
		m.jobs, read = m.jobs.read(m.deps, readAgain)
		return m, read
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
		return fmt.Sprintf("ajai needs a terminal of at least %d×%d; this one is %d×%d.\nMake it larger, or press q to quit.",
			minWidth, minHeight, m.width, m.height)
	}
	rule := m.divider(0, 0)
	height := m.bodyHeight()
	var body string
	switch {
	case m.help:
		body = m.helpView()
	case m.setupShown:
		body = m.checks.view(m.styles, m.deps)
	case m.tab == tabConfig:
		body = m.conf.view(m.styles, m.width)
	case m.tab == tabAnswers:
		body = m.ans.view(m.styles, m.width)
	case m.tab == tabSchedule:
		body = m.sched.view(m.styles, m.width, m.deps.Now(), daemonProc{m.dash.snap.daemon, m.dash.snap.daemonUp})
	case m.tab == tabJobs:
		body = m.jobs.view(m.styles, m.deps, m.width)
	default:
		top := m.runs.lines(m.styles, m.width)
		body = strings.Join(append(top, m.dash.view(m.styles, m.width, height-len(top), m.deps.Now(), m.checks.summary())), "\n")
	}
	bar, start, width := m.tabBar()
	return strings.Join([]string{bar, m.divider(start, width), fit(body, m.width, height), rule, m.hints()}, "\n")
}

// tabGap separates the tab bar's labels.
const tabGap = "   "

// tabBar is the brand and the tabs, and where the active one's label sits:
// its first column and its width (0 when none is active, as on the help).
func (m Model) tabBar() (bar string, start, width int) {
	parts := []string{m.styles.brand.Render("ajai")}
	add := func(label string, active bool) {
		if !active {
			parts = append(parts, m.styles.tab.Render(label))
			return
		}
		start, width = lipgloss.Width(strings.Join(parts, tabGap)+tabGap), lipgloss.Width(label)
		parts = append(parts, m.styles.activeTab.Render(label))
	}
	if m.setupShown {
		add("Setup", true)
	}
	for i, name := range tabNames {
		label := tabKey(tab(i)) + " " + name
		if tab(i) == tabAnswers && m.dash.snap.pending > 0 {
			label += fmt.Sprintf(" (%d)", m.dash.snap.pending)
		}
		add(label, tab(i) == m.tab && !m.help && !m.setupShown)
	}
	return strings.Join(parts, tabGap), start, width
}

// divider is a full-width rule with a flame stroke over the width columns from
// start: under the tab bar it marks the active tab's label, without colour too.
func (m Model) divider(start, width int) string {
	start, width = min(start, m.width), min(width, max(0, m.width-start))
	return m.styles.dim.Render(strings.Repeat("─", start)) + m.styles.stroke.Render(strings.Repeat("━", width)) +
		m.styles.dim.Render(strings.Repeat("─", m.width-start-width))
}

// binding is a key and what it does: in a word or two for the footer (none:
// the help screen only), in a sentence for the help screen.
type binding struct{ key, short, long string }

// The keys every screen takes, unless a value is being typed.
var (
	tabsKey  = binding{fmt.Sprintf("1-%d, tab", len(tabNames)), "", "show a tab; shift+tab goes back"}
	runKey   = binding{"r", "run", "start a run, on the Dashboard: market, dry run or live, a maximum"}
	stopKey  = binding{"x", "stop", "stop the run in progress, whoever started it"}
	setupKey = binding{"s", "setup", "the setup checklist"}
	helpKey  = binding{"?", "keys", "this list; Esc closes it"}
	quitKey  = binding{"q", "quit", "quit, as Ctrl-C does (a run in progress keeps running)"}
)

// globals are the keys that work everywhere on the screen shown: all of them
// but on the setup checklist, where r is its own and s and x have nothing to do.
func (m Model) globals() []binding {
	if m.setupShown {
		return []binding{tabsKey, helpKey, quitKey}
	}
	return []binding{tabsKey, runKey, stopKey, setupKey, helpKey, quitKey}
}

// moveKeys move the selection on every list.
var moveKeys = binding{"↑↓", "move", "move the selection (k j too); PgUp PgDn, g G jump"}

// screenKeys are the keys of what is shown: its name on the help screen and
// its bindings, the footer and the help screen both made from them.
func (m Model) screenKeys() (string, []binding) {
	answer := func(yes, no string) []binding { return []binding{{"y", yes, ""}, {"n", no, ""}} }
	entering := func(does string) []binding { return []binding{{"enter", does, ""}, {"esc", "cancels", ""}} }
	switch {
	case m.setupShown && m.checks.editing():
		return "", entering("imports")
	case m.setupShown:
		keys := []binding{moveKeys, {"enter", "fix", "fix the selected check: the init wizard, LinkedIn sign-in, or the Config tab"},
			{"i", "import", "import settings, answers and history from the Python bot"}, {"r", "check again", "check again"}}
		if m.checks.noConfig {
			return "Setup checklist", append(keys, binding{tabKey(tabConfig), "config", "the Config tab, which works before there is a config file"})
		}
		return "Setup checklist", append(keys, binding{"esc", "back", "back to the tabs"})
	case m.tab == tabConfig && m.conf.editing():
		return "", entering("saves")
	case m.tab == tabConfig:
		return "Config", []binding{moveKeys, {"enter", "edit", "edit the setting or secret; Enter saves it, Esc cancels"}}
	case m.tab == tabAnswers && m.ans.deleting != nil:
		return "", answer("deletes", "keeps")
	case m.tab == tabAnswers && m.ans.edit != nil:
		return "", entering("saves")
	case m.tab == tabAnswers:
		return "Answers", []binding{moveKeys, {"←→", "filter", "pending (the review queue) or approved (h l too)"},
			{"enter", "edit", "correct the answer: pick or type an option, or type the answer"},
			{"a", "approve", "approve the answer as shown"}, {"d", "delete", "delete it: ajai asks the question again"}}
	case m.tab == tabSchedule && m.sched.daemon.editing():
		return "", answer("does it", "leaves it")
	case m.tab == tabSchedule && m.sched.deleting != nil:
		return "", answer("deletes", "keeps")
	case m.tab == tabSchedule && m.sched.form != nil:
		return "", []binding{{"↑↓", "field", ""}, {"←→", "day or choice", ""}, {"space", "picks", ""}, {"enter", "adds", ""}, {"esc", "cancels", ""}}
	case m.tab == tabSchedule:
		keys := []binding{moveKeys, {"n", "new", "add a window: weekdays, hours, market, dry run or live"},
			{"space", "on/off", "turn the window on or off"}, {"d", "delete", "delete it"}}
		for _, k := range m.sched.daemon.keys() { // in the daemon's strip, not the footer
			b := k.binding
			b.short = ""
			keys = append(keys, b)
		}
		return "Schedule", keys
	case m.tab == tabJobs && m.jobs.editing():
		return "", entering("searches")
	case m.tab == tabJobs:
		clears := ""
		if m.jobs.text != "" {
			clears = "clears"
		}
		return "Jobs", []binding{moveKeys, {"←→", "filter", "every job, or only the applied, external, failed or skipped ones (h l too)"},
			{"/", "search", "search titles, companies and locations"}, {"esc", clears, "clear the search"},
			{"enter", "open", "open the job in your browser (the application page for an external job)"}}
	case m.runs.form != nil:
		return "", []binding{{"↑↓", "field", ""}, {"←→", "change", ""}, {"enter", "starts", ""}, {"esc", "cancels", ""}}
	case m.runs.confirm != nil:
		return "", answer("stops the run", "keeps it")
	}
	return "Dashboard", []binding{runKey, stopKey, setupKey}
}

// hints is the footer: the shown screen's keys, then the help and quit keys
// unless a value is being typed or a question asked.
func (m Model) hints() string {
	_, keys := m.screenKeys()
	switch {
	case m.help:
		keys = []binding{{"esc", "closes the list", ""}, quitKey}
	case !m.typing():
		keys = append(keys, helpKey, quitKey)
	}
	var parts []string
	for _, b := range keys {
		if b.short != "" {
			parts = append(parts, b.key+" "+b.short)
		}
	}
	return m.styles.hints(strings.Join(parts, " · "))
}

// helpView lists every key that works now: the global ones, then the shown
// screen's own (all of them would not fit the smallest terminal).
func (m Model) helpView() string {
	lines := []string{m.styles.section.Render("Keys")}
	rows := func(title string, keys []binding) {
		lines = append(lines, "", m.styles.section.Render(title), "")
		for _, b := range keys {
			lines = append(lines, "  "+m.styles.key.Render(fmt.Sprintf("%-11s", b.key))+" "+b.long)
		}
	}
	globals := m.globals()
	rows("Everywhere", globals)
	title, keys := m.screenKeys()
	var own []binding
	for _, b := range keys {
		if b.long != "" && !slices.Contains(globals, b) {
			own = append(own, b)
		}
	}
	if len(own) > 0 {
		rows(title, own)
	}
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
