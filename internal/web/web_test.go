package web_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/logging"
	"github.com/luizfreitas27/ajai/internal/memory"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/store"
	"github.com/luizfreitas27/ajai/internal/web"
)

// fakeLauncher records the arguments of every run it is asked to start.
type fakeLauncher struct{ calls [][]string }

func (f *fakeLauncher) Start(args []string) (int, <-chan error, error) {
	f.calls = append(f.calls, args)
	return 4242, make(chan error), nil
}

type fixture struct {
	t        *testing.T
	dataDir  string
	st       *store.Store
	launcher *fakeLauncher
	srv      *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{t: t, dataDir: dir, st: st, launcher: &fakeLauncher{}}
	h := web.New(web.Deps{DataDir: dir, LogDir: filepath.Join(dir, "logs"), Store: st, Launcher: f.launcher, Every: 10 * time.Millisecond})
	f.srv = httptest.NewServer(h)
	t.Cleanup(f.srv.Close)
	return f
}

// do sends a request with a JSON body (nil: none) and returns status and body.
func (f *fixture) do(method, path, body string, hdr map[string]string) (int, string) {
	f.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, r)
	if err != nil {
		f.t.Fatal(err)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, body)
	}
	return m
}

// Case 1: only a loopback address is served, and only loopback Host and Origin are let through.
func TestOnlyLoopbackAddressesAreServed(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7077", ":7077", "192.168.1.20:7077", "example.com:7077"} {
		if err := web.ValidAddr(addr); err == nil {
			t.Errorf("ValidAddr(%q) = nil, want an error", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:7077", "127.0.0.2:0", "localhost:7077", "[::1]:7077"} {
		if err := web.ValidAddr(addr); err != nil {
			t.Errorf("ValidAddr(%q) = %v, want nil", addr, err)
		}
	}
}

func TestAForeignHostOrOriginIsForbidden(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.do("GET", "/api/status", "", map[string]string{"Host": "evil.example"}); code != http.StatusForbidden {
		t.Errorf("foreign Host: %d, want 403", code)
	}
	if code, _ := f.do("GET", "/api/status", "", map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Errorf("foreign Origin: %d, want 403", code)
	}
	if code, _ := f.do("GET", "/api/status", "", map[string]string{"Origin": "http://localhost:5173"}); code != http.StatusOK {
		t.Errorf("loopback Origin of a dev server: %d, want 200", code)
	}
}

// Case 2: no run in progress: no holder, and the last runs.
func TestStatusWithNoRunShowsNoHolderAndTheLastRuns(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, err := f.st.StartRun(ctx, "brasil", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.FinishRun(ctx, store.Run{ID: id, Applied: 2, Reason: "end of the terms"}); err != nil {
		t.Fatal(err)
	}
	code, body := f.do("GET", "/api/status", "", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	m := decode(t, body)
	if m["running"] != false || m["holder"] != nil || m["kind"] != "idle" {
		t.Errorf("status with no run = %s", body)
	}
	runs, _ := m["last_runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("last_runs = %s, want the one run", body)
	}
	if r := runs[0].(map[string]any); r["market"] != "brasil" || r["applied"] != float64(2) || r["dry_run"] != true {
		t.Errorf("last run = %v", r)
	}
}

// Case 3: jobs filtered by text and paged.
func TestJobsFilterByTextAndPage(t *testing.T) {
	f := newFixture(t)
	for _, j := range []store.Job{
		{JobID: "1", Outcome: store.Applied, Title: "Go Developer", Company: "Acme", WorkLocation: "São Paulo"},
		{JobID: "2", Outcome: store.Skipped, Title: "Go Engineer", Company: "Beta", WorkLocation: "Remote"},
		{JobID: "3", Outcome: store.Applied, Title: "Data Analyst", Company: "São Miguel", WorkLocation: "Recife"},
	} {
		if err := f.st.UpsertJob(j); err != nil {
			t.Fatal(err)
		}
	}
	code, body := f.do("GET", "/api/jobs?text=SÃO", "", nil)
	if code != http.StatusOK {
		t.Fatalf("jobs %d: %s", code, body)
	}
	var jobs []map[string]any
	if err := json.Unmarshal([]byte(body), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Errorf("text=SÃO: %d jobs, want 2 (São Paulo and São Miguel): %s", len(jobs), body)
	}
	_, body = f.do("GET", "/api/jobs?limit=1&offset=1", "", nil)
	if err := json.Unmarshal([]byte(body), &jobs); err != nil || len(jobs) != 1 || jobs[0]["job_id"] != "2" {
		t.Errorf("limit=1&offset=1 = %s, want the second job, newest first", body)
	}
}

// Case 4: a message is approved only with the digest of the text shown.
func TestMessageApprovalNeedsTheDigestOfTheTextShown(t *testing.T) {
	f := newFixture(t)
	if err := f.st.UpsertJob(store.Job{JobID: "1", Outcome: store.Applied, Title: "Dev", Company: "Acme"}); err != nil {
		t.Fatal(err)
	}
	id, err := f.st.InsertMessage(store.Message{JobID: "1", RecipientName: "Ana", RecipientURL: "https://www.linkedin.com/in/ana", Text: "Oi Ana"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/messages/" + strconv.FormatInt(id, 10) + "/approve"
	if code, body := f.do("POST", path, `{"digest":"00000000"}`, nil); code != http.StatusConflict {
		t.Errorf("wrong digest: %d %s, want 409", code, body)
	}
	if m, _ := f.st.GetMessage(id); m.State != store.MessageQueued {
		t.Errorf("a refused approval changed the state to %s", m.State)
	}
	if code, body := f.do("POST", path, `{"digest":"`+messages.Digest("Oi Ana")+`"}`, nil); code != http.StatusOK {
		t.Errorf("right digest: %d %s, want 200", code, body)
	}
	if m, _ := f.st.GetMessage(id); m.State != store.MessageApproved {
		t.Errorf("state after approval = %s", m.State)
	}
}

// Case 5: an answer approval: a select answer must be one of its options, and the digest must match.
func TestAnswerApprovalChecksOptionsAndDigest(t *testing.T) {
	f := newFixture(t)
	mem := memory.New(f.st, nil)
	a, err := mem.Remember("Do you know Go?", "select", "Yes", "ai", "", []string{"Yes", "No"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/answers/" + a.ID + "/approve"
	if code, body := f.do("POST", path, `{"digest":"`+messages.Digest("Yes")+`","answer":"maybe"}`, nil); code != http.StatusBadRequest {
		t.Errorf("an answer that is no option: %d %s, want 400", code, body)
	}
	if code, body := f.do("POST", path, `{"digest":"00000000"}`, nil); code != http.StatusConflict {
		t.Errorf("digest mismatch: %d %s, want 409", code, body)
	}
	if got, _ := mem.Get(a.ID); got.State != "pending" {
		t.Errorf("refused approvals changed the entry: %+v", got)
	}
	if code, body := f.do("POST", path, `{"digest":"`+messages.Digest("Yes")+`","answer":"no"}`, nil); code != http.StatusOK {
		t.Errorf("a option, a beginning of one: %d %s, want 200", code, body)
	}
	if got, _ := mem.Get(a.ID); got.State != "approved" || got.Answer != "No" {
		t.Errorf("approved entry = %+v, want approved answer No", got)
	}
}

// Case 5b: a blank approval leaves a text question empty; a checkbox cannot be blank.
func TestBlankApprovalLeavesATextQuestionEmptyAndRefusesACheckbox(t *testing.T) {
	f := newFixture(t)
	mem := memory.New(f.st, nil)
	text, err := mem.Remember("If your answer was 'Other' please specify. If NOT leave blank.", "text", "", "form", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	box, err := mem.Remember("I agree to the terms", "checkbox", "", "form", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := f.do("POST", "/api/answers/"+box.ID+"/approve", `{"digest":"`+messages.Digest("")+`","blank":true}`, nil); code != http.StatusBadRequest {
		t.Errorf("a checkbox left blank: %d %s, want 400", code, body)
	}
	if got, _ := mem.Get(box.ID); got.State != "pending" {
		t.Errorf("refused blank changed the checkbox: %+v", got)
	}
	if code, body := f.do("POST", "/api/answers/"+text.ID+"/approve", `{"digest":"`+messages.Digest("")+`","blank":true}`, nil); code != http.StatusOK {
		t.Errorf("a blank approval: %d %s, want 200", code, body)
	}
	if got, _ := mem.Get(text.ID); got.State != "approved" || got.Answer != "" {
		t.Errorf("approved entry = %+v, want approved blank", got)
	}
}

// Case 6: a run starts as ajai run --unattended --origin web, and 409 while a run holds the lock.
func TestStartingARunLaunchesItAsTheWebFront(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("POST", "/api/runs", `{"market":"internacional","dry_run":true}`, nil)
	if code != http.StatusAccepted {
		t.Fatalf("start: %d %s", code, body)
	}
	want := "run --unattended --origin web --market internacional --dry-run"
	if len(f.launcher.calls) != 1 || strings.Join(f.launcher.calls[0], " ") != want {
		t.Errorf("launcher got %v, want %q", f.launcher.calls, want)
	}
	lock, err := run.AcquireLock(f.dataDir, run.Holder{Origin: run.OriginTUI, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	code, body = f.do("POST", "/api/runs", `{}`, nil)
	if code != http.StatusConflict || !strings.Contains(body, "the TUI") {
		t.Errorf("with the lock held: %d %s, want 409 naming the holder", code, body)
	}
	if len(f.launcher.calls) != 1 {
		t.Error("a refused start must not launch a process")
	}
}

// Case 7: a stop is requested only for the run the caller saw.
func TestStopOnlyTheRunTheCallerSaw(t *testing.T) {
	f := newFixture(t)
	started := time.Now().Truncate(time.Second)
	lock, err := run.AcquireLock(f.dataDir, run.Holder{Origin: run.OriginTUI, Started: started})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	wrong := `{"pid":1,"started":"` + started.Format(time.RFC3339Nano) + `"}`
	if code, body := f.do("POST", "/api/runs/stop", wrong, nil); code != http.StatusConflict {
		t.Errorf("another pid: %d %s, want 409", code, body)
	}
	if _, err := os.Stat(run.StopPath(f.dataDir)); err == nil {
		t.Fatal("a refused stop wrote a stop request")
	}
	right := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"started":"` + started.Format(time.RFC3339Nano) + `"}`
	if code, body := f.do("POST", "/api/runs/stop", right, nil); code != http.StatusOK {
		t.Fatalf("the run the caller saw: %d %s", code, body)
	}
	data, err := os.ReadFile(run.StopPath(f.dataDir))
	if err != nil || string(data) != "stopped from the web front" {
		t.Errorf("stop request = %q, %v", data, err)
	}
}

// Case 8: the event stream sends status events while the client stays connected.
func TestEventsStreamStatus(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if sc.Text() == "event: status" {
			break
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	sc.Scan() // data: {...}
	line := sc.Text()
	if !strings.HasPrefix(line, "data: {") || !strings.Contains(line, `"running"`) {
		t.Errorf("status event data = %q", line)
	}
}

// Case 9: unknown pages fall back to the front, unknown API paths are JSON 404s.
func TestUnknownPathsFallBackToTheFrontOrAreJSONNotFound(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("GET", "/some/spa/route", "", nil)
	if code != http.StatusOK || !strings.Contains(body, "<html") {
		t.Errorf("unknown page: %d %.80s, want index.html", code, body)
	}
	code, body = f.do("GET", "/api/unknown", "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown api: %d", code)
	}
	if m := decode(t, body); m["error"] == nil {
		t.Errorf("unknown api body = %s, want a JSON error", body)
	}
}

// Case 10: a client that connects during a run gets the day's lines already
// written (oldest first, each once), then the lines written after it connected.
func TestEventsSendTheBacklogThenFollow(t *testing.T) {
	f := newFixture(t)
	logDir := filepath.Join(f.dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := logging.FileName(logDir, time.Now())
	line := func(n int) string {
		return `{"time":"2026-10-21T10:00:00Z","level":"INFO","msg":"line ` + strconv.Itoa(n) + `"}` + "\n"
	}
	before := ""
	for i := 1; i <= 5; i++ {
		before += line(i)
	}
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timeout := time.AfterFunc(5*time.Second, cancel) // a missing line fails the test, not hangs it
	defer timeout.Stop()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	// wait for the first status event, so the backlog is past it
	for sc.Scan() {
		if sc.Text() == "event: status" {
			break
		}
	}
	// a line written after the client connected
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(line(6)); err != nil {
		t.Fatal(err)
	}
	_ = fh.Close()

	var got []string
	inLog := false
	for len(got) < 6 && sc.Scan() {
		text := sc.Text()
		switch {
		case text == "event: log":
			inLog = true
		case text == "event: status":
			inLog = false
		case inLog && strings.HasPrefix(text, "data: "):
			got = append(got, text)
			inLog = false
		}
	}
	for i, text := range got {
		want := "data: " + strings.TrimSuffix(line(i+1), "\n")
		if text != want {
			t.Errorf("log line %d = %q, want %q", i+1, text, want)
		}
	}
	if len(got) != 6 {
		t.Errorf("got %d log lines, want 6", len(got))
	}
}

// Case 11: a long day sends only its last 300 lines on connecting.
func TestEventsBacklogIsCappedAt300Lines(t *testing.T) {
	f := newFixture(t)
	logDir := filepath.Join(f.dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var day strings.Builder
	for i := 1; i <= 305; i++ {
		day.WriteString(`{"time":"2026-10-21T10:00:00Z","level":"INFO","msg":"line ` + strconv.Itoa(i) + `"}` + "\n")
	}
	if err := os.WriteFile(logging.FileName(logDir, time.Now()), []byte(day.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timeout := time.AfterFunc(5*time.Second, cancel)
	defer timeout.Stop()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	var got []string
	inLog := false
	for len(got) < 300 && sc.Scan() {
		switch text := sc.Text(); {
		case text == "event: log":
			inLog = true
		case inLog && strings.HasPrefix(text, "data: "):
			got = append(got, text)
			inLog = false
		}
	}
	if len(got) != 300 {
		t.Fatalf("got %d backlog lines, want 300", len(got))
	}
	if want := `data: {"time":"2026-10-21T10:00:00Z","level":"INFO","msg":"line 6"}`; got[0] != want {
		t.Errorf("first backlog line = %q, want %q", got[0], want)
	}
	if want := `data: {"time":"2026-10-21T10:00:00Z","level":"INFO","msg":"line 305"}`; got[299] != want {
		t.Errorf("last backlog line = %q, want %q", got[299], want)
	}
}

// Case 8: a backfill starts as ajai messages backfill --unattended --origin web, with --limit when given.
func TestBackfillLaunchesItAsTheWebFront(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("POST", "/api/messages/backfill", `{}`, nil)
	if code != http.StatusAccepted {
		t.Fatalf("start: %d %s", code, body)
	}
	code, body = f.do("POST", "/api/messages/backfill", `{"limit":5}`, nil)
	if code != http.StatusAccepted {
		t.Fatalf("start with a limit: %d %s", code, body)
	}
	want := []string{
		"messages backfill --unattended --origin web",
		"messages backfill --unattended --origin web --limit 5",
	}
	if len(f.launcher.calls) != 2 {
		t.Fatalf("launcher got %v, want 2 starts", f.launcher.calls)
	}
	for i, w := range want {
		if got := strings.Join(f.launcher.calls[i], " "); got != w {
			t.Errorf("start %d: launcher got %q, want %q", i, got, w)
		}
	}
}

// Case 9: a backfill is refused with 409 while a run holds the lock, and launches nothing.
func TestBackfillWaitsForTheRunLock(t *testing.T) {
	f := newFixture(t)
	lock, err := run.AcquireLock(f.dataDir, run.Holder{Origin: run.OriginTUI, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	code, body := f.do("POST", "/api/messages/backfill", `{"limit":3}`, nil)
	if code != http.StatusConflict || !strings.Contains(body, "is in progress") {
		t.Errorf("with the lock held: %d %s, want 409", code, body)
	}
	if len(f.launcher.calls) != 0 {
		t.Errorf("a refused backfill launched %v", f.launcher.calls)
	}
}

// Case 10: a negative limit is a 400 and launches nothing.
func TestBackfillRefusesANegativeLimit(t *testing.T) {
	f := newFixture(t)
	code, body := f.do("POST", "/api/messages/backfill", `{"limit":-1}`, nil)
	if code != http.StatusBadRequest {
		t.Errorf("negative limit: %d %s, want 400", code, body)
	}
	if len(f.launcher.calls) != 0 {
		t.Errorf("a refused backfill launched %v", f.launcher.calls)
	}
}

// Case 11: GET /api/messages counts the applied jobs whose recruiter was never reached.
func TestMessagesReportTheUnreachedApplied(t *testing.T) {
	f := newFixture(t)
	if err := f.st.UpsertJob(store.Job{JobID: "1", Outcome: store.Applied, Title: "Dev", Company: "Acme"}); err != nil {
		t.Fatal(err)
	}
	code, body := f.do("GET", "/api/messages", "", nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	want, err := f.st.CountUnreachedApplied()
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, body)["unreached"]
	if got != float64(want) || want != 1 {
		t.Errorf("unreached = %v, store counts %d, want 1", got, want)
	}
}
