package apply

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/questions"
	"github.com/luizfreitas27/aja/internal/store"
)

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
	if err != nil || !out.Discarded || out.Reason != "dry run" || out.Applied {
		t.Fatalf("%+v %v", out, err)
	}
	if button.clicks != 1 || input.typed.String() != "5551234" || !input.cleared || selEl.selected != "Yes" || inputs[1].clicks != 1 {
		t.Errorf("form not filled: typed=%q selected=%q radio=%d", input.typed.String(), selEl.selected, inputs[1].clicks)
	}
	if out.Resume != "uploaded" {
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
	if err != nil || !out.Applied || submit.clicks != 1 || done.clicks != 1 {
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
	if err != nil || !out.Discarded || out.Reason != "unanswered questions" || len(out.Blocked) != 1 || !strings.Contains(out.Blocked[0], "Favourite colour? [Red, Blue]") {
		t.Fatalf("%+v %v", out, err)
	}
	if pg.pressed == nil || pg.pressed[0] != "Escape" {
		t.Errorf("with no close button the fallback is Escape: %v", pg.pressed)
	}
}

func TestAMissingModalAndAFailureAreReportedWithAScreenshot(t *testing.T) {
	pg := &page{root: el("")}
	a := &Applier{Page: pg, Pacer: quick(), Answerer: answerer(t), ScreenshotDir: t.TempDir()}
	if _, err := a.Apply(context.Background(), job, el("Apply"), Options{}); err != ErrModalMissing {
		t.Errorf("%v", err)
	}
	modal := modalWith() // no controls, no buttons at all
	pg, button := pageWith(modal)
	a.Page = pg
	out, err := a.Apply(context.Background(), job, button, Options{})
	if err == nil || !strings.Contains(err.Error(), "Next, Review or Submit") || !out.Discarded || len(pg.shots) != 1 || !strings.Contains(out.Reason, "screenshot") {
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
	if err == nil || !out.Discarded || out.Reason != "stopped by user" {
		t.Errorf("%+v %v", out, err)
	}
}
