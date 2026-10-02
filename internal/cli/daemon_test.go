package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/schedule"
	"github.com/luizfreitas27/aja/internal/store"
)

func TestTheDaemonRunsUntilCtrlCAndRecordsAWindowItCouldNotStart(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.now = friday // 12:00
	if code, _, errText := h.run("schedule", "add", "--days", "fri", "--start", "11:00", "--end", "13:00"); code != 0 {
		t.Fatal(errText)
	}
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	OpenBrowser = func(context.Context, browser.Options) (browser.Browser, error) { return nil, browser.ErrProfileInUse }

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		code      int
		out, errs string
	}
	done := make(chan result)
	go func() {
		code, out, errs := h.runCtx(ctx, "daemon")
		done <- result{code, out, errs}
	}()
	// The first tick is immediate: the open window's run is tried and fails (Chrome busy).
	db := filepath.Join(h.home, "aja.db")
	deadline := time.Now().Add(10 * time.Second)
	var rec *store.OpeningRecord
	for rec == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if st, err := store.Open(db); err == nil {
			rec, _ = st.GetOpening(schedule.Key{WindowID: 1, Date: "2026-10-02"})
			_ = st.Close()
		}
	}
	if rec == nil || rec.Outcome != schedule.OutcomeFailed || !strings.Contains(rec.Detail, "profile is in use") {
		t.Fatalf("%+v", rec)
	}
	if _, out, _ := h.run("status"); !strings.Contains(out, "daemon") || !strings.Contains(out, "running (pid") {
		t.Errorf("status shows the daemon:\n%s", out)
	}
	if code, _, errText := h.run("daemon"); code == 0 || !strings.Contains(errText, "aja daemon is already running") {
		t.Errorf("a second daemon must refuse: %d %s", code, errText)
	}
	cancel()
	var r result
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not stop on Ctrl-C")
	}
	if r.code != 0 || !strings.Contains(r.out, "running (pid") || !strings.Contains(r.out, "stopped") {
		t.Errorf("%d %s %s", r.code, r.out, r.errs)
	}
	logs, _ := os.ReadFile(logging.DaemonFileName(filepath.Join(h.home, "logs"), time.Now()))
	for _, want := range []string{`"msg":"daemon started"`, `"msg":"schedule window could not start"`, `"msg":"daemon stopped"`} {
		if !strings.Contains(string(logs), want) {
			t.Errorf("the daemon log lacks %s:\n%s", want, logs)
		}
	}
	if _, out, _ := h.run("logs", "--daemon", "-n", "0"); !strings.Contains(out, "daemon stopped") {
		t.Errorf("aja logs --daemon shows it:\n%s", out)
	}
	if _, out, _ := h.run("status"); !strings.Contains(out, "not running: schedule windows start runs only while aja daemon runs") {
		t.Errorf("%s", out)
	}
	if _, running, _ := daemon.ReadLock(h.home); running {
		t.Error("the daemon lock must be released")
	}
}

func TestTheDaemonListensForTelegramCommandsWhenTheBotIsSet(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	}))
	defer srv.Close()
	saved := notify.APIBase
	notify.APIBase = srv.URL
	defer func() { notify.APIBase = saved }()
	h.env["AJA_TELEGRAM_BOT_TOKEN"], h.env["AJA_TELEGRAM_CHAT_ID"] = "1:tok", "42"
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	code, out, errText := h.runCtx(ctx, "daemon")
	if code != 0 || !strings.Contains(out, "listening for commands: /status, /stop, /run, /help") {
		t.Errorf("%d %s %s", code, out, errText)
	}
	logs, _ := os.ReadFile(logging.DaemonFileName(filepath.Join(h.home, "logs"), time.Now()))
	if !strings.Contains(string(logs), "listening for Telegram commands") || strings.Contains(string(logs), "1:tok") {
		t.Errorf("%s", logs)
	}
}
