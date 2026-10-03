package tui

import (
	"strings"
	"testing"

	"github.com/luizfreitas27/ajai/internal/memory"
)

// remembered seeds the answer memory: an AI answer, a radio question nothing
// answered yet, a suggested checkbox (all pending) and an approved answer.
func (h *harness) remembered() (mem *memory.Memory, years, relocate, consent *memory.Remembered) {
	h.t.Helper()
	mem = memory.New(h.st, nil)
	must := func(r *memory.Remembered, err error) *memory.Remembered {
		h.t.Helper()
		if err != nil {
			h.t.Fatal(err)
		}
		return r
	}
	consent = must(mem.Remember("Send me updates", "checkbox", "checked", "form", "", nil))
	relocate = must(mem.Remember("Are you willing to relocate?", "radio", "", "form", "https://www.linkedin.com/jobs/view/2/", []string{"Yes", "No, not now"}))
	years = must(mem.Remember("Years of experience with Go?", "text", "3", "ai", "https://www.linkedin.com/jobs/view/1/", nil))
	must(mem.Remember("City", "text", "Recife", "user", "", nil))
	return mem, years, relocate, consent
}

func (h *harness) answersTab() { h.t.Helper(); h.press(tabKey(tabAnswers)) }

// selectAnswer moves the selection to the answer whose question starts with label.
func (h *harness) selectAnswer(label string) {
	h.t.Helper()
	h.press("g")
	for range 50 {
		for _, l := range strings.Split(h.screen(), "\n") {
			if strings.HasPrefix(l, "> ") && strings.Contains(l, label) {
				return
			}
		}
		h.press("down")
	}
	h.t.Fatalf("no answer %q:\n%s", label, h.screen())
}

func TestTheAnswersTabListsTheReviewQueueAndTheApprovedOnes(t *testing.T) {
	h := newHarness(t)
	h.remembered()
	h.open(100, 30)
	h.shows("2 Answers (3)")
	h.answersTab()
	h.shows("Answers  pending", "Years of experience with Go?", "(none yet)", "Send me updates", "the AI", "the form")
	if strings.Contains(h.screen(), "Recife") {
		t.Errorf("an approved answer among the pending ones:\n%s", h.screen())
	}
	h.selectAnswer("Are you willing")
	h.shows("radio · answer: none yet: e gives it one (the form)", "options: Yes | No, not now", "last job: https://www.linkedin.com/jobs/view/2/")
	h.golden("answers")
	h.press("tab")
	h.shows("Answers  approved", "City", "Recife", "you")
}

func TestApproveCorrectAndDelete(t *testing.T) {
	h := newHarness(t)
	mem, years, relocate, consent := h.remembered()
	h.open(100, 30)
	h.answersTab()

	h.selectAnswer("Years of experience")
	h.press("a")
	h.shows("Approved: Years of experience with Go?")
	if r, _ := mem.Get(years.ID); r.State != "approved" || r.Source != "ai" {
		t.Errorf("approved as shown, still the AI's: %+v", r)
	}
	if strings.Contains(h.screen(), "> Years") || strings.Contains(h.screen(), "  text      Years") {
		t.Errorf("an approved answer left the review queue:\n%s", h.screen())
	}

	h.selectAnswer("Are you willing")
	h.press("e")
	h.shows("Answer: Are you willing to relocate?", "Yes", "No, not now", "enter saves · esc cancels")
	h.press("q1", "maybe", "enter") // typed, not keys
	h.shows(`Refused: "q1maybe" is not one of the options`)
	h.press("ctrl+u", "no, n", "enter")
	h.shows("Saved: Are you willing to relocate? = No, not now")
	if r, _ := mem.Get(relocate.ID); r.Answer != "No, not now" || r.State != "approved" || r.Source != "user" {
		t.Errorf("%+v", r)
	}

	h.selectAnswer("Send me updates")
	h.press("e")
	h.shows("A checkbox takes only Approve (a) or Delete (d).")
	h.press("d")
	h.shows(`Delete the answer to "Send me updates"?`, "y deletes · n keeps")
	h.press("n")
	if r, _ := mem.Get(consent.ID); r == nil {
		t.Fatal("n keeps it")
	}
	h.press("d", "y")
	h.shows("Deleted: Send me updates. ajai asks it again next time.")
	if r, _ := mem.Get(consent.ID); r != nil {
		t.Errorf("not deleted: %+v", r)
	}
}

