package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/doctor"
)

// setupView is the setup checklist: `aja doctor`'s checks, each with what fixes
// it. Opened by itself when a check fails, and with s from the Dashboard.
type setupView struct {
	loaded    bool
	report    doctor.Report
	noConfig  bool // no config file: only the checklist and the Config tab make sense
	cursor    int
	importing *textinput.Model // the Python checkout's path being typed
	notice    notice
}

// fix is what resolves a check.
type fix int

const (
	noFix        fix = iota
	fixInit          // aja init in the terminal
	fixLogin         // aja login in the terminal
	fixConfigTab     // a setting or a secret, in the Config tab
)

type doctorMsg struct {
	report   doctor.Report
	noConfig bool
}

// subcommandDoneMsg: an aja subcommand run in the terminal has ended.
type subcommandDoneMsg struct {
	args []string
	err  error
}

// openTabMsg asks for a tab to be shown.
type openTabMsg struct{ tab tab }

// runDoctor runs `aja doctor`'s checks, which take a moment (Chrome's version,
// the profile's cookies), away from the screen.
func runDoctor(d Deps) tea.Cmd {
	return func() tea.Msg {
		l, err := config.Load(d.Paths, d.Env, nil)
		_, statErr := os.Stat(d.Paths.ConfigFile)
		return doctorMsg{report: doctor.Run(l, err, d.Paths, d.Probe), noConfig: os.IsNotExist(statErr)}
	}
}

// runInTerminal runs the aja subcommand args in the terminal; the doctor runs
// again when it ends.
func runInTerminal(d Deps, args ...string) tea.Cmd {
	return d.Launcher.InTerminal(args, func(err error) tea.Msg { return subcommandDoneMsg{args: args, err: err} })
}

// fixFor is what resolves c, and how the checklist says so.
func fixFor(c doctor.Check, noConfig bool) (fix, string) {
	if c.Status == doctor.OK {
		return noFix, ""
	}
	switch {
	case c.Name == "config" && noConfig:
		return fixInit, "create the config with the init wizard (`aja init`)"
	case c.Name == "config":
		return fixConfigTab, "fix it in the Config tab"
	case c.Name == "chrome profile":
		return fixLogin, "sign in to LinkedIn once (`aja login` opens Chrome)"
	case c.Name == "ai" || c.Name == "telegram":
		return fixConfigTab, "set the secrets (or the AI settings) in the Config tab"
	case strings.HasPrefix(c.Name, "resume"):
		return fixConfigTab, "set the resume in the Config tab"
	}
	return noFix, ""
}

func (v setupView) update(msg tea.Msg, d Deps) (setupView, tea.Cmd) {
	switch msg := msg.(type) {
	case doctorMsg:
		v.loaded, v.report, v.noConfig = true, msg.report, msg.noConfig
		v.cursor = min(v.cursor, max(0, len(v.report.Checks)-1))
	case subcommandDoneMsg:
		v.notice = notice{text: "`aja " + msg.args[0] + "` ended; checked again."}
		if msg.err != nil {
			v.notice = notice{text: fmt.Sprintf("`aja %s` ended with an error (%v); checked again.", msg.args[0], msg.err), level: levelWarn}
		}
		return v, runDoctor(d)
	case tea.KeyMsg:
		if v.importing != nil {
			return v.importKey(msg, d)
		}
		return v.key(msg, d)
	}
	return v, nil
}

func (v setupView) key(msg tea.KeyMsg, d Deps) (setupView, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(len(v.report.Checks)-1, v.cursor+1)
	case "r":
		v.notice = notice{text: "Checked again."}
		return v, runDoctor(d)
	case "i":
		in := textinput.New()
		in.Prompt = "> "
		in.Placeholder = "the Python bot's folder, e.g. ~/Projects/Auto_job_applier_linkedIn"
		in.Cursor.SetMode(cursor.CursorStatic)
		in.Focus()
		v.importing = &in
	case "enter":
		if v.cursor >= len(v.report.Checks) {
			return v, nil
		}
		switch f, _ := fixFor(v.report.Checks[v.cursor], v.noConfig); f {
		case fixInit:
			return v, runInTerminal(d, "init")
		case fixLogin:
			return v, runInTerminal(d, "login")
		case fixConfigTab:
			return v, func() tea.Msg { return openTabMsg{tabConfig} }
		}
	}
	return v, nil
}

func (v setupView) importKey(msg tea.KeyMsg, d Deps) (setupView, tea.Cmd) {
	switch msg.String() {
	case "esc":
		v.importing = nil
		return v, nil
	case "enter":
		path := expandHome(strings.TrimSpace(v.importing.Value()))
		if path == "" {
			return v, nil
		}
		v.importing = nil
		return v, runInTerminal(d, "import", path)
	}
	in, cmd := v.importing.Update(msg)
	v.importing = &in
	return v, cmd
}

// expandHome turns a leading ~/ into the home directory, as a shell would.
func expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return path
}

// editing reports whether keys go to the import prompt.
func (v setupView) editing() bool { return v.importing != nil }

// summary is the Dashboard's line about the checks: "" until they ran.
func (v setupView) summary() (string, level) {
	if !v.loaded {
		return "", levelOK
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
		return fmt.Sprintf("%d problem(s), %d warning(s): press s for the setup checklist", fail, warn), levelFail
	case warn > 0:
		return fmt.Sprintf("%d warning(s): press s for the setup checklist", warn), levelWarn
	}
	return "every check passes", levelOK
}

func (v setupView) view(s styles, d Deps, width int) string {
	lines := []string{s.section.Render("Setup checklist") + "  " + s.dim.Render("what a run needs, as `aja doctor` checks it")}
	if v.notice.text != "" {
		lines = append(lines, s.byLevel(v.notice.level).Render(v.notice.text))
	}
	lines = append(lines, "")
	if !v.loaded {
		return strings.Join(append(lines, "Checking…"), "\n")
	}
	for i, c := range v.report.Checks {
		mark := "  "
		if i == v.cursor {
			mark = s.key.Render("> ")
		}
		status := s.ok.Render("✓")
		switch c.Status {
		case doctor.Warn:
			status = s.warn.Render("!")
		case doctor.Fail:
			status = s.fail.Render("✗")
		}
		detail, _, _ := strings.Cut(d.short(c.Detail), "\n")
		lines = append(lines, mark+status+" "+fmt.Sprintf("%-15s", c.Name)+" "+detail)
	}
	lines = append(lines, "")
	if v.cursor < len(v.report.Checks) {
		c := v.report.Checks[v.cursor]
		lines = append(lines, wrap(c.Name+": "+d.short(c.Detail), width)...)
		if _, how := fixFor(c, v.noConfig); how != "" {
			lines = append(lines, s.key.Render("Enter")+": "+how)
		}
	}
	lines = append(lines, "")
	if v.importing != nil {
		return strings.Join(append(lines, s.section.Render("Import from the Python bot")+"  "+s.dim.Render("its settings, answer memory, job history and resume"),
			v.importing.View()), "\n")
	}
	lines = append(lines, s.key.Render("i")+"  import settings, answers and history from the Python bot",
		s.key.Render("r")+"  check again")
	if v.noConfig {
		lines = append(lines, s.dim.Render("The other tabs open once there is a config file; the Config tab already sets the secrets."))
	}
	return strings.Join(lines, "\n")
}
