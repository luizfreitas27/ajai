package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/doctor"
)

// doctorEvery: the checks run again this often while the TUI is open, so a
// change made elsewhere (`ajai login` in another terminal) shows.
const doctorEvery = 30 // refreshes, one a second

// setupDetailLines is the least room kept under the checks (on a terminal too
// narrow for the details beside them) for the selected check, its fix and the
// checklist's actions.
const setupDetailLines = 6

// checklist is the setup checklist: `ajai doctor`'s checks, each with what
// fixes it. It opens by itself when something a run needs is missing, and
// with s from the Dashboard.
type checklist struct {
	loaded       bool
	report       doctor.Report
	noConfig     bool // no config file: only the checklist and the Config tab make sense
	dotenvBroken bool // ajai's .env cannot be read
	cursor       int
	top          int // the first check shown
	width        int // the checklist's room on screen
	height       int
	importing    *textinput.Model // the Python checkout's path being typed
	notice       notice
}

// fix is what resolves a check.
type fix int

const (
	noFix        fix = iota
	fixInit          // ajai init in the terminal
	fixLogin         // ajai login in the terminal
	fixConfigTab     // a setting or a secret, in the Config tab
)

type doctorMsg struct {
	report       doctor.Report
	noConfig     bool
	dotenvBroken bool
}

// checkAgainMsg asks for the checks to run again.
type checkAgainMsg struct{}

func checkAgain() tea.Msg { return checkAgainMsg{} }

// subcommandDoneMsg: an ajai subcommand run in the terminal has ended.
type subcommandDoneMsg struct {
	args []string
	err  error
}

// openConfigMsg asks for the Config tab.
type openConfigMsg struct{}

// runDoctor runs `ajai doctor`'s checks, which take a moment (Chrome's version,
// the profile's cookies), away from the screen. Model.checkAgain keeps them
// from overlapping.
func runDoctor(d Deps) tea.Cmd {
	return func() tea.Msg {
		env := d.Env
		msg := doctorMsg{dotenvBroken: env.LoadDotenv(d.Paths.EnvFile) != nil}
		l, err := config.Load(d.Paths, d.Env, nil)
		msg.report = doctor.Run(l, err, d.Paths, d.Probe)
		if l != nil {
			msg.noConfig = !l.FileExisted
		} else { // the config or .env could not be read: is there a config at all?
			_, statErr := os.Stat(d.Paths.ConfigFile)
			msg.noConfig = os.IsNotExist(statErr)
		}
		return msg
	}
}

// runInTerminal runs the ajai subcommand args in the terminal.
func runInTerminal(d Deps, args ...string) tea.Cmd {
	return d.Launcher.InTerminal(args, func(err error) tea.Msg { return subcommandDoneMsg{args: args, err: err} })
}

// blocking reports whether the report says a run cannot work: a check failed,
// or LinkedIn is not signed in.
func blocking(r doctor.Report) bool {
	for _, c := range r.Checks {
		if c.Status == doctor.Fail || (c.Name == doctor.CheckChromeProfile && c.Status != doctor.OK) {
			return true
		}
	}
	return false
}

// fixFor is what resolves c, and how the checklist says so.
func (v checklist) fixFor(c doctor.Check, d Deps) (fix, string) {
	if c.Status == doctor.OK {
		return noFix, ""
	}
	switch {
	case c.Name == doctor.CheckConfig && v.dotenvBroken:
		return noFix, "fix " + d.tilde(d.Paths.EnvFile) + " in an editor (ajai never shows its content)"
	case c.Name == doctor.CheckConfig && v.noConfig:
		return fixInit, "create the config with the init wizard (`ajai init`)"
	case c.Name == doctor.CheckConfig:
		return fixConfigTab, "fix it in the Config tab"
	case c.Name == doctor.CheckChromeProfile:
		return fixLogin, "sign in to LinkedIn once (`ajai login` opens Chrome)"
	case c.Name == doctor.CheckAI || c.Name == doctor.CheckTelegram:
		return fixConfigTab, "set the secrets (or the AI settings) in the Config tab"
	case doctor.IsResume(c.Name):
		return fixConfigTab, "set the resume in the Config tab"
	}
	return noFix, ""
}

func (v checklist) update(msg tea.Msg, d Deps) (checklist, tea.Cmd) {
	switch msg := msg.(type) {
	case doctorMsg:
		v.loaded, v.report, v.noConfig, v.dotenvBroken = true, msg.report, msg.noConfig, msg.dotenvBroken
		v = v.settled(d)
	case subcommandDoneMsg:
		v.notice = notice{text: "`ajai " + msg.args[0] + "` ended; checked again."}
		if msg.err != nil {
			v.notice = notice{text: fmt.Sprintf("`ajai %s` ended with an error (%v); checked again.", msg.args[0], msg.err), level: levelWarn}
		}
		return v, checkAgain
	case tea.KeyMsg:
		if v.importing != nil {
			return v.importKey(msg, d)
		}
		return v.key(msg, d)
	}
	return v, nil
}

