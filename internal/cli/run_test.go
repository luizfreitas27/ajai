package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/browser"
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
