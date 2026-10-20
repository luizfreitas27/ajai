//go:build browser

package browser

import (
	"errors"
	"strings"
	"testing"
)

// shadowHost is a page whose button lives in an open shadow root, as LinkedIn's
// messaging overlay does inside #interop-outlet.
const shadowHost = `<div id="host"></div><script>
document.getElementById("host").attachShadow({mode: "open"}).innerHTML =
  '<button id="inside" onclick="document.body.insertAdjacentHTML(\'beforeend\', \'<p id=clicked></p>\')">In</button>';
</script>`

// TestShadowElementsReachWhatTheDocumentCannot finds the button through the
// host's shadow root, and not from the document.
func TestShadowElementsReachWhatTheDocumentCannot(t *testing.T) {
	pg := openPage(t, shadowHost+`<p id="plain"></p>`)
	var missing *ErrNotFound
	if _, err := pg.Element(`//button[@id="inside"]`); !errors.As(err, &missing) {
		t.Fatalf("the document sees into the shadow root: %v", err)
	}
	host, err := pg.Element(`//div[@id="host"]`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := host.ShadowElements(`//button[@id="inside"]`); err != nil || len(got) != 1 {
		t.Errorf("the button in the shadow root: %d found, %v", len(got), err)
	}
	plain, err := pg.Element(`//p[@id="plain"]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.ShadowElements(`//button`); err == nil {
		t.Error("an element without a shadow root gave one")
	}
}

// TestScriptClickRunsTheClickHandler clicks the button in the shadow root by
// script.
func TestScriptClickRunsTheClickHandler(t *testing.T) {
	pg := openPage(t, shadowHost)
	host, err := pg.Element(`//div[@id="host"]`)
	if err != nil {
		t.Fatal(err)
	}
	buttons, err := host.ShadowElements(`//button[@id="inside"]`)
	if err != nil || len(buttons) != 1 {
		t.Fatal(len(buttons), err)
	}
	if err := buttons[0].ScriptClick(); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Element(`//p[@id="clicked"]`); err != nil {
		t.Errorf("the click handler did not run: %v", err)
	}
}

// TestShiftEnterBreaksTheLineInATextBox types two lines into a contenteditable
// box, as LinkedIn's message box is, where a plain Enter may send.
func TestShiftEnterBreaksTheLineInATextBox(t *testing.T) {
	pg := openPage(t, `<div id="box" contenteditable="true" role="textbox"><p><br></p></div>`)
	box, err := pg.Element(`//div[@id="box"]`)
	if err != nil {
		t.Fatal(err)
	}
	if err := box.Input("one"); err != nil {
		t.Fatal(err)
	}
	if err := pg.Press("Shift+Enter"); err != nil {
		t.Fatal(err)
	}
	if err := box.Input("two"); err != nil {
		t.Fatal(err)
	}
	if got, _ := box.Text(); strings.TrimSpace(got) != "one\ntwo" {
		t.Errorf("the box holds %q, want two lines", got)
	}
}
