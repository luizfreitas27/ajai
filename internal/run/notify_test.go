package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luizfreitas27/ajai/internal/apply"
	"github.com/luizfreitas27/ajai/internal/notify"
	"github.com/luizfreitas27/ajai/internal/store"
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
	for _, want := range []string{"ajai: run finished.", "Run: #1 (internacional, live)", "Applied: 1", "External: 0", "Failed: 0",
		"Skipped: 1", "Pending answers: 1 to review", "Ended: all search terms visited", "ajai answers review"} {
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
	if !strings.HasPrefix(msg, "ajai stopped with an error.") || !strings.Contains(msg, "the browser window was closed") ||
		!strings.Contains(msg, "Would apply: 0") || strings.Contains(msg, "run finished") || strings.Contains(msg, "Ended:") {
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
	if strings.Contains(msg, "Applied:") || strings.Contains(msg, "ajai answers review") {
		t.Errorf("%s", msg)
	}
	if got := StartFailureMessage(errors.New("your Chrome profile is in use")); !strings.Contains(got, "could not start") || !strings.Contains(got, "profile is in use") {
		t.Errorf("%s", got)
	}
	(&Runner{}).notifyEnd(Summary{}, nil) // no notifier: nothing happens, nothing panics
}

// telegramAt returns a real Telegram notifier pointed at a fake Bot API, the
// messages it receives, and the log it writes.
func telegramAt(t *testing.T) (*notify.Telegram, *[]string, *bytes.Buffer) {
	t.Helper()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body.Text)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	var log bytes.Buffer
	tg := notify.New("123:SECRET", "42", slog.New(slog.NewTextHandler(&log, nil))).WithAPIBase(srv.URL)
	return tg, &got, &log
}

func TestRunEndAndFatalErrorEachSendOneMessageOverTheBotAPI(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, _, _ := runner(t, site, cfg())
	tg, got, log := telegramAt(t)
	r.Notifier, r.Logger = tg, slog.New(slog.NewTextHandler(log, nil))
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || !strings.HasPrefix((*got)[0], "ajai: run finished.") || !strings.Contains((*got)[0], "Applied: 1") {
		t.Errorf("%q", *got)
	}
	failing := &failingSite{fakeSite{jobs: map[string]fakeJob{}, pages: [][]string{{}}}}
	r2, _, _ := runner(t, &failing.fakeSite, cfg())
	r2.Site, r2.Notifier = failing, tg
	if _, err := r2.Run(context.Background(), Options{DryRun: true}); err == nil {
		t.Fatal("expected a failure")
	}
	if len(*got) != 2 || !strings.HasPrefix((*got)[1], "ajai stopped with an error.") {
		t.Errorf("%q", *got)
	}
	if strings.Count((*got)[1], "the browser window was closed") != 1 {
		t.Errorf("the error must be said once:\n%s", (*got)[1])
	}
	if strings.Contains(log.String(), "SECRET") {
		t.Error("the token reached the log")
	}
}

func TestARunThatCannotBeRecordedOrCrashesStillSaysSo(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, st, _ := runner(t, site, cfg())
	rec := &recorder{}
	r.Notifier = rec
	_ = st.Close() // the database is gone: the run cannot even be recorded
	if _, err := r.Run(context.Background(), Options{}); err == nil {
		t.Fatal("expected a failure")
	}
	if len(rec.messages) != 1 || !strings.Contains(rec.messages[0], "could not start a run") {
		t.Errorf("%q", rec.messages)
	}
	// A bad flag is the typist's to see, not Telegram's.
	rec.messages = nil
	if _, err := r.Run(context.Background(), Options{Fast: true}); err == nil || len(rec.messages) != 0 {
		t.Errorf("%v %q", err, rec.messages)
	}

	site2 := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r2, st2, _ := runner(t, site2, cfg())
	rec2 := &recorder{}
	r2.Notifier = rec2
	r2.Applier = applierFunc(func() (apply.Outcome, error) { panic("index out of range") })
	func() {
		defer func() {
			p := recover()
			if c, ok := p.(*Crash); !ok || c.Value != "index out of range" {
				t.Errorf("the panic must go on, as a *Crash, after being reported: %v", p)
			}
		}()
		_, _ = r2.Run(context.Background(), Options{})
	}()
	if len(rec2.messages) != 1 || !strings.Contains(rec2.messages[0], "ajai crashed: index out of range") {
		t.Errorf("%q", rec2.messages)
	}
	// Recorded before crashing on: no run left "in progress", no live state left.
	if runs, _ := st2.ListRuns(1); len(runs) != 1 || runs[0].EndedAt == "" || runs[0].Reason != "crashed: index out of range" {
		t.Errorf("%+v", runs)
	}
	if _, err := ReadState(r2.StatePath); !errors.Is(err, ErrNoState) {
		t.Error("the live state must be removed")
	}
}

func TestReportStartFailureStaysQuietWhenTheUserCancelled(t *testing.T) {
	rec := &recorder{}
	ReportStartFailure(context.Background(), rec, errors.New("Chrome was not found"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ReportStartFailure(ctx, rec, errors.New("context canceled"))
	ReportStartFailure(context.Background(), rec, nil)
	ReportStartFailure(context.Background(), nil, errors.New("no notifier, no panic"))
	if len(rec.messages) != 1 || !rec.ctxLive[0] {
		t.Errorf("%q", rec.messages)
	}
}
