package tui

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/daemon"
)

const (
	// configKeyWidth is the settings list's first column.
	configKeyWidth = 34
	// configDetailLines is the least room kept under the list (on a terminal
	// too narrow for the details beside it) for the selected
	// row's explanation or the editor; they take more when they need it.
	configDetailLines = 6
)

// configTab is the Config tab: the three secrets, shown only as set or not and
// typed into a masked field that saves them in ajai's .env; then every setting
// by section, as the config file alone has it (an override, from the
// environment or .env, is marked and never saved), edited one at a time.
type configTab struct {
	loaded  bool
	problem string   // the config file or .env could not be read
	missing bool     // there is no config file yet
	invalid []string // what the file's validation finds wrong
	rows    []configRow
	path    string // the config file, as the header shows it
	home    string // the home directory, shown as ~ in the values
	cursor  int    // the selected row (never a section title)
	top     int    // the first row shown
	width   int    // the tab's room on screen
	height  int
	edit    *configEdit
	notice  notice
}

type rowKind int

const (
	titleRow rowKind = iota
	secretRow
	settingRow
)

type configRow struct {
	kind  rowKind
	title string // titleRow

	secret          string // secretRow: the variable
	set             bool   // it has a value
	fromEnvironment bool   // the value is the environment's, not .env's

	field        config.Field // settingRow
	value        string       // its value in the file
	override     string       // the variable overriding it, or ""
	overrideFrom string       // "your environment" or ".env"
}

// name is what the row edits: a secret's variable or a setting's key.
// isTitle says whether the row is a section's title, which is never selected.
func (r configRow) isTitle() bool { return r.kind == titleRow }

func (r configRow) name() string {
	if r.kind == secretRow {
		return r.secret
	}
	return r.field.Key
}

// configEdit is the row being edited. A closed set of options is picked from
// (any number of them for a list), anything else typed.
type configEdit struct {
	target  configRow
	input   textinput.Model
	options []string
	multi   bool         // a list: any number of options
	picked  map[int]bool // multi: the options chosen
	choice  int          // the option under the cursor
	refusal string       // why the last value was refused
	saving  bool
}

// notice is the outcome of the last action.
type notice struct {
	text  string
	level level
}

type level int

const (
	levelOK level = iota
	levelWarn
	levelFail
)

type configLoadedMsg struct {
	problem string
	missing bool
	invalid []string
	rows    []configRow
}

type configSavedMsg struct {
	notice  notice
	refusal string // the value was refused: keep editing
}

// loadConfig reads the secrets' state and the config file.
func loadConfig(d Deps) tea.Cmd {
	return func() tea.Msg { return readConfigRows(d) }
}

func readConfigRows(d Deps) configLoadedMsg {
	var msg configLoadedMsg
	env := d.Env
	err := env.LoadDotenv(d.Paths.EnvFile)
	dotenvOK := err == nil
	if !dotenvOK {
		msg.problem = config.DotenvError(d.Paths.EnvFile, err).Error()
	}
	msg.rows = append(msg.rows, configRow{kind: titleRow, title: "secrets, kept in " + d.tilde(d.Paths.EnvFile)})
	for _, secret := range env.Secrets().Values() {
		_, inEnvironment := env.Lookup(secret[0])
		msg.rows = append(msg.rows, configRow{kind: secretRow, secret: secret[0], set: secret[1] != "", fromEnvironment: inEnvironment})
	}

	file, err := config.LoadFile(d.Paths)
	switch {
	case file == nil:
		msg.problem = err.Error()
		return msg
	case !file.FileExisted:
		msg.missing = true
		return msg
	}
	var invalid *config.ValidationError
	if errors.As(err, &invalid) {
		for _, fe := range invalid.Errors {
			msg.invalid = append(msg.invalid, fe.Error())
		}
	}
	overridden := map[string]bool{}
	if dotenvOK {
		effective, err := config.Load(d.Paths, d.Env, nil)
		if effective == nil {
			msg.invalid = append(msg.invalid, err.Error()) // an AJAI_CFG_ value that cannot apply
		} else {
			for _, key := range effective.Overrides {
				overridden[key] = true
			}
		}
	}
	section := ""
	for _, f := range config.Fields() {
		if s, _, _ := strings.Cut(f.Key, "."); s != section {
			section = s
			msg.rows = append(msg.rows, configRow{kind: titleRow, title: s})
		}
		value, _ := file.Config.Get(f.Key)
		r := configRow{kind: settingRow, field: f, value: value}
		if overridden[f.Key] {
			r.override, r.overrideFrom = config.EnvOverrideName(f.Key), ".env"
			if _, ok := env.Lookup(r.override); ok {
				r.overrideFrom = "your environment"
			}
		}
		msg.rows = append(msg.rows, r)
	}
	return msg
}

