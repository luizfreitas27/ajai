package tui

import (
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/memory"
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
	h.shows("Deleted: Send me updates. aja asks it again next time.")
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
