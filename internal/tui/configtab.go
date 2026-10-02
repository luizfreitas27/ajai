package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/daemon"
)

const (
	// configKeyWidth is the settings list's first column.
	configKeyWidth = 34
	// configDetailLines is the room under the list for the selected row's
	// explanation, or for the editor.
	configDetailLines = 7
)

// configTab is the Config tab: the three secrets, shown only as set or not and
// typed into a masked field that saves them in aja's .env; then every setting
// by section, as the config file has it (an environment override is marked and
// never saved), edited one at a time.
type configTab struct {
	loaded  bool
	problem string   // the config file or .env could not be read
	missing bool     // there is no config file yet
	invalid []string // what the file's validation finds wrong
	rows    []configRow
	cursor  int // the selected row (never a section title)
	top     int // the first row shown
	width   int // the tab's room on screen
	height  int
	edit    *configEdit
	notice  notice
}

type configRow struct {
	title    string       // a section title; nothing else is set
	field    config.Field // a setting
	value    string       // its value in the file
	override string       // the variable overriding it, or ""
	secret   string       // a secret's variable name
	set      bool         // the secret has a value
	from     string       // where that value comes from
}

func (r configRow) selectable() bool { return r.title == "" }

// configEdit is the row being edited: a closed set of options is picked from,
// anything else typed.
type configEdit struct {
	row     int
	input   textinput.Model
	options []string
	choice  int
	refusal string // why the last value was refused
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
	if err := env.LoadDotenv(d.Paths.EnvFile); err != nil {
		msg.problem = err.Error()
	}
	msg.rows = append(msg.rows, configRow{title: "secrets, kept in " + d.Paths.EnvFile})
	for _, name := range config.SecretEnvNames {
		_, inEnvironment := env.Lookup(name)
		r := configRow{secret: name, set: strings.TrimSpace(env.Get(name)) != ""}
		switch {
		case r.set && inEnvironment:
			r.from = "your environment"
		case r.set:
			r.from = ".env"
		}
		msg.rows = append(msg.rows, r)
	}

	// The file alone, so an override is never shown as the file's value or saved.
	file, err := config.Load(d.Paths, config.MapEnv(nil), nil)
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
	if effective, _ := config.Load(d.Paths, d.Env, nil); effective != nil {
		for _, key := range effective.Overrides {
			overridden[key] = true
		}
	}
	section := ""
	for _, f := range config.Fields() {
		if s, _, _ := strings.Cut(f.Key, "."); s != section {
			section = s
			msg.rows = append(msg.rows, configRow{title: s})
		}
		value, _ := file.Config.Get(f.Key)
		r := configRow{field: f, value: value}
		if overridden[f.Key] {
			r.override = config.EnvOverrideName(f.Key)
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
		c.cursor = c.nearestSelectable(c.cursor)
		c = c.scrolled()
	case configSavedMsg:
		if c.edit == nil {
			return c, nil
		}
		c.edit.saving = false
		if msg.refusal != "" {
			c.edit.refusal = msg.refusal
			return c, nil
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

// resized gives the tab its room on screen.
func (c configTab) resized(width, height int) configTab {
	c.width, c.height = width, height
	return c.scrolled()
}

// scrolled keeps the selected row in view, moving the list as little as it can.
func (c configTab) scrolled() configTab {
	n := c.listHeight()
	if c.cursor < c.top {
		c.top = c.cursor
	}
	if c.cursor >= c.top+n {
		c.top = c.cursor - n + 1
	}
	c.top = max(0, c.top)
	return c
}

// listHeight is the room for rows: the tab's, less its title line, its
// heading and the detail.
func (c configTab) listHeight() int {
	lines := 0
	for _, n := range c.heading() {
		lines += len(wrap(n.text, c.width))
	}
	return max(1, c.height-1-lines-configDetailLines)
}

func (c configTab) browseKey(msg tea.KeyMsg) configTab {
	switch msg.String() {
	case "up", "k":
		c.cursor = c.step(-1)
	case "down", "j":
		c.cursor = c.step(1)
	case "pgup":
		for range 10 {
			c.cursor = c.step(-1)
		}
	case "pgdown":
		for range 10 {
			c.cursor = c.step(1)
		}
	case "home", "g":
		c.cursor = c.nearestSelectable(0)
	case "end", "G":
		c.cursor = c.step(len(c.rows))
	case "enter":
		if c.cursor < len(c.rows) {
			c.edit = newConfigEdit(c.cursor, c.rows[c.cursor])
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
		for next += dir; next >= 0 && next < len(c.rows) && !c.rows[next].selectable(); next += dir {
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
		if c.rows[i].selectable() {
			return i
		}
	}
	return max(0, len(c.rows)-1)
}

func newConfigEdit(row int, r configRow) *configEdit {
	e := &configEdit{row: row, input: textinput.New()}
	e.input.Prompt = "> "
	e.input.Cursor.SetMode(cursor.CursorStatic)
	switch {
	case r.secret != "":
		e.input.EchoMode = textinput.EchoPassword
		e.input.EchoCharacter = '•'
		e.input.Placeholder = "the value; leave empty to remove it from .env"
	case len(r.field.Options) > 0:
		e.options = r.field.Options
	case r.field.Kind == reflect.Bool:
		e.options = []string{"true", "false"}
	default:
		e.input.SetValue(r.value)
		e.input.CursorEnd()
	}
	for i, o := range e.options {
		if o == r.value {
			e.choice = i
		}
	}
	e.input.Focus()
	return e
}

func (c configTab) editKey(msg tea.KeyMsg, d Deps) (configTab, tea.Cmd) {
	e := *c.edit
	if e.saving {
		return c, nil
	}
	row := c.rows[e.row]
	switch msg.String() {
	case "esc":
		c.edit = nil
		return c, nil
	case "enter":
		e.saving, e.refusal = true, ""
		c.edit = &e
		if row.secret != "" {
			return c, saveSecret(d, row.secret, e.input.Value())
		}
		value := e.input.Value()
		if e.options != nil {
			value = e.options[e.choice]
		}
		return c, saveSetting(d, row.field.Key, value)
	}
	if e.options != nil {
		switch msg.String() {
		case "up", "k":
			e.choice = max(0, e.choice-1)
		case "down", "j":
			e.choice = min(len(e.options)-1, e.choice+1)
		}
		c.edit = &e
		return c, nil
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	c.edit = &e
	return c, cmd
}

// saveSetting validates the new value against the file alone and writes it.
func saveSetting(d Deps, key, value string) tea.Cmd {
	return func() tea.Msg {
		file, err := config.Load(d.Paths, config.MapEnv(nil), nil)
		switch {
		case file == nil:
			return configSavedMsg{refusal: "the config file cannot be read: " + err.Error()}
		case !file.FileExisted:
			return configSavedMsg{refusal: "there is no config file yet at " + d.Paths.ConfigFile + "; run `aja init` first"}
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
		if _, running, _ := daemon.ReadLock(d.Paths.DataDir); running {
			n.text += " The daemon's next run uses it."
		}
		if others != nil {
			n.level = levelWarn
			n.text += fmt.Sprintf(" The file still has %d other problem(s), listed above.", len(others.Errors))
		}
		return configSavedMsg{notice: n}
	}
}

// saveSecret writes name to aja's .env; the value is never shown or logged.
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
		if _, running, _ := daemon.ReadLock(d.Paths.DataDir); running && name != config.EnvAIAPIKey {
			n.level = levelWarn
			n.text += " Restart the daemon to use it: it reads Telegram's settings when it starts."
		}
		return configSavedMsg{notice: n}
	}
}

// heading is what the tab says above its list: a problem, the notice of the
// last action. Each entry is one line.
func (c configTab) heading() []notice {
	var head []notice
	switch {
	case c.problem != "":
		head = append(head, notice{c.problem, levelFail})
	case c.missing:
		head = append(head, notice{"There is no config file yet: run `aja init`. The secrets can be set already.", levelWarn})
	case len(c.invalid) > 0:
		head = append(head, notice{fmt.Sprintf("The file has %d problem(s): %s", len(c.invalid), strings.Join(c.invalid, "; ")), levelWarn})
	}
	if c.notice.text != "" {
		head = append(head, c.notice)
	}
	return head
}

func (c configTab) view(s styles, d Deps, width int) string {
	if !c.loaded {
		return "Reading…"
	}
	lines := []string{s.section.Render("Config") + "  " + s.dim.Render(d.Paths.ConfigFile)}
	for _, n := range c.heading() {
		for _, l := range wrap(n.text, width) {
			lines = append(lines, s.byLevel(n.level).Render(l))
		}
	}
	n := c.listHeight()
	for i := c.top; i < len(c.rows) && i < c.top+n; i++ {
		lines = append(lines, c.rowLine(s, i))
	}
	for i := len(c.rows) - c.top; i < n; i++ {
		lines = append(lines, "")
	}
	return strings.Join(append(lines, c.detail(s, width)...), "\n")
}

func (c configTab) rowLine(s styles, i int) string {
	r := c.rows[i]
	if r.title != "" {
		return s.section.Render(r.title)
	}
	mark := "  "
	if i == c.cursor {
		mark = s.key.Render("> ")
	}
	name, value := "", ""
	switch {
	case r.secret != "":
		name, value = r.secret, s.dim.Render("not set")
		if r.set {
			value = s.ok.Render("set") + " (" + r.from + ")"
		}
	default:
		_, name, _ = strings.Cut(r.field.Key, ".")
		value = r.value
		if r.override != "" {
			value += s.warn.Render("  ← " + r.override + " overrides it")
		}
	}
	return mark + fmt.Sprintf("%-*s", configKeyWidth, name) + " " + value
}

// detail explains the selected row, or shows the editor.
func (c configTab) detail(s styles, width int) []string {
	lines := []string{s.dim.Render(strings.Repeat("─", width))}
	if len(c.rows) == 0 || c.cursor >= len(c.rows) {
		return lines
	}
	r := c.rows[c.cursor]
	if e := c.edit; e != nil {
		what := r.field.Key
		switch {
		case r.secret != "":
			what = r.secret + " (hidden as you type)"
		case r.field.Kind == reflect.Slice:
			what += " (comma-separated; empty clears it)"
		}
		lines = append(lines, s.section.Render("Edit "+what))
		if e.options != nil {
			for i, o := range e.options {
				mark := "  "
				if i == e.choice {
					mark = s.key.Render("> ")
				}
				lines = append(lines, mark+o)
			}
		} else {
			lines = append(lines, e.input.View())
		}
		if e.refusal != "" {
			lines = append(lines, s.fail.Render("Refused: "+e.refusal))
		}
		return lines
	}
	if r.secret != "" {
		lines = append(lines, s.plain.Width(width).Render(secretDoc[r.secret]))
		if r.from == "your environment" {
			lines = append(lines, s.warn.Render("Your environment sets it, and wins over .env."))
		}
		return lines
	}
	lines = append(lines, s.plain.Width(width).Render(r.field.Doc))
	var facts []string
	if len(r.field.Options) > 0 {
		facts = append(facts, "one of: "+strings.Join(r.field.Options, " | "))
	}
	if r.field.Example != "" {
		facts = append(facts, "e.g. "+r.field.Example)
	}
	if len(facts) > 0 {
		lines = append(lines, s.dim.Render(strings.Join(facts, "   ")))
	}
	if r.override != "" {
		lines = append(lines, s.warn.Render(r.override+" in your environment wins over the file; an edit here changes the file."))
	}
	return lines
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

// secretDoc says what each secret is for.
var secretDoc = map[string]string{
	config.EnvAIAPIKey:         "The AI provider's API key, used when ai.enabled is true and the provider needs one.",
	config.EnvTelegramBotToken: "The token @BotFather gave your bot: run summaries, nudges and remote commands.",
	config.EnvTelegramChatID:   "The numeric id of your chat with the bot; aja only talks and listens there.",
}
