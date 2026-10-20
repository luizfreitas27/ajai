//go:build browser

package apply

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/chrome"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/questions"
)

// checkboxGroupPage is a synthetic multipleChoice question as LinkedIn drew it in
// the failed runs: a single-answer group of checkboxes whose inputs take no
// pointer events, with their <label for> drawn as the visible options. CHECKED
// is what a test adds to the first option.
const checkboxGroupPage = `<html><body style="margin:0">
<div role="dialog" class="jobs-easy-apply-modal"><div data-test-form-element>
<fieldset data-test-form-builder-radio-button-form-component="true">
<legend><span data-test-form-builder-radio-button-form-component__title>Will you now or in the future require sponsorship for employment visa status?</span></legend>
<div style="position:relative;padding-left:30px">
  <input id="opt-yes" type="checkbox" class="fb-form-element__checkbox" CHECKED style="position:absolute;left:0;top:0;width:20px;height:20px;margin:0;pointer-events:none;opacity:0">
  <label for="opt-yes">Yes</label>
</div>
<div style="position:relative;padding-left:30px">
  <input id="opt-no" type="checkbox" class="fb-form-element__checkbox" style="position:absolute;left:0;top:0;width:20px;height:20px;margin:0;pointer-events:none;opacity:0">
  <label for="opt-no">No</label>
</div>
</fieldset>
</div></div></body></html>`

// openCheckboxGroup serves the page with the first option checked or not and
// opens it in a headless Chrome.
func openCheckboxGroup(t *testing.T, checked bool) browser.Page {
	t.Helper()
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	attr := ""
	if checked {
		attr = "checked"
	}
	page := strings.Replace(checkboxGroupPage, "CHECKED", attr, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	br, err := browser.Open(ctx, browser.Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Close() })
	pg := br.Main()
	if err := pg.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	return pg
}

// TestAChoiceWhoseInputsTakeNoPointerEventsIsPickedThroughItsLabel: the form
// reader sees the group as a choice with its two options, and picking "No"
// ticks it through its label and unticks the option that was ticked before, so
// a single-answer group never ends with two boxes ticked.
func TestAChoiceWhoseInputsTakeNoPointerEventsIsPickedThroughItsLabel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		checked bool
	}{{"nothing ticked", false}, {"Yes ticked before", true}} {
		t.Run(tc.name, func(t *testing.T) {
			pg := openCheckboxGroup(t, tc.checked)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			modal, err := pg.WaitElement(ctx, linkedin.ModalXPath, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			controls, err := ReadForm(modal)
			if err != nil {
				t.Fatal(err)
			}
			if len(controls) != 1 {
				t.Fatalf("controls: %+v", controls)
			}
			c := controls[0]
			if c.Question.Kind != questions.Radio || strings.Join(c.Question.Options, "|") != "Yes|No" || len(c.RadioInputs) != 2 {
				t.Fatalf("not read as a choice with its options: %+v", c.Question)
			}
			if err := (&Applier{}).applyDecision(ctx, c, questions.Decision{Action: questions.Choose, Option: 1}); err != nil {
				t.Fatalf("picking No: %v", err)
			}
			yes, _ := pg.Element("//input[@id='opt-yes']")
			no, _ := pg.Element("//input[@id='opt-no']")
			yesOn, _ := yes.Property("checked")
			noOn, _ := no.Property("checked")
			if noOn != "true" || yesOn == "true" {
				t.Errorf("after picking No: Yes checked=%q No checked=%q", yesOn, noOn)
			}
		})
	}
}
