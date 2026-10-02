package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/questions"
)

// answersDetailLines is the room kept under the list for the selected answer.
const answersDetailLines = 8

// answersTab is the Answers tab: the answer memory, the review queue
// (pending) first, the approved answers a key away. An answer is approved as
// shown, corrected (picked from its options, or typed), or deleted.
type answersTab struct {
	loaded   bool
	state    string // "pending" or "approved"
	answers  []memory.Remembered
	problem  string
	cursor   int
	top      int
	width    int
	height   int
	edit     *answerEdit
	deleting *memory.Remembered // the answer Delete asks about
	notice   notice
}

// answerEdit is the answer being corrected: a select or radio with remembered
// options is picked (or typed: an option or the beginning of one), anything
// else typed.
type answerEdit struct {
	target  memory.Remembered
	input   textinput.Model
	choice  int
	refusal string
}

type answersLoadedMsg struct {
	state   string
	answers []memory.Remembered
	problem string
}

type answerChangedMsg struct {
	notice  notice
	refusal string // the change was refused: keep editing
}

// loadAnswers reads the answers in state.
func loadAnswers(d Deps, state string) tea.Cmd {
	return func() tea.Msg {
		list, err := memory.New(d.Store, d.logger()).List(state)
		msg := answersLoadedMsg{state: state, answers: list}
		if err != nil {
			msg.problem = "Could not read the answer memory: " + err.Error()
		}
		return msg
	}
}

func (a answersTab) editing() bool { return a.edit != nil || a.deleting != nil }

// shownState is the filter, pending until the user switches.
func (a answersTab) shownState() string {
	if a.state == "" {
		return "pending"
	}
	return a.state
}

func (a answersTab) resized(width, height int) answersTab {
	a.width, a.height = width, height
	if a.edit != nil {
		e := *a.edit
		e.input.Width = inputWidth(width)
		a.edit = &e
	}
	return a.scrolled()
}

func (a answersTab) listHeight() int {
	head := 2 // title, column names
	if a.notice.text != "" || a.problem != "" {
		head++
	}
	return max(1, a.height-head-answersDetailLines)
}

func (a answersTab) scrolled() answersTab {
	n := a.listHeight()
	a.cursor = max(0, min(a.cursor, len(a.answers)-1))
	if a.cursor < a.top {
		a.top = a.cursor
	}
	if a.cursor >= a.top+n {
		a.top = a.cursor - n + 1
	}
	return a
}

func (a answersTab) update(msg tea.Msg, d Deps) (answersTab, tea.Cmd) {
	switch msg := msg.(type) {
	case answersLoadedMsg:
		if msg.state != a.shownState() {
			return a, nil // the filter changed meanwhile
		}
		a.loaded, a.answers, a.problem = true, msg.answers, msg.problem
		return a.scrolled(), nil
	case answerChangedMsg:
		if msg.refusal != "" && a.edit != nil {
			e := *a.edit
			e.refusal = msg.refusal
			a.edit = &e
			return a, nil
		}
		a.edit, a.notice = nil, msg.notice
		return a, loadAnswers(d, a.shownState())
	case tea.KeyMsg:
		switch {
		case a.deleting != nil:
			return a.deleteKey(msg, d)
		case a.edit != nil:
			return a.editKey(msg, d)
		}
		return a.browseKey(msg, d)
	}
	return a, nil
}

func (a answersTab) selected() (memory.Remembered, bool) {
	if a.cursor >= len(a.answers) {
		return memory.Remembered{}, false
	}
	return a.answers[a.cursor], true
}

func (a answersTab) browseKey(msg tea.KeyMsg, d Deps) (answersTab, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		a.cursor--
	case "down", "j":
		a.cursor++
	case "pgup":
		a.cursor -= 10
	case "pgdown":
		a.cursor += 10
	case "home", "g":
		a.cursor = 0
	case "end", "G":
		a.cursor = len(a.answers) - 1
	case "tab":
		a.state = map[string]string{"pending": "approved", "approved": "pending"}[a.shownState()]
		a.loaded, a.cursor, a.top, a.notice = false, 0, 0, notice{}
		return a, loadAnswers(d, a.state)
	case "a":
		if r, ok := a.selected(); ok {
			return a, approveAnswer(d, r)
		}
	case "e", "enter":
		if r, ok := a.selected(); ok {
			if r.Kind == string(questions.Checkbox) {
				a.notice = notice{"A checkbox takes only Approve (a) or Delete (d).", levelWarn}
				return a, nil
			}
			a.edit, a.notice = newAnswerEdit(r, a.width), notice{}
		}
	case "d":
		if r, ok := a.selected(); ok {
			a.deleting = &r
		}
	}
	return a.scrolled(), nil
}

