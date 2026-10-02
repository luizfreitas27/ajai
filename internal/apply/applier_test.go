package apply

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/questions"
	"github.com/luizfreitas27/aja/internal/store"
)

func init() {
	DiscardWait, TypeaheadSettle = 10*time.Millisecond, 0 // the fake DOM answers at once
}

func quick() *pacing.Pacer {
	c := config.Defaults().Pacing
	c.BetweenActionsMin, c.BetweenActionsMax, c.TypingDelayMinMs, c.TypingDelayMaxMs, c.BetweenJobsMin, c.BetweenJobsMax = 0, 0, 0, 0, 0, 0
	return pacing.New(c, true)
}

func answerer(t *testing.T) *questions.Answerer {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "aja.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	cfg.Personals.FirstName, cfg.Personals.LastName, cfg.Personals.Phone = "Jane", "Doe", "5551234"
	cfg.Questions.LegallyAuthorized, cfg.Questions.RequireVisa = "Yes", "No"
	cfg.Search.Terms = []string{"x"}
	return questions.New(cfg, memory.New(st, nil), nil, nil)
}

// Builders for question blocks, mirroring the Python fakes.
func textBlock(label, value string) (*node, *node) {
	input := el("").prop("value", value)
	return el("").with(linkedin.TextInputXPath, input).with(linkedin.LabelForXPath, el(label)), input
}

func selectBlock(label string, selected string, options ...string) (*node, *node) {
	sel := el("")
	for _, o := range options {
		opt := el(o)
		if o == selected {
			opt.prop("selected", "true")
		}
		sel.with(".//option", opt)
	}
	return el("").with(linkedin.SelectXPath, sel).with(linkedin.SelectTitleXPath, el(label)), sel
}

func radioBlock(label string, options ...string) (*node, []*node) {
	fieldset := el("").with(linkedin.RadioTitleXPath, el(label))
	var inputs []*node
	for i, o := range options {
		id := "opt" + string(rune('0'+i))
		in := el("").attr("id", id).attr("value", o)
		inputs = append(inputs, in)
		fieldset.with(linkedin.RadioInputsXPath, in).with(linkedin.OptionLabelXPath(id), el(o))
	}
	return el("").with(linkedin.RadioFieldsetXPath, fieldset), inputs
}

func checkboxBlock(visible, hidden string) (*node, *node) {
	box := el("")
	b := el("").with(linkedin.CheckboxXPath, box).with(linkedin.LabelForXPath, el(visible))
	if hidden != "" {
		b.with(linkedin.HiddenTitleXPath, el(hidden))
	}
	return b, box
}

func modalWith(blocks ...*node) *node {
	m := el("")
	for _, b := range blocks {
		m.with(linkedin.FormQuestionXPath, b)
	}
	return m
}

func pageWith(modal *node) (*page, *node) {
	button := el("Easy Apply")
	root := el("").with(linkedin.ModalXPath, modal)
	return &page{root: root}, button
}

var job = linkedin.Card{ID: "1", Title: "Go Dev", Company: "ACME", Location: "Remote", Link: "https://www.linkedin.com/jobs/view/1"}

func TestReadFormReadsEveryControlKind(t *testing.T) {
	text, _ := textBlock("Phone", "")
	sel, _ := selectBlock("Are you legally authorized to work?", "Select an option", "Select an option", "Yes", "No")
	radio, _ := radioBlock("Require visa?", "Yes", "No")
	box, _ := checkboxBlock("I agree to the terms", "Legal")
	follow := el("").with(linkedin.CheckboxXPath, el("").attr("id", linkedin.FollowCompanyCheckbox))
	area := el("").with(linkedin.TextareaXPath, el("").prop("value", "prefilled")).with(linkedin.LabelForXPath, el("Summary"))
	controls, err := ReadForm(modalWith(text, sel, radio, box, follow, area))
	if err != nil || len(controls) != 6 {
		t.Fatalf("%d controls, %v", len(controls), err)
	}
	want := []questions.Question{
		{Kind: questions.Text, Label: "Phone"},
		{Kind: questions.Select, Label: "Are you legally authorized to work?", Options: []string{"Select an option", "Yes", "No"}, Current: "Select an option"},
		{Kind: questions.Radio, Label: "Require visa?", Options: []string{"Yes", "No"}},
		{Kind: questions.Checkbox, Label: "I agree to the terms", GroupTitle: "Legal"},
		{},
		{Kind: questions.Textarea, Label: "Summary", Current: "prefilled"},
	}
	for i, c := range controls {
		if i == 4 {
			if !c.FollowCompany {
				t.Error("follow-company box not flagged")
			}
			continue
		}
		got := c.Question
		if got.Kind != want[i].Kind || got.Label != want[i].Label || got.Current != want[i].Current || got.GroupTitle != want[i].GroupTitle || strings.Join(got.Options, "|") != strings.Join(want[i].Options, "|") {
			t.Errorf("control %d: %+v", i, got)
		}
	}
}

