//go:build browser

package browser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/chrome"
)

// switchPage is a switch as LinkedIn's All filters panel draws it: the
// <input role="switch"> takes no pointer events, and its label, drawn over it
// as the toggle, is what a click lands on. EXTRA is what a test adds.
const switchPage = `<html><body style="margin:0">
<fieldset><h3>Easy Apply</h3>
<div style="position:relative;width:60px;height:30px">
  <input id="sw" type="checkbox" role="switch" style="position:absolute;inset:0;margin:0;width:60px;height:30px;pointer-events:none;opacity:0">
  <label for="sw" style="position:absolute;inset:0;background:#888;border-radius:15px"></label>
</div>
</fieldset>EXTRA</body></html>`

// clickedMark is what an element a test must not see clicked adds to the page.
const clickedMark = `onclick="document.body.insertAdjacentHTML('beforeend', '<p id=clicked></p>')"`

// openPage serves the switch page with extra in it and opens it in a headless
// Chrome.
func openPage(t *testing.T, extra string) Page {
	t.Helper()
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Replace(switchPage, "EXTRA", extra, 1))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	br, err := Open(ctx, Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true})
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

// switchState is the switch's checked property.
func switchState(t *testing.T, pg Page) string {
	t.Helper()
	sw, err := pg.Element(`//input[@role="switch"]`)
	if err != nil {
		t.Fatal(err)
	}
	on, _ := sw.Property("checked")
	return on
}

// TestASwitchUnderItsDrawingIsClickedAtItsCentre drives a real headless
// Chrome: Click refuses the input (its pointer-events are none), as it did on
// LinkedIn, and ClickAtCentre turns the switch on through the toggle drawn
// over it.
func TestASwitchUnderItsDrawingIsClickedAtItsCentre(t *testing.T) {
	pg := openPage(t, "")
	sw, err := pg.Element(`//input[@role="switch"]`)
	if err != nil {
		t.Fatal(err)
	}
	if err := sw.Click(); err == nil {
		t.Fatal("Click went through: the page no longer refuses it as LinkedIn's did")
	}
	if on := switchState(t, pg); on == "true" {
		t.Fatal("the switch is on before ClickAtCentre")
	}
	if err := sw.ClickAtCentre(); err != nil {
		t.Fatal(err)
	}
	if on := switchState(t, pg); on != "true" {
		t.Errorf("the switch is not on after ClickAtCentre: checked=%q", on)
	}
}

// TestClickAtCentreNeverClicksWhatCoversTheElement: a button drawn over the
// switch (a panel's sticky footer) is not clicked, nor the spot of an element
// that takes no room; both are refused, saying what is wrong.
func TestClickAtCentreNeverClicksWhatCoversTheElement(t *testing.T) {
	for _, tc := range []struct{ name, extra, xpath, err string }{
		{"a footer over the switch",
			`<button class="footer show-results" ` + clickedMark + ` style="position:fixed;left:0;top:0;width:300px;height:300px">Show results</button>`,
			`//input[@role="switch"]`, "button.footer.show-results is drawn over the element"},
		{"an element with no room",
			`<span id="none" ` + clickedMark + ` style="position:absolute;left:10px;top:10px;width:0;height:0"></span>`,
			`//span[@id="none"]`, "takes no room"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := openPage(t, tc.extra)
			el, err := pg.Element(tc.xpath)
			if err != nil {
				t.Fatal(err)
			}
			if err := el.ClickAtCentre(); err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("ClickAtCentre: %v, want %q", err, tc.err)
			}
			var missing *ErrNotFound
			if _, err := pg.Element(`//p[@id="clicked"]`); !errors.As(err, &missing) {
				t.Errorf("something was clicked: %v", err)
			}
			if on := switchState(t, pg); on == "true" {
				t.Error("the switch turned on")
			}
		})
	}
}

// shortWait shortens actionWait for one test.
func shortWait(t *testing.T, d time.Duration) {
	t.Helper()
	was := actionWait
	actionWait = d
	t.Cleanup(func() { actionWait = was })
}

// later runs js on el once the action under test has started waiting.
func later(el Element, js string) {
	time.AfterFunc(300*time.Millisecond, func() { _, _ = el.(*rodElement).el.Eval(js) })
}

// TestAClickOrTypingThatCannotHappenGivesUpSayingWhy: rod waits without end
// for an element to be uncovered and enabled, as it did for a Next button
// LinkedIn kept disabled; ajai gives up within actionWait, saying why, and
// nothing is clicked or typed.
func TestAClickOrTypingThatCannotHappenGivesUpSayingWhy(t *testing.T) {
	shortWait(t, time.Second)
	for _, tc := range []struct {
		name, extra string
		then        string // js run on the element once the wait started, "" for none
		act         func(Element) error
		why         string
	}{
		{"a disabled button", `<button id="target" disabled ` + clickedMark + `>Next</button>`, "", Element.Click, "it stayed disabled"},
		{"a covered button", `<button id="target" ` + clickedMark + `>Next</button><div style="position:fixed;inset:0;background:#fff"></div>`, "",
			Element.Click, "something stayed drawn over it"},
		{"a disabled button the page replaces", `<button id="target" disabled ` + clickedMark + `>Next</button>`, "() => this.remove()",
			Element.Click, "it left the page"},
		{"a disabled input", `<input id="target" disabled>`, "", func(e Element) error { return e.Input("typed") }, "it stayed disabled"},
		{"clearing a disabled input", `<input id="target" disabled value="kept">`, "", Element.Clear, "it stayed disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := openPage(t, tc.extra)
			el, err := pg.Element(`//*[@id="target"]`)
			if err != nil {
				t.Fatal(err)
			}
			if tc.then != "" {
				later(el, tc.then)
			}
			start := time.Now()
			err = tc.act(el)
			if took := time.Since(start); took > 5*actionWait {
				t.Errorf("it took %s to give up", took)
			}
			if err == nil || !strings.Contains(err.Error(), "gave up after 1s: "+tc.why) {
				t.Errorf("%v, want it to give up because %s", err, tc.why)
			}
			var missing *ErrNotFound
			if _, err := pg.Element(`//p[@id="clicked"]`); !errors.As(err, &missing) {
				t.Errorf("something was clicked: %v", err)
			}
			if v, _ := el.Property("value"); v == "typed" || strings.Contains(tc.extra, `value="kept"`) && v != "kept" {
				t.Errorf("the input's value is %q", v)
			}
		})
	}
}

// TestAButtonEnabledWhileWaitedForIsClicked: the bounded wait still waits, as
// for a Next button LinkedIn enables once an upload is processed.
func TestAButtonEnabledWhileWaitedForIsClicked(t *testing.T) {
	shortWait(t, 5*time.Second)
	pg := openPage(t, `<button id="target" disabled `+clickedMark+`>Next</button>`)
	el, err := pg.Element(`//*[@id="target"]`)
	if err != nil {
		t.Fatal(err)
	}
	later(el, "() => { this.disabled = false }")
	if err := el.Click(); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Element(`//p[@id="clicked"]`); err != nil {
		t.Errorf("the button was not clicked: %v", err)
	}
}
