//go:build browser

package browser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/chrome"
)

// switchPage is a switch as LinkedIn's All filters panel draws it: the
// <input role="switch"> takes no pointer events, and its label, drawn over it
// as the toggle, is what a click lands on.
const switchPage = `<html><body style="margin:0">
<fieldset><h3>Easy Apply</h3>
<div style="position:relative;width:60px;height:30px">
  <input id="sw" type="checkbox" role="switch" style="position:absolute;inset:0;margin:0;width:60px;height:30px;pointer-events:none;opacity:0">
  <label for="sw" style="position:absolute;inset:0;background:#888;border-radius:15px"></label>
</div>
</fieldset></body></html>`

// TestASwitchUnderItsDrawingIsClickedWhereItShows drives a real headless
// Chrome: Click refuses the input (its pointer-events are none), and
// ClickWhereShown turns the switch on through the toggle drawn over it.
func TestASwitchUnderItsDrawingIsClickedWhereItShows(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, switchPage)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	br, err := Open(ctx, Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	pg := br.Main()
	if err := pg.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	sw, err := pg.Element(`//input[@role="switch"]`)
	if err != nil {
		t.Fatal(err)
	}
	if err := sw.Click(); err == nil {
		t.Log("Click went through: Chrome no longer refuses it, ClickWhereShown still must work")
	}
	if on, _ := sw.Property("checked"); on == "true" {
		t.Fatal("the switch is on before ClickWhereShown")
	}
	if err := sw.ClickWhereShown(); err != nil {
		t.Fatal(err)
	}
	if on, _ := sw.Property("checked"); on != "true" {
		t.Errorf("the switch is not on after ClickWhereShown: checked=%q", on)
	}
}