func TestAnAnswerThatChangedIsNotApprovedUnseenAndATextIsTyped(t *testing.T) {
	h := newHarness(t)
	mem, years, _, _ := h.remembered()
	h.open(100, 30)
	h.answersTab()
	h.selectAnswer("Years of experience")
	if _, err := mem.Remember("Years of experience with Go?", "text", "5", "ai", "", nil); err != nil { // a run answered again
		t.Fatal(err)
	}
	h.press("a")
	h.shows(`Not approved: the answer changed meanwhile, to "5". Check it again.`, "5")
	if r, _ := mem.Get(years.ID); r.State != "pending" {
		t.Fatalf("approved unseen: %+v", r)
	}

	h.selectAnswer("Years of experience")
	h.press("enter", "ctrl+u", "7", "enter")
	h.shows("Saved: Years of experience with Go? = 7")
	if r, _ := mem.Get(years.ID); r.Answer != "7" || r.Source != "user" || r.State != "approved" {
		t.Errorf("%+v", r)
	}
}

func TestThePickListIsUsedWithArrowsAndShowsEveryOption(t *testing.T) {
	h := newHarness(t)
	mem := memory.New(h.st, nil)
	options := []string{"Less than 1 year", "1 year", "2 years", "3 years", "4 years", "5 years", "6 to 10 years", "More than 10 years"}
	r, err := mem.Remember("How long have you used Kubernetes?", "select", "", "form", "", options)
	if err != nil {
		t.Fatal(err)
	}
	h.open(80, 24)
	h.answersTab()
	h.press("e")
	for _, o := range options {
		h.shows(o) // all eight, on the smallest terminal
	}
	h.golden("answers-edit")
	h.press("down", "down", "down", "enter")
	h.shows("Saved: How long have you used Kubernetes? = 3 years")

	// Typing moves the pick; an arrow afterwards wins over what was typed.
	h.press("tab", "e", "mor")
	if !strings.Contains(h.screen(), "> More than 10 years") {
		t.Errorf("the pick follows the typing:\n%s", h.screen())
	}
	h.press("up", "enter")
	if got, _ := mem.Get(r.ID); got.Answer != "6 to 10 years" {
		t.Errorf("the arrow's pick: %+v", got)
	}
}

func TestTheSelectionFollowsItsAnswerAndTheListKeepsUp(t *testing.T) {
	h := newHarness(t)
	mem, years, relocate, _ := h.remembered()
	h.open(100, 30)
	h.answersTab()
	h.selectAnswer("Are you willing")
	if _, err := mem.Remember("Are you willing to relocate?", "radio", "Yes", "ai", "", nil); err != nil { // a run answered it: it moves to the top
		t.Fatal(err)
	}
	h.press("a")
	h.shows(`Not approved: the answer changed meanwhile, to "Yes".`, "radio · answer: Yes (the AI)")
	h.press("a")
	if got, _ := mem.Get(relocate.ID); got.State != "approved" {
		t.Errorf("the answer shown is the one approved: %+v", got)
	}
	if got, _ := mem.Get(years.ID); got.State != "pending" {
		t.Errorf("another answer was approved: %+v", got)
	}

	// A run queues a question while the tab is shown: the list reads it on the next refresh.
	if _, err := mem.Remember("Notice period?", "text", "", "form", "", nil); err != nil {
		t.Fatal(err)
	}
	h.refresh()
	h.shows("Notice period?")
}

func TestEnterOnAnUnchangedAnswerApprovesItAndALateResultLeavesTheEditorOpen(t *testing.T) {
	h := newHarness(t)
	mem, years, _, consent := h.remembered()
	h.open(100, 30)
	h.answersTab()
	h.selectAnswer("Years of experience")
	h.press("e")
	h.send(answerChangedMsg{notice: notice{text: "an approval from before"}}) // not the editor's
	h.shows("Answer: Years of experience with Go?")
	h.press("enter")
	h.shows("Approved: Years of experience with Go?")
	if got, _ := mem.Get(years.ID); got.State != "approved" || got.Source != "ai" {
		t.Errorf("approved as shown, still the AI's: %+v", got)
	}

	if err := mem.Delete(consent.ID); err != nil { // gone elsewhere meanwhile
		t.Fatal(err)
	}
	h.selectAnswer("Send me updates")
	h.press("d")
	h.golden("answers-delete")
	h.press("y")
	h.shows("That answer was gone already.")
}

func TestAfterTheChecklistTheAnswersTabIsRead(t *testing.T) {
	h := newHarness(t)
	h.remembered()
	h.removeConfig()
	h.onRun = func([]string) { h.writeConfig() }
	h.open(100, 30)
	h.press(tabKey(tabAnswers)) // no config: the checklist stays, Answers comes after
	h.shows("Setup checklist")
	h.press("enter", "esc")
	h.shows("Answers  pending", "Years of experience with Go?")
}
