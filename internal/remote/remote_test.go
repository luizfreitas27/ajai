package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/schedule"
	"github.com/luizfreitas27/aja/internal/store"
)

const (
	token = "123:SECRETTOKEN"
	chat  = 42
)

// fakeTelegram is the Bot API: getUpdates serves queued updates the way
// Telegram does (an update stays until a later call's offset passes it) and
// sendMessage records the text.
type fakeTelegram struct {
	mu      sync.Mutex
	queue   []notify.Update
	sent    []string
	fail    int // the next getUpdates calls that fail with a dropped connection
	nextID  int64
	srv     *httptest.Server
	arrived chan struct{}
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{nextID: 100, arrived: make(chan struct{}, 100)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			http.Error(w, `{"ok":false,"description":"Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch strings.TrimPrefix(r.URL.Path, "/bot"+token+"/") {
		case "getUpdates":
			f.mu.Lock()
			if f.fail > 0 {
				f.fail--
				f.mu.Unlock()
				hj, _ := w.(http.Hijacker)
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			offset := int64(body["offset"].(float64))
			var out []notify.Update
			kept := f.queue[:0]
			for _, u := range f.queue {
				if u.UpdateID >= offset {
					kept = append(kept, u)
					out = append(out, u)
				}
			}
			f.queue = kept
			f.mu.Unlock()
			if len(out) == 0 {
				select { // a short long poll
				case <-f.arrived:
				case <-time.After(20 * time.Millisecond):
				case <-r.Context().Done():
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": out})
		case "sendMessage":
			f.mu.Lock()
			f.sent = append(f.sent, body["text"].(string))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// say queues a message from chat c sent at when.
func (f *fakeTelegram) say(c int64, text string, when time.Time) {
	f.mu.Lock()
	f.nextID++
	f.queue = append(f.queue, notify.Update{UpdateID: f.nextID, Message: &notify.Message{MessageID: f.nextID, Date: when.Unix(), Chat: notify.Chat{ID: c}, Text: text}})
	f.mu.Unlock()
	f.arrived <- struct{}{}
}

func (f *fakeTelegram) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// waitFor waits until the bot has sent n messages, or fails.
func (f *fakeTelegram) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := f.messages(); len(m) >= n {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d messages, got %q", n, f.messages())
	return nil
}

// fakeRuns is the supervisor: it records what it is asked and answers as told.
type fakeRuns struct {
	mu       sync.Mutex
	starts   []daemon.Request
	stops    []string
	startErr error
	stopErr  error
	holder   run.Holder
}

func (r *fakeRuns) Start(_ context.Context, req daemon.Request) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, req)
	return r.startErr
}

// seen returns copies of what the supervisor was asked.
func (r *fakeRuns) seen() ([]daemon.Request, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]daemon.Request(nil), r.starts...), append([]string(nil), r.stops...)
}

func (r *fakeRuns) Stop(reason string) (run.Holder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops = append(r.stops, reason)
	return r.holder, r.stopErr
}

type world struct {
	t       *testing.T
	tg      *fakeTelegram
	runs    *fakeRuns
	st      *store.Store
	dir     string
	logs    *bytes.Buffer
	logMu   sync.Mutex
	now     time.Time
	cancel  context.CancelFunc
	done    chan struct{}
	backoff []time.Duration
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "aja.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &world{t: t, tg: newFakeTelegram(t), runs: &fakeRuns{}, st: st, dir: dir, logs: &bytes.Buffer{},
		now: time.Date(2026, 10, 5, 9, 30, 0, 0, time.Local)}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// listen starts a listener (a daemon starting) and returns once it polls.
func (w *world) listen() {
	w.t.Helper()
	logger := slog.New(slog.NewTextHandler(lockedWriter{&w.logMu, w.logs}, nil))
	bot := notify.New(token, "42", logger).WithAPIBase(w.tg.srv.URL)
	l := New(bot, w.runs, w.st, w.dir, logger, func() time.Time { return w.now })
	l.Poll, l.StopConfirm = 50*time.Millisecond, 2*time.Second
	l.Sleep = func(ctx context.Context, d time.Duration) error {
		w.logMu.Lock()
		w.backoff = append(w.backoff, d)
		w.logMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.done = cancel, make(chan struct{})
	go func() { l.Loop(ctx); close(w.done) }()
	w.t.Cleanup(w.stop)
}

func (w *world) stop() {
	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
	}
}

func (w *world) log() string { w.logMu.Lock(); defer w.logMu.Unlock(); return w.logs.String() }

func TestRunStartsThroughTheSupervisorAndSaysWhyNot(t *testing.T) {
	w := newWorld(t)
	w.listen()
	w.tg.say(chat, "/run brasil dry", w.now)
	m := w.tg.waitFor(t, 1)
	if !strings.Contains(m[0], "Started a run (market brasil, dry run)") {
		t.Errorf("%q", m)
	}
	if starts, _ := w.runs.seen(); len(starts) != 1 || starts[0] != (daemon.Request{Origin: run.OriginTelegram, Market: "brasil", DryRun: true}) {
		t.Errorf("%+v", starts)
	}

	w.runs.mu.Lock()
	w.runs.startErr = &run.HeldError{Holder: run.Holder{PID: 7, Origin: run.OriginSchedule, Window: 3, Started: w.now}}
	w.runs.mu.Unlock()
	w.tg.say(chat, "/run", w.now)
	if m := w.tg.waitFor(t, 2); !strings.Contains(m[1], "Not started: a run started from schedule window 3") || !strings.Contains(m[1], "/stop") {
		t.Errorf("%q", m[1])
	}
	w.runs.mu.Lock()
	w.runs.startErr = errors.New("fix the configuration first (`aja config validate`): search.terms is empty")
	w.runs.mu.Unlock()
	w.tg.say(chat, "/run@aja_bot internacional", w.now)
	if m := w.tg.waitFor(t, 3); !strings.Contains(m[2], "Not started: fix the configuration first") {
		t.Errorf("%q", m[2])
	}
	w.tg.say(chat, "/run usa", w.now)
	if m := w.tg.waitFor(t, 4); !strings.Contains(m[3], `I do not know "usa"`) || !strings.Contains(m[3], "/run [brasil|internacional] [dry]") {
		t.Errorf("%q", m[3])
	}
	if starts, _ := w.runs.seen(); len(starts) != 3 {
		t.Errorf("an unknown market starts nothing: %d starts", len(starts))
	}
}

func TestStopAsksTheRunAndConfirmsWhenItEnded(t *testing.T) {
	w := newWorld(t)
	terminal, err := run.AcquireLock(w.dir, run.Holder{Origin: run.OriginTerminal, Started: w.now})
	if err != nil {
		t.Fatal(err)
	}
	w.runs.holder = run.Holder{PID: 9, Origin: run.OriginTerminal, Started: w.now}
	w.listen()
	w.tg.say(chat, "/stop", w.now)
	if m := w.tg.waitFor(t, 1); !strings.Contains(m[0], "Stopping a run started from the terminal") {
		t.Errorf("%q", m)
	}
	if _, stops := w.runs.seen(); len(stops) != 1 || stops[0] != ReasonStopped {
		t.Errorf("%q", stops)
	}
	terminal.Release() // the run ends
	if m := w.tg.waitFor(t, 2); !strings.Contains(m[1], "Stopped.") {
		t.Errorf("%q", m)
	}
	w.runs.mu.Lock()
	w.runs.stopErr = run.ErrNoRun
	w.runs.mu.Unlock()
	w.tg.say(chat, "/stop", w.now)
	if m := w.tg.waitFor(t, 3); m[2] != "No run in progress." {
		t.Errorf("%q", m[2])
	}
}

func TestStatusHelpAndUnknownCommands(t *testing.T) {
	w := newWorld(t)
	win := schedule.Window{Days: []time.Weekday{time.Monday}, Start: 9 * 60, End: 11 * 60, Enabled: true}
	if _, err := w.st.AddWindow(win); err != nil {
		t.Fatal(err)
	}
	id, _ := w.st.StartRun(context.Background(), "brasil", false)
	_ = w.st.FinishRun(context.Background(), store.Run{ID: id, Applied: 4, Failed: 1, Reason: "schedule window closed"})
	_ = w.st.UpsertAnswer(store.Answer{ID: store.NewAnswerID(), Label: "Q", Normalised: "q", Kind: "text", Source: "ai", State: "pending"})
	w.listen()
	w.tg.say(chat, "/status", w.now)
	m := w.tg.waitFor(t, 1)
	for _, want := range []string{"No run in progress.", "applied 4, external 0, failed 1", "schedule window closed", "Pending answers: 1",
		"Window open now: 1 until 11:00", "Next window: Mon 12 Oct 09:00"} {
		if !strings.Contains(m[0], want) {
			t.Errorf("status lacks %q:\n%s", want, m[0])
		}
	}

	// A run in progress, as its lock and live state show it.
	lock, _ := run.AcquireLock(w.dir, run.Holder{Origin: run.OriginSchedule, Window: 1, Started: w.now})
	defer lock.Release()
	_ = run.WriteState(run.StatePath(w.dir), run.State{Market: "brasil", DryRun: true, Term: "Golang", Applied: 2, Skipped: 5})
	w.tg.say(chat, "/status", w.now)
	m = w.tg.waitFor(t, 2)
	for _, want := range []string{"Running: a run started from schedule window 1", "Market brasil, dry run.", "Searching: Golang", "Applied 2, external 0, failed 0, skipped 5"} {
		if !strings.Contains(m[1], want) {
			t.Errorf("status lacks %q:\n%s", want, m[1])
		}
	}
	w.tg.say(chat, "/help", w.now)
	w.tg.say(chat, "/whatever", w.now)
	w.tg.say(chat, "just chatting", w.now)
	m = w.tg.waitFor(t, 4)
	if !strings.Contains(m[2], "/run [brasil|internacional] [dry]") || !strings.Contains(m[3], "Unknown command /whatever") {
		t.Errorf("%q", m[2:])
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(w.tg.messages()); n != 4 {
		t.Errorf("plain text gets no answer: %q", w.tg.messages())
	}
}

func TestOnlyTheConfiguredChatIsHeardAndLoggedOnce(t *testing.T) {
	w := newWorld(t)
	w.listen()
	w.tg.say(999, "/run", w.now)
	w.tg.say(999, "/stop", w.now)
	w.tg.say(chat, "/help", w.now) // proves the two before were read
	w.tg.waitFor(t, 1)
	if starts, stops := w.runs.seen(); len(starts)+len(stops) != 0 || len(w.tg.messages()) != 1 {
		t.Errorf("a stranger's commands: %+v %q %q", starts, stops, w.tg.messages())
	}
	if n := strings.Count(w.log(), "ignoring Telegram messages"); n != 1 {
		t.Errorf("logged %d times:\n%s", n, w.log())
	}
}

func TestARestartNeverRepeatsACommandAndOldCommandsAreNotActedOn(t *testing.T) {
	w := newWorld(t)
	w.listen()
	w.tg.say(chat, "/run", w.now)
	w.tg.waitFor(t, 1)
	w.stop() // the daemon stops before Telegram learnt the update was read
	w.listen()
	w.tg.say(chat, "/help", w.now)
	w.tg.waitFor(t, 2)
	if starts, _ := w.runs.seen(); len(starts) != 1 {
		t.Errorf("the /run was acted on %d times", len(starts))
	}
	w.tg.say(chat, "/run", w.now.Add(-time.Hour)) // sent while the daemon was down
	m := w.tg.waitFor(t, 3)
	if starts, _ := w.runs.seen(); !strings.Contains(m[2], "Not acting on /run sent at") || len(starts) != 1 {
		t.Errorf("%q %d", m[2], len(starts))
	}
}

func TestTelegramFailingIsOneWarningWithGrowingPausesAndTheTokenIsNeverLogged(t *testing.T) {
	w := newWorld(t)
	w.tg.fail = 4
	w.listen()
	w.tg.say(chat, "/help", w.now)
	w.tg.waitFor(t, 1)
	logs := w.log()
	if n := strings.Count(logs, "cannot reach Telegram"); n != 1 || !strings.Contains(logs, "reachable again") {
		t.Errorf("one warning and one recovery line:\n%s", logs)
	}
	if strings.Contains(logs, "SECRETTOKEN") {
		t.Errorf("the token reached the log:\n%s", logs)
	}
	w.logMu.Lock()
	b := append([]time.Duration(nil), w.backoff...)
	w.logMu.Unlock()
	if len(b) < 4 || b[0] != 5*time.Second || b[1] != 10*time.Second || b[3] != 40*time.Second {
		t.Errorf("growing pauses: %v", b)
	}
}
