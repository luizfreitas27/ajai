package run

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/luizfreitas27/aja/internal/apply"
	"github.com/luizfreitas27/aja/internal/store"
)

// recorder is a Notifier that keeps every message and the context it was sent with.
type recorder struct {
	mu       sync.Mutex
	messages []string
	ctxLive  []bool
}

func (r *recorder) Send(ctx context.Context, text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, text)
	r.ctxLive = append(r.ctxLive, ctx.Err() == nil)
	return true
}

// failingSite fails the search, a fatal error for the run.
type failingSite struct{ fakeSite }

func (f *failingSite) Search(context.Context, string) error {
	return errors.New("the browser window was closed")
}

func TestAFinishedRunSendsExactlyOneSummaryMessage(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go"), "2": job("2", "PHP Dev", "B", "PHP")}, pages: [][]string{{"1", "2"}}}
	r, st, _ := runner(t, site, cfg())
	rec := &recorder{}
	r.Notifier = rec
	if err := st.UpsertAnswer(store.Answer{Label: "Q", Normalised: "q", Kind: "text", Source: "form", State: "pending"}); err != nil {
		t.Fatal(err)
	}
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("%d messages: %q", len(rec.messages), rec.messages)
	}
	msg := rec.messages[0]
	for _, want := range []string{"aja: run finished.", "Run: #1 (internacional, live)", "Applied: 1", "External: 0", "Failed: 0",
		"Skipped: 1", "Pending answers: 1 to review", "Ended: all search terms visited", "aja answers review"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestAFatalErrorSendsExactlyOneMessageWithTheErrorAndTheCounts(t *testing.T) {
	site := &failingSite{fakeSite{jobs: map[string]fakeJob{}, pages: [][]string{{}}}}
	r, _, _ := runner(t, &site.fakeSite, cfg())
	r.Site = site
	rec := &recorder{}
	r.Notifier = rec
	if _, err := r.Run(context.Background(), Options{DryRun: true}); err == nil {
		t.Fatal("expected the run to fail")
	}
	if len(rec.messages) != 1 {
		t.Fatalf("%d messages: %q", len(rec.messages), rec.messages)
	}
	msg := rec.messages[0]
	if !strings.HasPrefix(msg, "aja stopped with an error.") || !strings.Contains(msg, "the browser window was closed") ||
		!strings.Contains(msg, "Would apply: 0") || strings.Contains(msg, "run finished") {
		t.Errorf("%s", msg)
	}
}

func TestCtrlCStillSendsTheSummaryOnALiveContext(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, _, _ := runner(t, site, cfg())
	rec := &recorder{}
	r.Notifier = rec
	ctx, cancel := context.WithCancel(context.Background())
	r.Applier = applierFunc(func() (apply.Outcome, error) {
		cancel()
		return apply.Outcome{Kind: apply.Applied, Resume: apply.PreviousResume}, nil
	})
	if _, err := r.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(rec.messages) != 1 || !rec.ctxLive[0] || !strings.Contains(rec.messages[0], "Ended: stopped by user") || !strings.Contains(rec.messages[0], "Applied: 1") {
		t.Errorf("%q live=%v", rec.messages, rec.ctxLive)
	}
}

func TestMessagesForADryRunAndAStartFailure(t *testing.T) {
	s := Summary{RunID: 7, Market: "brasil", DryRun: true, WouldApply: 4, Skipped: 2, Captured: 1, Reason: "--max reached"}
	msg := SummaryMessage(s)
	for _, want := range []string{"Run: #7 (brasil, dry run)", "Would apply: 4", "Captured: 1 answer(s)", "Ended: --max reached"} {
		if !strings.Contains(msg, want) {
			t.Errorf("lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Applied:") || strings.Contains(msg, "aja answers review") {
		t.Errorf("%s", msg)
	}
	if got := StartFailureMessage(errors.New("your Chrome profile is in use")); !strings.Contains(got, "could not start") || !strings.Contains(got, "profile is in use") {
		t.Errorf("%s", got)
	}
	(&Runner{}).notifyEnd(Summary{}, nil) // no notifier: nothing happens, nothing panics
}