// editing reports whether keys go to the editor.
func (c configTab) editing() bool { return c.edit != nil }

func (c configTab) update(msg tea.Msg, d Deps) (configTab, tea.Cmd) {
	switch msg := msg.(type) {
	case configLoadedMsg:
		c.loaded, c.problem, c.missing, c.invalid, c.rows = true, msg.problem, msg.missing, msg.invalid, msg.rows
		c.path, c.home = d.tilde(d.Paths.ConfigFile), d.Home
		c.cursor = c.nearestSelectable(c.cursor)
		if c.edit != nil { // the rows moved under the editor: follow its row
			c.cursor = c.find(c.edit.target.name())
		}
		return c.scrolled(), nil
	case configSavedMsg:
		if c.edit == nil {
			return c, nil
		}
		e := *c.edit
		e.saving = false
		if msg.refusal != "" {
			e.refusal = msg.refusal
			c.edit = &e
			return c.scrolled(), nil // the details grew: the edited row stays in view
		}
		c.edit, c.notice = nil, msg.notice
		return c, loadConfig(d)
	case tea.KeyMsg:
		if c.edit != nil {
			return c.editKey(msg, d)
		}
		return c.browseKey(msg).scrolled(), nil
	}
	return c, nil
}

func (c configTab) browseKey(msg tea.KeyMsg) configTab {
	switch msg.String() {
	case "up", "k":
		c.cursor = c.step(-1)
	case "down", "j":
		c.cursor = c.step(1)
	case "pgup":
		c.cursor = c.step(-10)
	case "pgdown":
		c.cursor = c.step(10)
	case "home", "g":
		c.cursor, c.top = c.nearestSelectable(0), 0
	case "end", "G":
		c.cursor = c.step(len(c.rows))
	case "enter":
		if c.cursor < len(c.rows) && !c.rows[c.cursor].isTitle() {
			c.edit = newConfigEdit(c.rows[c.cursor], detailWidth(c.width))
			c.notice = notice{}
		}
	}
	return c
}

// step moves the selection n selectable rows, stopping at either end.
func (c configTab) step(n int) int {
	at := c.cursor
	for n != 0 {
		next, dir := at, 1
		if n < 0 {
			dir = -1
		}
		for next += dir; next >= 0 && next < len(c.rows) && c.rows[next].isTitle(); next += dir {
		}
		if next < 0 || next >= len(c.rows) {
			break
		}
		at, n = next, n-dir
	}
	return at
}

func (c configTab) nearestSelectable(i int) int {
	for ; i < len(c.rows); i++ {
		if !c.rows[i].isTitle() {
			return i
		}
	}
	return max(0, len(c.rows)-1)
}

// find is the row that edits name, or the first selectable one.
func (c configTab) find(name string) int {
	for i, r := range c.rows {
		if !r.isTitle() && r.name() == name {
			return i
		}
	}
	return c.nearestSelectable(0)
}

// resized gives the tab its room on screen.
func (c configTab) resized(width, height int) configTab {
	c.width, c.height = width, height
	if c.edit != nil {
		e := *c.edit
		e.input.Width = inputWidth(detailWidth(width))
		c.edit = &e
	}
	return c.scrolled()
}

func inputWidth(width int) int { return max(10, width-4) }

// scrolled keeps the selected row in view, moving the list as little as it
// can; going up, its section's title comes into view too when it fits.
func (c configTab) scrolled() configTab {
	c.cursor, c.top = c.frame(measure).keep(c.cursor, c.top, c.width, c.height)
	return c
}

// measure renders nothing anyone sees: it counts lines.
var measure = func() styles {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	return newStyles(r)
}()

func newConfigEdit(r configRow, width int) *configEdit {
	e := &configEdit{target: r, input: textinput.New()}
	e.input.Prompt = "> "
	e.input.Width = inputWidth(width)
	e.input.Cursor.SetMode(cursor.CursorStatic)
	switch {
	case r.kind == secretRow:
		e.input.EchoMode = textinput.EchoPassword
		e.input.EchoCharacter = '•'
		e.input.Placeholder = "the value; leave empty to remove it from .env"
	case len(r.field.Options) > 0:
		e.options = r.field.Options
		e.multi = r.field.Kind == reflect.Slice
	case r.field.Kind == reflect.Bool:
		e.options = []string{"true", "false"}
	default:
		e.input.SetValue(r.value)
		e.input.CursorEnd()
	}
	if e.multi {
		current := map[string]bool{}
		for _, v := range strings.Split(r.value, ",") {
			current[strings.TrimSpace(v)] = true
		}
		e.picked = map[int]bool{}
		for i, o := range e.options {
			e.picked[i] = current[o]
		}
	} else {
		for i, o := range e.options {
			if o == r.value {
				e.choice = i
			}
		}
	}
	e.input.Focus()
	return e
}