func TestADryRunFillsEverythingAndDiscardsAtReview(t *testing.T) {
	text, input := textBlock("Phone", "")
	sel, selEl := selectBlock("Are you legally authorized to work?", "Select an option", "Select an option", "Yes", "No")
	radio, inputs := radioBlock("Require visa?", "Yes", "No")
	modal := modalWith(text, sel, radio)
	modal.with(linkedin.SubmitButtonXPath, el("Submit application")) // single-page form: Review page straight away
	modal.with(linkedin.ResumeUploadInputXPath, el(""))
	pg, button := pageWith(modal)
	closeBtn, discardBtn := el("x"), el("Discard")
	discardBtn.onClick = func() { modal.visible = false } // confirming Discard closes the dialog
	pg.root.with(strings.TrimPrefix(linkedin.ModalCloseXPath, "."), closeBtn).with(strings.TrimPrefix(linkedin.DiscardButtonXPath, "."), discardBtn)

	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t)}
	out, err := a.Apply(context.Background(), job, button, Options{DryRun: true, ResumePath: "/tmp/cv.pdf"})
	if err != nil || out.Kind != DryRun {
		t.Fatalf("%+v %v", out, err)
	}
	if button.clicks != 1 || input.typed.String() != "5551234" || !input.cleared || selEl.selected != "Yes" || inputs[1].clicks != 1 {
		t.Errorf("form not filled: typed=%q selected=%q radio=%d", input.typed.String(), selEl.selected, inputs[1].clicks)
	}
	if out.Resume != "cv.pdf" {
		t.Errorf("resume %q", out.Resume)
	}
	if closeBtn.clicks != 1 || discardBtn.clicks != 1 {
		t.Errorf("discard sequence: close %d discard %d", closeBtn.clicks, discardBtn.clicks)
	}
	if len(out.Answered) != 3 {
		t.Errorf("%v", out.Answered)
	}
}

func TestALiveRunSubmitsAndSetsTheFollowBox(t *testing.T) {
	text, _ := textBlock("Phone", "")
	modal := modalWith(text)
	submit := el("Submit application")
	modal.with(linkedin.SubmitButtonXPath, submit)
	followBox := el("").prop("checked", "true")
	followLabel := el("Follow")
	modal.with(".//input[@id='"+linkedin.FollowCompanyCheckbox+"' and @type='checkbox']", followBox).with(".//label[@for='"+linkedin.FollowCompanyCheckbox+"']", followLabel)
	pg, button := pageWith(modal)
	done := el("Done")
	pg.root.with(linkedin.TextXPath("span", "Done"), done)

	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t)}
	out, err := a.Apply(context.Background(), job, button, Options{FollowCompanies: false})
	if err != nil || out.Kind != Applied || submit.clicks != 1 || done.clicks != 1 || out.Resume != PreviousResume {
		t.Fatalf("%+v %v submit=%d done=%d", out, err, submit.clicks, done.clicks)
	}
	if followLabel.clicks != 1 {
		t.Error("the ticked follow box must be unticked when follow_companies is off")
	}
}

func TestUnansweredQuestionsStallTheFormAndItIsDiscarded(t *testing.T) {
	quiz, _ := radioBlock("Favourite colour?", "Red", "Blue")
	modal := modalWith(quiz)
	modal.with(linkedin.NextButtonXPath, el("Next")) // Next stays on the same page: LinkedIn refuses to advance
	pg, button := pageWith(modal)
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t)}
	out, err := a.Apply(context.Background(), job, button, Options{})
	if err != nil || out.Kind != Unanswered || len(out.Blocked) != 1 || !strings.Contains(out.Blocked[0], "Favourite colour? [Red, Blue]") {
		t.Fatalf("%+v %v", out, err)
	}
	if pg.pressed == nil || pg.pressed[0] != "Escape" {
		t.Errorf("with no close button the fallback is Escape: %v", pg.pressed)
	}
	if len(out.Answered) != 0 {
		t.Errorf("nothing was answered, and nothing twice: %v", out.Answered)
	}
}

