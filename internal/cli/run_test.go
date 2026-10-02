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

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/run"
)

func TestRunRefusesBeforeAValidConfigAndWithoutDryRun(t *testing.T) {
	h := newHarness(t)
	code, _, errText := h.run("run", "--dry-run")
	if code == 0 || !strings.Contains(errText, "aja init") {
		t.Errorf("%d %s", code, errText)
	}
	h.initValid()
	code, _, errText = h.run("run", "--fast")
	if code == 0 || !strings.Contains(errText, "--dry-run") {
		t.Errorf("%d %s", code, errText)
	}
}

func TestRunReportsAProfileInUseInsteadOfHanging(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		sent = append(sent, string(data))
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	savedBase := notify.APIBase
	notify.APIBase = srv.URL
	defer func() { notify.APIBase = savedBase }()
	h.env["AJA_TELEGRAM_BOT_TOKEN"], h.env["AJA_TELEGRAM_CHAT_ID"] = "1:tok", "42"
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	OpenBrowser = func(ctx context.Context, o browser.Options) (browser.Browser, error) {
		return nil, browser.ErrProfileInUse
	}
	code, out, errText := h.run("run", "--dry-run")
	if code == 0 || !strings.Contains(errText, "profile is in use") {
		t.Errorf("%d %s %s", code, out, errText)
	}
	if !strings.Contains(out, "dry run, unattended") {
		t.Errorf("a run whose stdout is not a terminal is unattended:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(h.home, "run.pid")); err == nil {
		t.Error("no pid file should be left when the browser never opened")
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "could not start a run") || !strings.Contains(sent[0], "profile is in use") {
		t.Errorf("a run that cannot start says so on Telegram, once: %q", sent)
	}
	logs, _ := os.ReadFile(filepath.Join(h.home, "logs", mustOnlyFile(t, filepath.Join(h.home, "logs"))))
	if strings.Contains(string(logs), "1:tok") {
		t.Error("the token reached the log")
	}
}

func mustOnlyFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	return entries[0].Name()
}

func TestStatusAndLogsWithoutAnyRun(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	_, out, _ := h.run("status")
	if !strings.Contains(out, "No run in progress") || !strings.Contains(out, "No runs recorded") {
		t.Errorf("%s", out)
	}
	_, out, _ = h.run("logs", "-n", "1")
	if !strings.Contains(out, `"msg":"config created"`) {
		t.Errorf("logs should print the init line:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(h.home, "run.pid"), []byte(`{"pid": 999999999, "started": "2026-10-01T10:00:00Z", "dry_run": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, _ = h.run("status")
	if !strings.Contains(out, "stale") {
		t.Errorf("a dead pid is reported as stale:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(h.home, "run.pid"), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, out, _ = h.run("status"); !strings.Contains(out, "unreadable") {
		t.Errorf("a corrupt state file is reported:\n%s", out)
	}
}

func TestRunTelegramWarningsReachTheLogAndABrokenConfigStillReports(t *testing.T) {
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		sent = append(sent, string(data))
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"ok":false,"description":"Unauthorized"}`)
	}))
	defer srv.Close()
	savedBase := notify.APIBase
	notify.APIBase = srv.URL
	defer func() { notify.APIBase = savedBase }()

	h := newHarness(t)
	h.initValid()
	h.env["AJA_TELEGRAM_BOT_TOKEN"] = "1:tok" // half configured
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	OpenBrowser = func(ctx context.Context, o browser.Options) (browser.Browser, error) {
		return nil, browser.ErrProfileInUse
	}
	h.run("run", "--dry-run")
	logs, _ := os.ReadFile(filepath.Join(h.home, "logs", mustOnlyFile(t, filepath.Join(h.home, "logs"))))
	if !strings.Contains(string(logs), "BOTH") || len(sent) != 0 {
		t.Errorf("a half-configured Telegram warns in the log and sends nothing: %d sent\n%s", len(sent), logs)
	}

	h.env["AJA_TELEGRAM_CHAT_ID"] = "42"
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("search:\n  trems: [x]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errText := h.run("run", "--dry-run")
	if code == 0 || !strings.Contains(errText, "trems") {
		t.Fatalf("%d %s", code, errText)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "could not start a run") || !strings.Contains(sent[0], "trems") {
		t.Errorf("a broken config file is reported too: %q", sent)
	}
	logs, _ = os.ReadFile(filepath.Join(h.home, "logs", mustOnlyFile(t, filepath.Join(h.home, "logs"))))
	if !strings.Contains(string(logs), "401") || strings.Contains(string(logs), "1:tok") {
		t.Errorf("a 401 is a warning in the log, without the token:\n%s", logs)
	}
}

func TestRunFromTheTUIRecordsItsOriginAndLogsWhyItCouldNotStart(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	var holder run.Holder
	OpenBrowser = func(ctx context.Context, o browser.Options) (browser.Browser, error) {
		holder, _, _ = run.ReadLock(h.home) // the run holds the lock by now
		return nil, browser.ErrProfileInUse
	}
	if code, _, errText := h.run("run", "--dry-run", "--unattended", "--origin", "tui"); code == 0 || !strings.Contains(errText, "profile is in use") {
		t.Fatalf("%d %s", code, errText)
	}
	if holder.Origin != run.OriginTUI || holder.From() != "the TUI" {
		t.Errorf("holder %+v", holder)
	}
	logs, _ := os.ReadFile(filepath.Join(h.home, "logs", mustOnlyFile(t, filepath.Join(h.home, "logs"))))
	if !strings.Contains(string(logs), `"msg":"`+run.StartFailedMsg+`","error":"`) || !strings.Contains(string(logs), "profile is in use") {
		t.Errorf("why it could not start is in the log:\n%s", logs)
	}
	if code, _, errText := h.run("run", "--dry-run", "--origin", "telegram"); code == 0 || !strings.Contains(errText, `unknown --origin "telegram"`) {
		t.Errorf("only the TUI's origin: %d %s", code, errText)
	}
	if _, out, _ := h.run("run", "--help"); strings.Contains(out, "--origin") {
		t.Error("--origin is not for people")
	}
}