func newAnswerEdit(r memory.Remembered, width int) *answerEdit {
	e := &answerEdit{target: r, input: textinput.New()}
	e.input.Prompt = "> "
	e.input.Width = inputWidth(width)
	e.input.Cursor.SetMode(cursor.CursorStatic)
	if len(r.Options) > 0 {
		e.input.Placeholder = "type an option or its beginning, or pick one with ↑↓"
		for i, o := range r.Options {
			if o == r.Answer {
				e.choice = i
			}
		}
	} else {
		e.input.SetValue(r.Answer)
		e.input.CursorEnd()
	}
	e.input.Focus()
	return e
}

func (a answersTab) editKey(msg tea.KeyMsg, d Deps) (answersTab, tea.Cmd) {
	e := *a.edit
	options := e.target.Options
	switch msg.String() {
	case "esc":
		a.edit = nil
		return a, nil
	case "enter":
		text := strings.TrimSpace(e.input.Value())
		if len(options) > 0 {
			i := e.choice
			if text != "" {
				if i = questions.PickOption(text, options); i < 0 {
					e.refusal = fmt.Sprintf("%q is not one of the options, nor the beginning of just one.", text)
					a.edit = &e
					return a, nil
				}
			}
			text = options[i]
		}
		e.refusal = ""
		a.edit = &e
		return a, correctAnswer(d, e.target, text)
	case "up":
		if len(options) > 0 {
			e.choice = max(0, e.choice-1)
			a.edit = &e
			return a, nil
		}
	case "down":
		if len(options) > 0 {
			e.choice = min(len(options)-1, e.choice+1)
			a.edit = &e
			return a, nil
		}
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	a.edit = &e
	return a, cmd
}

func (a answersTab) deleteKey(msg tea.KeyMsg, d Deps) (answersTab, tea.Cmd) {
	switch msg.String() {
	case "y":
		r := *a.deleting
		a.deleting = nil
		return a, deleteAnswer(d, r)
	case "n", "esc":
		a.deleting = nil
	}
	return a, nil
}

// approveAnswer approves r as shown: if the memory's answer changed since (a
// run answered it again), nothing is approved and the list is read again.
func approveAnswer(d Deps, r memory.Remembered) tea.Cmd {
	return func() tea.Msg {
		mem := memory.New(d.Store, d.logger())
		live, err := mem.Get(r.ID)
		switch {
		case err != nil:
			return answerChangedMsg{notice: notice{"Could not read the answer memory: " + err.Error(), levelFail}}
		case live == nil:
			return answerChangedMsg{notice: notice{"That answer is no longer in the memory.", levelWarn}}
		case live.Answer != r.Answer:
			return answerChangedMsg{notice: notice{fmt.Sprintf("Not approved: the answer changed meanwhile, to %q. Check it again.", live.Answer), levelWarn}}
		}
		if _, err := mem.Approve(r.ID, ""); err != nil {
			if errors.Is(err, memory.ErrNothingToApprove) {
				return answerChangedMsg{notice: notice{"This question has no answer yet: e gives it one.", levelWarn}}
			}
			return answerChangedMsg{notice: notice{"Could not approve: " + err.Error(), levelFail}}
		}
		d.logger().Info("answer approved from the TUI", "id", r.ID, "question", r.Label)
		return answerChangedMsg{notice: notice{text: "Approved: " + r.Label}}
	}
}

// correctAnswer saves text as r's answer, the user's and approved.
func correctAnswer(d Deps, r memory.Remembered, text string) tea.Cmd {
	return func() tea.Msg {
		if text == "" {
			return answerChangedMsg{refusal: "type an answer (Delete forgets the question instead)"}
		}
		if _, err := memory.New(d.Store, d.logger()).Approve(r.ID, text); err != nil {
			if errors.Is(err, memory.ErrUnknownID) {
				return answerChangedMsg{notice: notice{"That answer is no longer in the memory.", levelWarn}}
			}
			return answerChangedMsg{refusal: err.Error()}
		}
		d.logger().Info("answer corrected from the TUI", "id", r.ID, "question", r.Label, "answer", text)
		return answerChangedMsg{notice: notice{text: fmt.Sprintf("Saved: %s = %s", r.Label, text)}}
	}
}

func deleteAnswer(d Deps, r memory.Remembered) tea.Cmd {
	return func() tea.Msg {
		err := memory.New(d.Store, d.logger()).Delete(r.ID)
		if err != nil && !errors.Is(err, memory.ErrUnknownID) {
			return answerChangedMsg{notice: notice{"Could not delete: " + err.Error(), levelFail}}
		}
		d.logger().Info("answer deleted from the TUI", "id", r.ID, "question", r.Label)
		return answerChangedMsg{notice: notice{text: "Deleted: " + r.Label + ". aja asks it again next time."}}
	}
}

// answerColumns lays out the list: kind, question, answer, who gave it.
const answerColumns = "  %-9s %-46s %-22s %s"

func (a answersTab) view(s styles, width int) string {
	other := map[string]string{"pending": "approved", "approved": "pending"}[a.shownState()]
	lines := []string{s.section.Render("Answers") + "  " + s.activeTab.Render(a.shownState()) + s.dim.Render("   tab shows the "+other+" ones")}
	switch {
	case a.problem != "":
		lines = append(lines, s.fail.Render(a.problem))
	case a.notice.text != "":
		lines = append(lines, s.byLevel(a.notice.level).Render(a.notice.text))
	}
	if !a.loaded {
		return strings.Join(append(lines, "Reading…"), "\n")
	}
	if len(a.answers) == 0 {
		empty := "No pending answers: the review queue is empty."
		if a.shownState() == "approved" {
			empty = "No approved answers yet."
		}
		return strings.Join(append(lines, "", s.dim.Render(empty)), "\n")
	}
	lines = append(lines, s.dim.Render(fmt.Sprintf(answerColumns, "kind", "question", "answer", "from")))
	n := a.listHeight()
	for i := a.top; i < a.top+n; i++ {
		if i >= len(a.answers) {
			lines = append(lines, "")
			continue
		}
		r := a.answers[i]
		mark := "  "
		if i == a.cursor {
			mark = s.key.Render("> ")
		}
		answer := r.Answer
		if strings.TrimSpace(answer) == "" {
			answer = "(none yet)"
		}
		lines = append(lines, mark+strings.TrimPrefix(fmt.Sprintf(answerColumns, r.Kind, clip(r.Label, 46), clip(answer, 22), sourceName(r.Source)), "  "))
	}
	return strings.Join(append(lines, a.detail(s, width)...), "\n")
}

// detail shows the selected answer in full, or the editor, or the delete
// confirmation.
func (a answersTab) detail(s styles, width int) []string {
	lines := []string{s.dim.Render(strings.Repeat("─", width))}
	r, ok := a.selected()
	if !ok {
		return lines
	}
	switch {
	case a.deleting != nil:
		return append(lines, s.warn.Render(fmt.Sprintf("Delete the answer to %q? aja asks the question again next time.", a.deleting.Label)),
			s.key.Render("y")+" deletes it · "+s.key.Render("n")+" keeps it")
	case a.edit != nil:
		e := a.edit
		lines = append(lines, s.section.Render("Answer: ")+e.target.Label, e.input.View())
		for i, o := range e.target.Options {
			mark := "  "
			if i == e.choice {
				mark = s.key.Render("> ")
			}
			lines = append(lines, mark+optionLabel(o))
		}
		if e.refusal != "" {
			lines = append(lines, s.fail.Render("Refused: "+e.refusal))
		}
		return lines
	}
	lines = append(lines, wrap(r.Label, width)...)
	answer := r.Answer
	if strings.TrimSpace(answer) == "" {
		answer = "none yet: e gives it one"
	}
	lines = append(lines, fmt.Sprintf("%s · answer: %s (%s) · used %d time(s)", r.Kind, answer, sourceName(r.Source), r.Uses))
	if len(r.Options) > 0 {
		labels := make([]string, len(r.Options))
		for i, o := range r.Options {
			labels[i] = optionLabel(o)
		}
		lines = append(lines, s.dim.Render("options: "+strings.Join(labels, " | ")))
	}
	if r.LastJobLink != "" {
		lines = append(lines, s.dim.Render("last job: "+r.LastJobLink))
	}
	return lines
}

// sourceName says who gave an answer.
func sourceName(source string) string {
	switch source {
	case "ai":
		return "the AI"
	case "user":
		return "you"
	case "form":
		return "the form"
	}
	return source
}

// clip cuts text to n characters, marking the cut.
func clip(text string, n int) string {
	if r := []rune(text); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return text
}