func TestAnsweredLinesAreRecordedOnceAcrossPasses(t *testing.T) {
	text, _ := textBlock("Phone", "")
	quiz, _ := radioBlock("Favourite colour?", "Red", "Blue")
	modal := modalWith(text, quiz).with(linkedin.NextButtonXPath, el("Next"))
	pg, button := pageWith(modal)
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t)}
	out, _ := a.Apply(context.Background(), job, button, Options{})
	if out.Kind != Unanswered || len(out.Answered) != 1 || out.Answered[0] != "Phone: 5551234" {
		t.Errorf("%+v", out)
	}
}

func TestAMultiPageFormWalksNextToReviewAndFillsATextareaAndATypeahead(t *testing.T) {
	// Page 1 holds a textarea and a city typeahead; clicking Next swaps the modal's blocks for page 2 (Submit).
	area := el("").with(linkedin.TextareaXPath, el("").prop("value", "")).with(linkedin.LabelForXPath, el("Summary"))
	city, cityInput := textBlock("City", "")
	modal := modalWith(area, city)
	next := el("Next")
	next.onClick = func() {
		modal.children[linkedin.FormQuestionXPath] = nil
		delete(modal.children, linkedin.NextButtonXPath)
		modal.with(linkedin.SubmitButtonXPath, el("Submit application"))
	}
	modal.with(linkedin.NextButtonXPath, next)
	pg, button := pageWith(modal)
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t)}
	a.Answerer.Cfg.Questions.Summary = "Summary text"
	a.Answerer.Cfg.Personals.City = "Lisbon"
	out, err := a.Apply(context.Background(), job, button, Options{DryRun: true})
	if err != nil || out.Kind != DryRun || next.clicks != 1 {
		t.Fatalf("%+v %v next=%d", out, err, next.clicks)
	}
	if cityInput.typed.String() != "Lisbon" || strings.Join(pg.pressed, ",") != "ArrowDown,Enter,Escape" {
		t.Errorf("typeahead: typed %q keys %v", cityInput.typed.String(), pg.pressed)
	}
	if strings.Join(out.Answered, "|") != "Summary: Summary text|City: Lisbon" {
		t.Errorf("%v", out.Answered)
	}
}

func TestReadFormReadsCheckedRadiosAndTickedBoxes(t *testing.T) {
	radio, inputs := radioBlock("Shift", "Day", "Night")
	inputs[1].prop("checked", "true")
	box, input := checkboxBlock("I agree", "")
	input.prop("checked", "true")
	controls, _ := ReadForm(modalWith(radio, box))
	if controls[0].Question.Current != "Night" || controls[1].Question.Current != "checked" || controls[1].Question.GroupTitle != questions.UnknownTitle {
		t.Errorf("%+v %+v", controls[0].Question, controls[1].Question)
	}
}

func TestAMissingModalAndAFailureAreReportedWithAScreenshot(t *testing.T) {
	pg := &page{root: el("")}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), ScreenshotDir: t.TempDir()}
	out, err := a.Apply(context.Background(), job, el("Apply"), Options{})
	if !errors.Is(err, ErrModalMissing) || out.Kind != Failed || out.Screenshot == "" || len(pg.shots) != 1 {
		t.Errorf("a missing modal is a failure with a screenshot: %+v %v", out, err)
	}
	modal := modalWith() // no controls, no buttons at all
	pg, button := pageWith(modal)
	a.Page = pg
	out, err = a.Apply(context.Background(), job, button, Options{})
	if err == nil || !strings.Contains(err.Error(), "Next, Review or Submit") || out.Kind != Failed || out.Stage != "finding Next, Review or Submit" || len(pg.shots) != 1 || !strings.HasPrefix(out.Screenshot, "1-failed-") {
		t.Errorf("%+v %v shots=%v", out, err, pg.shots)
	}
}

func TestACancelledContextDiscardsAndReturns(t *testing.T) {
	text, _ := textBlock("Phone", "")
	modal := modalWith(text).with(linkedin.SubmitButtonXPath, el("Submit"))
	pg, button := pageWith(modal)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t)}
	out, err := a.Apply(ctx, job, button, Options{})
	if err == nil || out.Kind != Stopped {
		t.Errorf("%+v %v", out, err)
	}
}

// fakeHelper plays the person at the keyboard: it runs fill, then returns err.
type fakeHelper struct {
	calls []HelpRequest
	fill  func()
	err   error
}

func (h *fakeHelper) Help(_ context.Context, req HelpRequest) error {
	h.calls = append(h.calls, req)
	if h.fill != nil {
		h.fill()
	}
	return h.err
}