func (v checklist) key(msg tea.KeyMsg, d Deps) (checklist, tea.Cmd) {
	if cursor, ok := moved(msg.String(), v.cursor, len(v.report.Checks)); ok {
		v.cursor = cursor
		return v.settled(d), nil
	}
	switch msg.String() {
	case "r":
		v.notice = notice{text: "Checked again."}
		return v, checkAgain
	case "i":
		in := textinput.New()
		in.Prompt = "> "
		in.Placeholder = "the Python bot's folder, e.g. ~/Projects/Auto_job_applier_linkedIn"
		in.Width = inputWidth(detailWidth(v.width))
		in.Cursor.SetMode(cursor.CursorStatic)
		in.Focus()
		v.importing = &in
	case "enter":
		if v.cursor >= len(v.report.Checks) {
			return v, nil
		}
		switch f, _ := v.fixFor(v.report.Checks[v.cursor], d); f {
		case fixInit:
			return v, runInTerminal(d, "init")
		case fixLogin:
			return v, runInTerminal(d, "login")
		case fixConfigTab:
			return v, func() tea.Msg { return openConfigMsg{} }
		}
	}
	return v, nil
}

func (v checklist) importKey(msg tea.KeyMsg, d Deps) (checklist, tea.Cmd) {
	switch msg.String() {
	case "esc":
		v.importing = nil
		return v, nil
	case "enter":
		path := d.expandHome(strings.TrimSpace(v.importing.Value()))
		if path == "" {
			return v, nil
		}
		v.importing = nil
		return v, runInTerminal(d, "import", "--", path) // a folder named -x is not a flag
	}
	in, cmd := v.importing.Update(msg)
	v.importing = &in
	return v, cmd
}

// expandHome turns a leading ~/ into the home directory, as a shell would.
func (d Deps) expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok && d.Home != "" {
		return filepath.Join(d.Home, rest)
	}
	return path
}

// editing reports whether keys go to the import prompt.
func (v checklist) editing() bool { return v.importing != nil }

// summary is the Dashboard's line about the checks; empty until they ran.
func (v checklist) summary() notice {
	if !v.loaded {
		return notice{}
	}
	var warn, fail int
	for _, c := range v.report.Checks {
		switch c.Status {
		case doctor.Warn:
			warn++
		case doctor.Fail:
			fail++
		}
	}
	switch {
	case fail > 0:
		return notice{fmt.Sprintf("%d problem(s), %d warning(s): press s for the setup checklist", fail, warn), levelFail}
	case warn > 0:
		return notice{fmt.Sprintf("%d warning(s): press s for the setup checklist", warn), levelWarn}
	}
	return notice{"every check passes", levelOK}
}

// resized gives the checklist its room on screen.
func (v checklist) resized(width, height int, d Deps) checklist {
	v.width, v.height = width, height
	if v.importing != nil {
		in := *v.importing
		in.Width = inputWidth(detailWidth(width))
		v.importing = &in
	}
	return v.settled(d)
}

// settled keeps the selected check among the checks and in view.
func (v checklist) settled(d Deps) checklist {
	v.cursor, v.top = v.frame(measure, d).keep(v.cursor, v.top, v.width, v.height)
	return v
}

// frame is the checklist for the frame to lay out: the checks, then the
// selected one's problem and fix, and the checklist's own actions.
func (v checklist) frame(s styles, d Deps) listFrame {
	return listFrame{title: "Setup checklist", summary: "what a run needs, as `ajai doctor` checks it", notes: listNotes(s, "", v.notice, v.width),
		waiting: waitText(v.loaded, "Checking…"), rows: len(v.report.Checks), minDetails: setupDetailLines,
		row: func(i, _ int) string {
			c := v.report.Checks[i]
			status := s.ok.Render("✓")
			switch c.Status {
			case doctor.Warn:
				status = s.warn.Render("!")
			case doctor.Fail:
				status = s.fail.Render("✗")
			}
			detail, _, _ := strings.Cut(d.tilde(c.Detail), "\n")
			return status + " " + fmt.Sprintf("%-15s", c.Name) + " " + detail
		},
		details: func(s styles, width int, _ bool) []string { return v.detail(s, d, width) },
	}
}

// detail is the selected check in full and what fixes it, then the import
// (its folder being typed) or the checklist's actions.
func (v checklist) detail(s styles, d Deps, width int) []string {
	var lines []string
	if v.cursor >= 0 && v.cursor < len(v.report.Checks) {
		c := v.report.Checks[v.cursor]
		lines = append(lines, wrap(c.Name+": "+d.tilde(c.Detail), width)...)
		if _, how := v.fixFor(c, d); how != "" {
			lines = append(lines, s.key.Render("Enter")+": "+how)
		}
		lines = append(lines, "")
	}
	if v.importing != nil {
		return append(lines, s.section.Render("Import from the Python bot")+"  "+s.dim.Render("its settings, answer memory, job history and resume"),
			v.importing.View())
	}
	lines = append(lines, s.key.Render("i")+"  import settings, answers and history from the Python bot",
		s.key.Render("r")+"  check again")
	if v.noConfig {
		lines = append(lines, wrapStyled(s.dim, "The other tabs open once there is a config file; the Config tab already sets the secrets.", width)...)
	}
	return lines
}

func (v checklist) view(s styles, d Deps) string {
	return v.frame(s, d).view(s, v.width, v.height, v.cursor, v.top)
}
