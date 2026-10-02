//go:build browser

package apply

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/chrome"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/questions"
)

// TestReadAndFillTheCapturedModalInARealChrome serves the captured Easy Apply
// modal and drives it through the rod adapter: the one place the selectors,
// the form reader and the DOM actions meet a real browser.
func TestReadAndFillTheCapturedModalInARealChrome(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "linkedin", "testdata", "fixtures", "06_easy_apply_modal.html"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	br, err := browser.Open(ctx, browser.Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	pg := br.Main()
	if err := pg.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	modal, err := pg.WaitElement(ctx, linkedin.ModalXPath, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	controls, err := ReadForm(modal)
	if err != nil {
		t.Fatal(err)
	}
	// The capture holds 2 selects and 1 text input (selectors_test asserts the same statically).
	kinds := map[questions.Kind]int{}
	for _, c := range controls {
		kinds[c.Question.Kind]++
		if c.Question.Label == questions.UnknownTitle {
			t.Errorf("a control without a readable title: %+v", c.Question)
		}
	}
	if kinds[questions.Select] != 2 || kinds[questions.Text] != 1 {
		t.Fatalf("controls: %+v", kinds)
	}
	for _, c := range controls {
		switch c.Question.Kind {
		case questions.Text:
			if err := c.Element.Clear(); err != nil {
				t.Fatal(err)
			}
			for _, r := range "5551234567" {
				if err := c.Element.Input(string(r)); err != nil {
					t.Fatal(err)
				}
			}
			if v, _ := c.Element.Property("value"); v != "5551234567" {
				t.Errorf("typed value %q", v)
			}
		case questions.Select:
			if len(c.Question.Options) < 2 {
				t.Fatalf("select without options: %+v", c.Question)
			}
			want := c.Question.Options[1]
			if err := c.Element.SelectOption(want); err != nil {
				t.Fatal(err)
			}
			again, _ := ReadForm(modal)
			found := false
			for _, r := range again {
				if r.Question.Label == c.Question.Label && r.Question.Current == want {
					found = true
				}
			}
			if !found {
				t.Errorf("select %q did not read back %q", c.Question.Label, want)
			}
		}
	}
	// The modal's own buttons are where the applier expects them.
	if _, err := modal.Element(linkedin.SubmitButtonXPath); err != nil {
		t.Error("submit button not found inside the dialog")
	}
	if _, err := modal.Element(linkedin.ModalCloseXPath); err != nil {
		t.Error("close button not found inside the dialog")
	}
	shot := filepath.Join(t.TempDir(), "modal.png")
	if err := pg.Screenshot(shot); err != nil {
		t.Errorf("screenshot: %v", err)
	}
}