func TestTheHelpPauseCapturesWhatTheUserFilledAndTheFormGoesThrough(t *testing.T) {
	quiz, inputs := radioBlock("Favourite colour?", "Red", "Blue")
	lang, langInput := textBlock("Favourite language", "")
	modal := modalWith(quiz, lang)
	next := el("Next")
	modal.with(linkedin.NextButtonXPath, next)
	pg, button := pageWith(modal)
	helper := &fakeHelper{fill: func() {
		inputs[1].prop("checked", "true") // the user picks Blue...
		langInput.prop("value", "Go")     // ...types Go...
		// ...and once both are filled LinkedIn's Next leads to the Review page.
		next.onClick = func() {
			delete(modal.children, linkedin.NextButtonXPath)
			modal.with(linkedin.SubmitButtonXPath, el("Submit application"))
		}
	}}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper, ScreenshotDir: t.TempDir()}
	out, err := a.Apply(context.Background(), job, button, Options{DryRun: true})
	if err != nil || out.Kind != DryRun || out.Captured != 2 || len(helper.calls) != 1 {
		t.Fatalf("%+v %v calls=%d", out, err, len(helper.calls))
	}
	req := helper.calls[0]
	if req.Job.ID != "1" || len(req.Questions) != 2 || !strings.Contains(req.Questions[0], "Favourite") || !strings.Contains(req.Screenshot, "1-help-") {
		t.Errorf("%+v", req)
	}
	mem := a.Answerer.Memory
	for label, kind := range map[string]string{"Favourite colour?": "radio", "Favourite language": "text"} {
		found, _, _ := mem.Lookup(label, kind, false)
		if found == nil || found.State != "approved" || found.Source != "user" {
			t.Errorf("%s: %+v", label, found)
		}
	}
}

func TestNoHelpMeansTheJobIsSkippedAndTheQuestionsQueuedForReview(t *testing.T) {
	quiz, _ := radioBlock("Favourite colour?", "Red", "Blue")
	modal := modalWith(quiz).with(linkedin.NextButtonXPath, el("Next"))
	pg, button := pageWith(modal)
	helper := &fakeHelper{err: errors.New("nobody answered in time")}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper}
	out, err := a.Apply(context.Background(), job, button, Options{})
	if err != nil || out.Kind != Unanswered || out.Queued != 1 || len(helper.calls) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	pending, _ := a.Answerer.Memory.(*memory.Memory).List("pending")
	if len(pending) != 1 || pending[0].Label != "Favourite colour?" || pending[0].Answer != "" || pending[0].Source != "form" {
		t.Errorf("%+v", pending)
	}
	// Unattended (no helper): skipped at once, queued just the same, never twice.
	a.Helper = nil
	modal2 := modalWith(func() *node { n, _ := radioBlock("Favourite colour?", "Red", "Blue"); return n }()).with(linkedin.NextButtonXPath, el("Next"))
	pg2, button2 := pageWith(modal2)
	a.Page = pg2
	out, _ = a.Apply(context.Background(), job, button2, Options{})
	if out.Kind != Unanswered || out.Queued != 0 {
		t.Errorf("already queued: %+v", out)
	}
}

func TestAFieldTheBotFilledIsNotCapturedEvenWhenTheUserChangesIt(t *testing.T) {
	quiz, _ := radioBlock("Favourite colour?", "Red", "Blue")
	first, firstInput := textBlock("First name", "") // the bot fills this one itself
	modal := modalWith(quiz, first).with(linkedin.NextButtonXPath, el("Next"))
	pg, button := pageWith(modal)
	helper := &fakeHelper{fill: func() {
		// The fake input keeps what was typed in .typed; mirror it into the value the
		// reader sees, then let the user correct it.
		firstInput.prop("value", firstInput.typed.String())
		firstInput.prop("value", "Janet")
	}}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper}
	out, _ := a.Apply(context.Background(), job, button, Options{})
	if out.Captured != 0 {
		t.Errorf("%+v", out)
	}
	if found, _ := a.Answerer.Memory.(*memory.Memory).Find("First name", "text"); found != nil {
		t.Errorf("a control the bot had filled was captured: %+v", found)
	}
}

func TestAFormThatCannotBeReReadAfterThePauseCapturesNothing(t *testing.T) {
	quiz, _ := radioBlock("Favourite colour?", "Red", "Blue")
	modal := modalWith(quiz).with(linkedin.NextButtonXPath, el("Next"))
	pg, button := pageWith(modal)
	helper := &fakeHelper{fill: func() { modal.failElements = true }}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper}
	out, err := a.Apply(context.Background(), job, button, Options{})
	if out.Captured != 0 || out.Kind != Failed || err == nil || !strings.Contains(err.Error(), "reading the form") {
		t.Errorf("%+v %v", out, err)
	}
}

