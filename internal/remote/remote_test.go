package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/daemon"
	"github.com/luizfreitas27/ajai/internal/notify"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

const (
	token = "123:SECRETTOKEN"
	chat  = 42
)

// fakeTelegram is the Bot API: getUpdates serves queued updates the way
// Telegram does (an update stays until a later call's offset passes it),
// sendMessage records the text and buttons, editMessageText and
// answerCallbackQuery are recorded too.
type fakeTelegram struct {
	mu       sync.Mutex
	queue    []notify.Update
	sent     []outgoing
	edits    []outgoing // ID is the edited message
	acks     []string   // answerCallbackQuery texts
	lastMsg  int64      // the last message id sendMessage gave
	tooMany  int        // the next sendMessage calls answered 429
	fail     int        // the next getUpdates calls that fail with a dropped connection
	conflict int        // the next getUpdates calls answered 409 (a webhook is set)
	nextID   int64
	srv      *httptest.Server
	arrived  chan struct{}
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
			if f.conflict > 0 {
				f.conflict--
				f.mu.Unlock()
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active"}`))
				return
			}
			if f.fail > 0 {
				f.fail--
				f.mu.Unlock()
				hj, _ := w.(http.Hijacker)
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			offset := int64(body["offset"].(float64))
			allowed := fmt.Sprint(body["allowed_updates"])
			var out []notify.Update
			kept := f.queue[:0]
			for _, u := range f.queue {
				if u.UpdateID < offset {
					continue
				}
				kept = append(kept, u)
				// Telegram leaves out the kinds of update the bot did not ask for.
				if (u.Message != nil && strings.Contains(allowed, "message")) || (u.CallbackQuery != nil && strings.Contains(allowed, "callback_query")) {
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
			if f.tooMany > 0 {
				f.tooMany--
				f.mu.Unlock()
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`))
				return
			}
			f.lastMsg++
			f.sent = append(f.sent, outgoing{ID: f.lastMsg, Text: body["text"].(string), Buttons: keyboard(body)})
			id := f.lastMsg
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": id, "chat": map[string]any{"id": chat}}})
		case "editMessageText":
			f.mu.Lock()
			f.edits = append(f.edits, outgoing{ID: int64(body["message_id"].(float64)), Text: body["text"].(string), Buttons: keyboard(body)})
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "answerCallbackQuery":
			f.mu.Lock()
			f.acks = append(f.acks, body["text"].(string))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// add queues the update fill makes from the next update id.
func (f *fakeTelegram) add(fill func(id int64) notify.Update) {
	f.mu.Lock()
	f.nextID++
	f.queue = append(f.queue, fill(f.nextID))
	f.mu.Unlock()
	f.arrived <- struct{}{}
}

// say queues a message from chat c sent at when.
func (f *fakeTelegram) say(c int64, text string, when time.Time) {
	f.add(func(id int64) notify.Update {
		return notify.Update{UpdateID: id, Message: &notify.Message{MessageID: id, Date: when.Unix(), Chat: notify.Chat{ID: c}, Text: text}}
	})
}

// outgoing is a message the bot sent or edited.
type outgoing struct {
	ID      int64
	Text    string
	Buttons []notify.Button
}

// keyboard reads the inline buttons of a sendMessage or editMessageText body:
// nil without reply_markup, empty when the buttons were removed.
func keyboard(body map[string]any) []notify.Button {
	markup, ok := body["reply_markup"]
	if !ok {
		return nil
	}
	data, _ := json.Marshal(markup)
	var m struct {
		Rows [][]notify.Button `json:"inline_keyboard"`
	}
	_ = json.Unmarshal(data, &m)
	out := []notify.Button{}
	for _, row := range m.Rows {
		out = append(out, row...)
	}
	return out
}

func (f *fakeTelegram) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var texts []string
	for _, o := range f.sent {
		texts = append(texts, o.Text)
	}
	return texts
}

// did is what the bot did: messages sent and edited, buttons acknowledged.
type did struct {
	sent, edits []outgoing
	acks        []string
}

// until waits until cond holds for what the bot did, or fails.
func (f *fakeTelegram) until(t *testing.T, what string, cond func(d did) bool) did {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		d := did{append([]outgoing(nil), f.sent...), append([]outgoing(nil), f.edits...), append([]string(nil), f.acks...)}
		f.mu.Unlock()
		if cond(d) {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; sent %+v, edits %+v, acks %q", what, d.sent, d.edits, d.acks)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitFor waits until the bot has sent n messages, or fails.
func (f *fakeTelegram) waitFor(t *testing.T, n int) []string {
	t.Helper()
	f.until(t, fmt.Sprint(n, " messages"), func(d did) bool { return len(d.sent) >= n })
	return f.messages()
}

// fakeRuns is the supervisor: it records what it is asked and answers as told.
type fakeRuns struct {
	panics   bool
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
	if r.panics {
		panic("a nil request")
	}
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
	st, err := store.Open(filepath.Join(dir, "ajai.db"))
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
	l.poll, l.stopConfirm = 50*time.Millisecond, 2*time.Second
	l.sleep = func(ctx context.Context, d time.Duration) error {
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
	w.runs.startErr = errors.New("fix the configuration first (`ajai config validate`): search.terms is empty")
	w.runs.mu.Unlock()
	w.tg.say(chat, "/run@ajai_bot internacional", w.now)
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
	if m := w.tg.waitFor(t, 2); m[1] != "Stopped." {
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

func TestStatusEndsTheRunADeadProcessLeft(t *testing.T) {
	w := newWorld(t)
	killed, _ := w.st.StartRun(context.Background(), "brasil", true)
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Skip("no true(1):", err)
	}
	_ = run.WriteState(run.StatePath(w.dir), run.State{PID: gone.Process.Pid, RunID: killed, Skipped: 7})
	w.listen()
	w.tg.say(chat, "/status", w.now)
	m := w.tg.waitFor(t, 1)
	if !strings.Contains(m[0], run.StaleReason) || !strings.Contains(m[0], "skipped 7") || strings.Contains(m[0], "never recorded an end") {
		t.Errorf("%s", m[0])
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
	_ = run.WriteState(run.StatePath(w.dir), run.State{PID: os.Getpid(), Market: "brasil", DryRun: true, Term: "Golang", Applied: 2, Skipped: 5})
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

func TestARestartNeverRepeatsACommand(t *testing.T) {
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
}

func TestCommandsThatWaitedWhileNobodyListenedAreCheckedLiveOnesAreNot(t *testing.T) {
	w := newWorld(t)
	hourAgo := w.now.Add(-time.Hour)
	// Sent while the daemon was down: the backlog of the first poll.
	w.tg.say(chat, "/run", hourAgo)
	w.tg.say(chat, "/status", hourAgo)
	w.tg.say(chat, "/start", hourAgo) // tapping Start while setting the bot up
	w.listen()
	m := w.tg.waitFor(t, 3)
	if !strings.Contains(m[0], "Not acting on /run sent at") || !strings.Contains(m[1], "No run in progress") || !strings.Contains(m[2], "ajai listens to") {
		t.Errorf("%q", m)
	}
	if starts, _ := w.runs.seen(); len(starts) != 0 {
		t.Errorf("a stale /run started a run")
	}
	// Live, a message's date is not trusted against this machine's clock.
	w.tg.say(chat, "/run", hourAgo)
	w.tg.waitFor(t, 4)
	if starts, _ := w.runs.seen(); len(starts) != 1 {
		t.Errorf("a live /run must be acted on whatever the clocks say")
	}
}

func TestABacklogStopDoesNotStopARunThatStartedAfterIt(t *testing.T) {
	w := newWorld(t)
	lock, _ := run.AcquireLock(w.dir, run.Holder{Origin: run.OriginSchedule, Window: 2, Started: w.now})
	defer lock.Release()
	w.tg.say(chat, "/stop", w.now.Add(-5*time.Minute)) // meant for an earlier run
	w.listen()
	if m := w.tg.waitFor(t, 1); !strings.Contains(m[0], "it started after your /stop") {
		t.Errorf("%q", m)
	}
	if _, stops := w.runs.seen(); len(stops) != 0 {
		t.Error("nothing stopped")
	}
}

func TestTwoStopsGiveOneStoppedAndAPanickingCommandIsReported(t *testing.T) {
	w := newWorld(t)
	lock, _ := run.AcquireLock(w.dir, run.Holder{Origin: run.OriginTerminal, Started: w.now.Add(-time.Hour)})
	w.runs.holder = run.Holder{PID: 9, Origin: run.OriginTerminal, Started: w.now}
	w.listen()
	w.tg.say(chat, "/stop", w.now)
	w.tg.say(chat, "/stop", w.now)
	w.tg.waitFor(t, 2)
	lock.Release()
	w.tg.waitFor(t, 3)
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(strings.Join(w.tg.messages(), "\n"), "Stopped."); n != 1 {
		t.Errorf("%d confirmations: %q", n, w.tg.messages())
	}

	w.runs.mu.Lock()
	w.runs.panics = true
	w.runs.mu.Unlock()
	w.tg.say(chat, "/run", w.now)
	w.tg.say(chat, "/help", w.now) // still listening afterwards
	m := w.tg.waitFor(t, 5)
	if !strings.Contains(m[3], "ajai hit a bug handling /run") || !strings.Contains(m[4], "ajai listens to") {
		t.Errorf("%q", m[3:])
	}
}

func TestARefusedTokenStopsListeningAndAConflictIsSaidOnce(t *testing.T) {
	w := newWorld(t)
	w.tg.conflict = 3
	w.listen()
	w.tg.say(chat, "/help", w.now)
	m := w.tg.waitFor(t, 2)
	if !strings.Contains(m[0], "ajai cannot read your commands: Conflict: can't use getUpdates method while webhook is active") ||
		!strings.Contains(m[1], "ajai listens to") {
		t.Errorf("%q", m)
	}
	if n := strings.Count(w.log(), "hands the bot's messages to someone else"); n != 1 {
		t.Errorf("said %d times", n)
	}

	logger := slog.New(slog.NewTextHandler(lockedWriter{&w.logMu, w.logs}, nil))
	l := New(notify.New("999:WRONG", "42", logger).WithAPIBase(w.tg.srv.URL), w.runs, w.st, w.dir, logger, time.Now)
	done := make(chan struct{})
	go func() { l.Loop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a refused token must end the loop")
	}
	if !strings.Contains(w.log(), "Telegram refused the bot token") || strings.Contains(w.log(), "WRONG") {
		t.Errorf("%s", w.log())
	}
}

func TestTheOffsetIsKeptPerBotAndForgottenAfterAQuietWeek(t *testing.T) {
	w := newWorld(t)
	bot := notify.New(token, "42", nil)
	l := New(bot, w.runs, w.st, w.dir, nil, time.Now)
	if err := l.saveOffset(500); err != nil {
		t.Fatal(err)
	}
	if got := l.loadOffset(); got != 500 {
		t.Errorf("%d", got)
	}
	other := New(notify.New("777:OTHER", "42", nil), w.runs, w.st, w.dir, nil, time.Now)
	if got := other.loadOffset(); got != 0 {
		t.Errorf("another bot starts from its own offset: %d", got)
	}
	data, _ := json.Marshal(savedOffset{Offset: 500, Saved: time.Now().Add(-8 * 24 * time.Hour)})
	_ = w.st.SetState(l.offsetKey(), string(data))
	if got := l.loadOffset(); got != 0 {
		t.Errorf("after a quiet week Telegram numbers updates afresh: %d", got)
	}
}

func TestParseAndRunArguments(t *testing.T) {
	cases := []struct {
		text string
		name string
		args []string
	}{
		{"/status", "status", nil},
		{"  /Run@ajai_bot  brasil DRY ", "run", []string{"brasil", "DRY"}},
		{"/start", "start", nil},
		{"hello /status", "", nil},
		{"", "", nil},
	}
	for _, tc := range cases {
		name, args := parse(tc.text)
		if name != tc.name || strings.Join(args, ",") != strings.Join(tc.args, ",") {
			t.Errorf("%q: %q %q", tc.text, name, args)
		}
	}
	w := newWorld(t)
	w.listen()
	runs := []struct {
		text string
		want daemon.Request
	}{
		{"/run", daemon.Request{Origin: run.OriginTelegram}},
		{"/run dry-run", daemon.Request{Origin: run.OriginTelegram, DryRun: true}},
		{"/run dryrun INTERNACIONAL", daemon.Request{Origin: run.OriginTelegram, Market: "internacional", DryRun: true}},
	}
	for i, tc := range runs {
		w.tg.say(chat, tc.text, w.now)
		w.tg.waitFor(t, i+1)
		if starts, _ := w.runs.seen(); starts[i] != tc.want {
			t.Errorf("%q: %+v", tc.text, starts[i])
		}
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
