//go:build browser

package apply

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/chrome"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/memory"
	"github.com/luizfreitas27/ajai/internal/pacing"
	"github.com/luizfreitas27/ajai/internal/questions"
	"github.com/luizfreitas27/ajai/internal/store"
)

// submittedPage is a job list behind an Easy Apply modal whose Submit draws
// LinkedIn's "Application sent" dialog over the whole page, as the live site
// does. A company name in the list holds the word "Done" ("Software Delivery
// Done Right", seen on 2026-10-20): it comes before the dialog in the document.
const submittedPage = `<html><body style="margin:0">
<ul><li><a id="card" href="#" class="job-card-container__link">Next job</a>
  <span>Sonatafy Technology | Software Delivery Done Right</span></li></ul>
<div id="apply" role="dialog" class="jobs-easy-apply-modal">
  <button aria-label="Submit application" onclick="sent()">Submit application</button>
</div>
<script>
function sent() {
  document.getElementById("apply").remove();
  document.body.insertAdjacentHTML("beforeend",
    '<div id="sent" style="position:fixed;inset:0;background:rgba(0,0,0,.6)">' +
    '<div role="dialog" class="artdeco-modal" aria-labelledby="h"><h2 id="h">Application sent</h2>' +
    '<button data-test-modal-close-btn aria-label="Dismiss" onclick="closeSent()">x</button>' +
    '<button onclick="closeSent()"><span>Done</span></button></div></div>');
}
function closeSent() { document.getElementById("sent").remove(); }
</script></body></html>`

// TestTheApplicationSentDialogIsClosedAfterSubmit: after Submit the dialog
// LinkedIn draws over the page is closed, so the next card can be clicked.
func TestTheApplicationSentDialogIsClosedAfterSubmit(t *testing.T) {
	bin, err := chrome.Find("")
	if err != nil {
		t.Skip("no Chrome on this machine:", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, submittedPage)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	br, err := browser.Open(ctx, browser.Options{Bin: bin, ProfileDir: t.TempDir(), HeadlessForTests: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	pg := br.Main()
	if err := pg.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	cfg := config.Defaults()
	pacer := pacing.New(config.Pacing{MaxPerHour: 15, MaxPerRun: 50, TypingDelayMinMs: 1, TypingDelayMaxMs: 2}, true)
	applier := &Applier{Page: pg, Pacer: pacer, Answerer: questions.New(cfg, memory.New(st, nil), nil, nil)}
	stub, err := pg.WaitElement(ctx, linkedin.ModalXPath, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	out, err := applier.Apply(ctx, linkedin.Card{ID: "fixture"}, stub, Options{})
	if err != nil || out.Kind != Applied {
		t.Fatalf("apply: %+v %v", out, err)
	}
	var missing *browser.ErrNotFound
	if _, err := pg.Element(`//*[@id="sent"]`); !errors.As(err, &missing) {
		t.Fatalf("the Application sent dialog is still drawn over the page (%v)", err)
	}
	card, err := pg.Element(`//*[@id="card"]`)
	if err != nil {
		t.Fatal(err)
	}
	if err := card.Click(); err != nil {
		t.Errorf("the next card could not be clicked: %v", err)
	}
}