func TestAConfiguredAnswerThatMatchesNoOptionIsShownAsAConfigProblemAndNotCaptured(t *testing.T) {
	sel, selEl := selectBlock("Gender", "Select an option", "Select an option", "Male", "Female")
	modal := modalWith(sel).with(linkedin.NextButtonXPath, el("Next"))
	pg, button := pageWith(modal)
	var shown []string
	helper := &fakeHelper{}
	helper.fill = func() {
		shown = helper.calls[0].Questions
		for _, o := range selEl.children[".//option"] {
			o.props["selected"] = map[bool]string{true: "true", false: ""}[o.text == "Female"]
		}
	}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper}
	a.Answerer.Cfg.Personals.Gender = "Nonbinary"
	out, _ := a.Apply(context.Background(), job, button, Options{})
	if len(shown) != 1 || !strings.Contains(shown[0], `no option matched the configured answer "Nonbinary"; fix it in the config`) {
		t.Errorf("%v", shown)
	}
	if out.Captured != 0 {
		t.Errorf("an answer the memory would never be asked for was captured: %+v", out)
	}
}

func TestAFieldThatAppearedAfterThePauseIsNotCaptured(t *testing.T) {
	quiz, _ := radioBlock("Favourite colour?", "Red", "Blue")
	modal := modalWith(quiz).with(linkedin.NextButtonXPath, el("Next"))
	pg, button := pageWith(modal)
	helper := &fakeHelper{fill: func() {
		// The user clicked Next anyway: a new page with LinkedIn's prefill replaces the form.
		prefilled, _ := textBlock("Mobile phone number", "5550001234")
		modal.children[linkedin.FormQuestionXPath] = []*node{prefilled}
	}}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper}
	out, _ := a.Apply(context.Background(), job, button, Options{})
	if out.Captured != 0 {
		t.Errorf("%+v", out)
	}
	if all, _ := a.Answerer.Memory.(*memory.Memory).List("approved"); len(all) != 0 {
		t.Errorf("%+v", all)
	}
}

func TestASensitiveAnswerTheUserTypedIsNotCapturedButNotWipedEither(t *testing.T) {
	clearance, clearanceInput := textBlock("Do you hold an active security clearance?", "")
	modal := modalWith(clearance)
	next := el("Next")
	modal.with(linkedin.NextButtonXPath, next)
	pg, button := pageWith(modal)
	helper := &fakeHelper{fill: func() {
		clearanceInput.prop("value", "Yes, TS/SCI")
		next.onClick = func() {
			delete(modal.children, linkedin.NextButtonXPath)
			modal.with(linkedin.SubmitButtonXPath, el("Submit application"))
		}
	}}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper}
	// With overwrite on, the next pass would otherwise clear a field it cannot answer.
	a.Answerer.Cfg.Questions.OverwritePrevious = true
	out, err := a.Apply(context.Background(), job, button, Options{DryRun: true})
	if err != nil || out.Kind != DryRun || out.Captured != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	if clearanceInput.cleared {
		t.Error("the user's answer was wiped by the next pass")
	}
	if all, _ := a.Answerer.Memory.(*memory.Memory).List(""); len(all) != 0 {
		t.Errorf("a sensitive answer was remembered: %+v", all)
	}
}

func TestPartialFillsGetSeveralPausesAndThenTheJobIsGivenUpNotFailed(t *testing.T) {
	var blocks []*node
	var inputs []*node
	for _, label := range []string{"Q one", "Q two", "Q three", "Q four", "Q five", "Q six", "Q seven", "Q eight"} {
		b, in := textBlock(label, "")
		blocks, inputs = append(blocks, b), append(inputs, in)
	}
	modal := modalWith(blocks...).with(linkedin.NextButtonXPath, el("Next"))
	pg, button := pageWith(modal)
	filledSoFar := 0
	helper := &fakeHelper{}
	helper.fill = func() { // one question per pause
		inputs[filledSoFar].prop("value", "answer")
		filledSoFar++
	}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), Helper: helper}
	out, err := a.Apply(context.Background(), job, button, Options{})
	if err != nil || out.Kind != Unanswered || len(helper.calls) != MaxHelpPauses || out.Captured != MaxHelpPauses {
		t.Fatalf("%+v %v pauses=%d", out, err, len(helper.calls))
	}
	if len(out.Blocked) != 8-MaxHelpPauses || out.Queued != 8-MaxHelpPauses {
		t.Errorf("the rest go to the review queue: %+v", out)
	}
}