// value is what the editor holds: the text typed, the option chosen, or the
// options picked, comma-separated.
func (e configEdit) value() string {
	switch {
	case e.multi:
		var picked []string
		for i, o := range e.options {
			if e.picked[i] {
				picked = append(picked, o)
			}
		}
		return strings.Join(picked, ", ")
	case e.options != nil:
		return e.options[e.choice]
	}
	return e.input.Value()
}

func (c configTab) editKey(msg tea.KeyMsg, d Deps) (configTab, tea.Cmd) {
	e := *c.edit
	if e.saving {
		return c, nil
	}
	switch msg.String() {
	case "esc":
		c.edit = nil
		return c.scrolled(), nil
	case "enter":
		e.saving, e.refusal = true, ""
		c.edit = &e
		if e.target.kind == secretRow {
			return c, saveSecret(d, e.target.secret, e.value())
		}
		return c, saveSetting(d, e.target.field.Key, e.value())
	}
	if e.options != nil {
		switch msg.String() {
		case "up", "k":
			e.choice = max(0, e.choice-1)
		case "down", "j":
			e.choice = min(len(e.options)-1, e.choice+1)
		case " ":
			if e.multi {
				picked := map[int]bool{}
				for i, on := range e.picked {
					picked[i] = on
				}
				picked[e.choice] = !picked[e.choice]
				e.picked = picked
			}
		}
		c.edit = &e
		return c.scrolled(), nil
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	c.edit = &e
	return c, cmd
}

// saveSetting validates the new value against the file alone and writes it.
func saveSetting(d Deps, key, value string) tea.Cmd {
	return func() tea.Msg {
		file, err := config.LoadFile(d.Paths)
		switch {
		case file == nil:
			return configSavedMsg{refusal: "the config file cannot be read: " + err.Error()}
		case !file.FileExisted:
			return configSavedMsg{refusal: "there is no config file yet at " + d.tilde(d.Paths.ConfigFile) + "; run `ajai init` first"}
		}
		others, err := file.Config.SetChecked(key, value)
		if err != nil {
			return configSavedMsg{refusal: err.Error()}
		}
		if err := config.Write(d.Paths.ConfigFile, file.Config); err != nil {
			return configSavedMsg{refusal: "could not write the config file: " + err.Error()}
		}
		saved, _ := file.Config.Get(key)
		d.logger().Info("config set from the TUI", "key", key, "value", saved)
		n := notice{text: fmt.Sprintf("Saved %s = %s.", key, saved)}
		if others != nil {
			n.level = levelWarn
			n.text += fmt.Sprintf(" The file still has %d other problem(s), listed above.", len(others.Errors))
		}
		return configSavedMsg{notice: daemonNote(d, key, n, true)}
	}
}

// saveSecret writes name to ajai's .env; the value is never shown or logged.
func saveSecret(d Deps, name, value string) tea.Cmd {
	return func() tea.Msg {
		value = strings.TrimSpace(value)
		if err := config.SetDotenv(d.Paths.EnvFile, name, value); err != nil {
			return configSavedMsg{refusal: err.Error()}
		}
		n := notice{text: "Saved " + name + " in .env."}
		if value == "" {
			n.text = "Removed " + name + " from .env."
		}
		d.logger().Info("secret changed from the TUI", "name", name, "removed", value == "")
		if _, inEnvironment := d.Env.Lookup(name); inEnvironment {
			n.level = levelWarn
			n.text += " Your environment sets it too, and wins."
		}
		return configSavedMsg{notice: daemonNote(d, name, n, false)}
	}
}

// daemonNote says, when the daemon runs, when a change to name reaches it: at
// its next run for a setting, or once it is restarted for what it reads only
// when it starts.
func daemonNote(d Deps, name string, n notice, setting bool) notice {
	if _, running, _ := daemon.ReadLock(d.Paths.DataDir); !running {
		return n
	}
	switch why, ok := config.ReadAtDaemonStart[name]; {
	case ok:
		n.level = levelWarn
		n.text += " Restart the daemon to use it: " + why + "."
	case setting:
		n.text += " The daemon's next run uses it."
	}
	return n
}

// heading is what the tab says above its list: a problem, the notice of the
// last action.
func (c configTab) heading() []notice {
	var head []notice
	switch {
	case c.problem != "":
		head = append(head, notice{c.problem, levelFail})
	case c.missing:
		head = append(head, notice{"There is no config file yet: run `ajai init`. The secrets can be set already.", levelWarn})
	case len(c.invalid) > 0:
		head = append(head, notice{fmt.Sprintf("The file has %d problem(s): %s", len(c.invalid), strings.Join(c.invalid, "; ")), levelWarn})
	}
	if c.notice.text != "" {
		head = append(head, c.notice)
	}
	return head
}

// frame is the tab for the frame to lay out: the file's path, then the
// sections, their titles and their rows.
func (c configTab) frame(s styles) listFrame {
	var notes []string
	for _, n := range c.heading() {
		notes = append(notes, wrapStyled(s.byLevel(n.level), n.text, c.width)...)
	}
	return listFrame{title: "Config", summary: c.path, notes: notes, waiting: waitText(c.loaded, "Reading…"), rows: len(c.rows), minDetails: configDetailLines,
		row:     func(i, _ int) string { return c.rowLine(s, i) },
		isTitle: func(i int) bool { return c.rows[i].isTitle() },
		details: func(s styles, width int, _ bool) []string { return c.detail(s, width) },
	}
}

func (c configTab) view(s styles, width int) string {
	return c.frame(s).view(s, width, c.height, c.cursor, c.top)
}

// rowLine is row i: a section's title, or a secret or a setting and its value.
func (c configTab) rowLine(s styles, i int) string {
	r := c.rows[i]
	if r.isTitle() {
		return s.section.Render(r.title)
	}
	name, value := r.secret, ""
	switch r.kind {
	case secretRow:
		value = s.dim.Render("not set")
		if r.set {
			value = s.ok.Render("set") + " (.env)"
			if r.fromEnvironment {
				value = s.ok.Render("set") + " (your environment)"
			}
		}
	default:
		_, name, _ = strings.Cut(r.field.Key, ".")
		value = tildeUnder(c.home, r.value) // shown only: the editor keeps the value as it is
		if r.override != "" {
			value += s.warn.Render("  ← " + r.override + " overrides it")
		}
	}
	return fmt.Sprintf("%-*s", configKeyWidth, name) + " " + value
}

// detail explains the selected row, or shows the editor.
func (c configTab) detail(s styles, width int) []string {
	var lines []string
	if c.cursor < 0 || c.cursor >= len(c.rows) {
		return lines
	}
	if e := c.edit; e != nil {
		return append(lines, c.editor(s, *e)...)
	}
	text := func(st lipgloss.Style, t string) {
		for _, l := range wrap(t, width) {
			lines = append(lines, st.Render(l))
		}
	}
	r := c.rows[c.cursor]
	switch r.kind {
	case secretRow:
		text(s.plain, config.SecretDocs[r.secret])
		if r.fromEnvironment {
			text(s.warn, "Your environment sets it, and wins over .env.")
		}
	case settingRow:
		text(s.plain, r.field.Doc)
		var facts []string
		if len(r.field.Options) > 0 {
			labels := make([]string, len(r.field.Options))
			for i, o := range r.field.Options {
				labels[i] = optionLabel(o)
			}
			facts = append(facts, "one of: "+strings.Join(labels, " | "))
		}
		if r.field.Example != "" {
			facts = append(facts, "e.g. "+r.field.Example)
		}
		if len(facts) > 0 {
			text(s.dim, strings.Join(facts, "   "))
		}
		if r.override != "" {
			text(s.warn, r.override+" in "+r.overrideFrom+" wins over the file; an edit here changes the file.")
		}
	}
	return lines
}

func (c configTab) editor(s styles, e configEdit) []string {
	r := e.target
	what := r.name()
	switch {
	case r.kind == secretRow:
		what += " (hidden as you type)"
	case e.multi:
		what += " (Space picks or drops an option)"
	case r.field.Kind == reflect.Slice:
		what += " (comma-separated; empty clears it)"
	}
	lines := []string{s.section.Render("Edit " + what)}
	if e.options == nil {
		lines = append(lines, e.input.View())
	}
	for i, o := range e.options {
		mark := s.mark(i == e.choice)
		if e.multi {
			box := "[ ] "
			if e.picked[i] {
				box = "[x] "
			}
			mark += box
		}
		lines = append(lines, mark+optionLabel(o))
	}
	if e.refusal != "" {
		lines = append(lines, s.fail.Render("Refused: "+e.refusal))
	}
	return lines
}

// optionLabel names an option; the empty one is "(blank)".
func optionLabel(o string) string {
	if o == "" {
		return "(blank)"
	}
	return o
}

// wrap breaks text into lines of at most width cells, at spaces.
func wrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) <= width:
			line += " " + word
		default:
			lines, line = append(lines, line), word
		}
	}
	return append(lines, line)
}
