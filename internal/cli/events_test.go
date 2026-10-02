package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/ui"
)

func TestJSONModePrintsOnlyJSONLinesForEventsAndSummaries(t *testing.T) {
	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out, eventsAsJSON: true}
	a.ui = ui.New(&out, &out, true)
	card := &linkedin.Card{ID: "1", Title: "Go Dev", Company: "ACME", Location: "Remote", Link: "https://www.linkedin.com/jobs/view/1"}
	a.report(run.Event{Kind: run.EventSkip, Text: "bad word", Card: card})
	a.report(run.Event{Kind: run.EventRunEnd, Text: "all search terms visited", Summary: &run.Summary{RunID: 3, Market: "brasil", Applied: 2, End: run.EndAllTerms, Reason: "all search terms visited"}})
	a.note(run.EventWarn, "resume", "resume.pdf not found")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("%q", out.String())
	}
	var skip, end, warn map[string]any
	for i, target := range []*map[string]any{&skip, &end, &warn} {
		if err := json.Unmarshal([]byte(lines[i]), target); err != nil {
			t.Fatalf("line %d is not JSON: %q", i, lines[i])
		}
	}
	if skip["event"] != "skip" || skip["job"].(map[string]any)["company"] != "ACME" || skip["text"] != "bad word" {
		t.Errorf("%v", skip)
	}
	sum := end["summary"].(map[string]any)
	if end["event"] != "run-end" || sum["applied"] != float64(2) || sum["end"] != "all-terms" || sum["market"] != "brasil" {
		t.Errorf("%v", end)
	}
	if warn["event"] != "warn" || warn["text"] != "resume.pdf not found" {
		t.Errorf("%v", warn)
	}
}

func TestRunWithJSONKeepsStdoutJSONAndImpliesUnattended(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	OpenBrowser = func(ctx context.Context, o browser.Options) (browser.Browser, error) {
		return nil, browser.ErrProfileInUse
	}
	code, out, errText := h.run("run", "--dry-run", "--json")
	if code == 0 || !strings.Contains(errText, "profile is in use") {
		t.Fatalf("%d %s", code, errText)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stdout has a non-JSON line: %q\nall:\n%s", line, out)
		}
	}
	if !strings.Contains(out, `"text":"dry run, unattended`) || strings.Contains(out, "aja test") {
		t.Errorf("json implies unattended and prints no banner:\n%s", out)
	}
}

func TestTheModeLineNamesNonStopAndTheFilterPause(t *testing.T) {
	if got := runMode(run.Options{DryRun: true}, true, true); got != "dry run, non-stop, pause after filters" {
		t.Errorf("%q", got)
	}
}

func TestThePauseAfterFiltersWaitsForEnterAndStopsOnOffEndOfInputOrSilence(t *testing.T) {
	cases := []struct {
		name  string
		stdin io.Reader
		keep  bool
	}{
		{"enter continues", delayed("\n"), true},
		{"off stops pausing", delayed("off\n"), false},
		{"OFF too", delayed("  OFF \n"), false},
		{"end of input", strings.NewReader(""), false},
		{"nobody answers", blockingReader{}, false},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		a := &App{Stdout: &out, Stderr: &out, Stdin: tc.stdin}
		a.ui = ui.New(&out, &out, true)
		if got := a.filterPause(200*time.Millisecond)(context.Background(), "Go Developer"); got != tc.keep {
			t.Errorf("%s: keep pausing = %v", tc.name, got)
		}
	}
}

// delayed types text a moment after the pause starts (an earlier line would be drained).
func delayed(text string) io.Reader {
	r, w := io.Pipe()
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, text)
		_ = w.Close()
	}()
	return r
}

func TestARetiredKeyIsAWarningInValidateAndDoctor(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	path := filepath.Join(h.home, "config.yaml")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "settings:\n", "settings:\n  click_gap: 1\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := h.run("config", "validate")
	if code != 0 || !strings.Contains(out, "settings.click_gap is no longer used") || !strings.Contains(out, "is valid") {
		t.Errorf("%d\n%s", code, out)
	}
	_, out, _ = h.run("doctor")
	if !strings.Contains(out, "warn  config") || !strings.Contains(out, "click_gap") {
		t.Errorf("%s", out)
	}
	h.run("config", "set", "search.switch_number", "5")
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "click_gap") {
		t.Error("config set must rewrite the file without the retired key")
	}
}
