package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "aja.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesTheSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aja.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetState("k", "v"); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
	s, _ := Open(path)
	defer func() { _ = s.Close() }()
	if v, _ := s.GetState("k"); v != "v" {
		t.Errorf("state lost across opens: %q", v)
	}
	if v, err := s.GetState("missing"); v != "" || err != nil {
		t.Errorf("missing key: %q %v", v, err)
	}
}

func TestJobsUpsertByIdFilterAndCount(t *testing.T) {
	s := open(t)
	s.now = func() time.Time { return time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC) }
	must(t, s.UpsertJob(Job{JobID: "1", Outcome: Applied, Title: "Dev", DateApplied: "2026-10-01"}))
	must(t, s.UpsertJob(Job{JobID: "2", Outcome: Failed, Reason: "timeout"}))
	s.now = func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) }
	must(t, s.UpsertJob(Job{JobID: "3", Outcome: External, ExternalLink: "https://x"}))
	s.now = func() time.Time { return time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC) }
	must(t, s.UpsertJob(Job{JobID: "1", Outcome: Applied, Title: "Dev Sênior"})) // same id: replaced, not duplicated

	all, _ := s.ListJobs(JobFilter{})
	if len(all) != 3 || all[0].JobID != "1" || all[0].Title != "Dev Sênior" {
		t.Errorf("all: %+v", all)
	}
	failed, _ := s.ListJobs(JobFilter{Outcomes: []Outcome{Failed}})
	if len(failed) != 1 || failed[0].Reason != "timeout" {
		t.Errorf("failed: %+v", failed)
	}
	since, _ := s.ListJobs(JobFilter{Since: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	if len(since) != 2 {
		t.Errorf("since: %+v", since)
	}
	counts, _ := s.CountJobs()
	if counts[Applied] != 1 || counts[Failed] != 1 || counts[External] != 1 {
		t.Errorf("counts %v", counts)
	}
	if err := s.UpsertJob(Job{JobID: "9", Outcome: "lost"}); err == nil {
		t.Error("an unknown outcome should be rejected by the schema")
	}
}

func TestAnswersAreUniquePerQuestionAndListedByState(t *testing.T) {
	s := open(t)
	must(t, s.UpsertAnswer(Answer{ID: "a1", Label: "Consent?", Normalised: "consent", Kind: "checkbox", Answer: "checked", Source: "form", State: "pending"}))
	must(t, s.UpsertAnswer(Answer{ID: "a2", Label: "Years with Go", Normalised: "years with go", Kind: "text", Answer: "3", Source: "ai", State: "pending", UpdatedAt: "2026-10-02T00:00:00"}))
	must(t, s.UpsertAnswer(Answer{ID: "a3", Label: "Consent?", Normalised: "consent", Kind: "checkbox", Answer: "checked", Source: "user", State: "approved"}))
	pending, _ := s.ListAnswers("pending")
	approved, _ := s.ListAnswers("approved")
	all, _ := s.ListAnswers("")
	if len(pending) != 1 || pending[0].ID != "a2" || len(approved) != 1 || approved[0].ID != "a3" || len(all) != 2 {
		t.Errorf("pending %+v approved %+v all %d", pending, approved, len(all))
	}
	if err := s.UpsertAnswer(Answer{ID: "x", Kind: "button", Source: "ai", State: "pending"}); err == nil {
		t.Error("an unknown kind should be rejected by the schema")
	}
}

func TestRunsRecordStartAndFinish(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, err := s.StartRun(ctx, "brasil", true)
	must(t, err)
	must(t, s.FinishRun(ctx, Run{ID: id, Applied: 2, Skipped: 5, PendingAnswers: 1, Reason: "pacing limit"}))
	runs, _ := s.ListRuns(10)
	if len(runs) != 1 || !runs[0].DryRun || runs[0].Applied != 2 || runs[0].Reason != "pacing limit" || runs[0].EndedAt == "" {
		t.Errorf("%+v", runs)
	}
}

func TestExportWritesThePythonSpreadsheetsOldestFirst(t *testing.T) {
	s := open(t)
	s.now = func() time.Time { return time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC) }
	must(t, s.UpsertJob(Job{JobID: "1", Outcome: Applied, Title: "Dev, Sênior", Company: "ACME", DateApplied: "2026-10-01 10:00"}))
	s.now = func() time.Time { return time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC) }
	must(t, s.UpsertJob(Job{JobID: "2", Outcome: External, ExternalLink: "https://acme/jobs", DateApplied: "Pending"}))
	must(t, s.UpsertJob(Job{JobID: "3", Outcome: Failed, Reason: "modal closed", Screenshot: "3.png"}))
	must(t, s.UpsertJob(Job{JobID: "4", Outcome: Skipped, Reason: "blacklist"}))

	dir := t.TempDir()
	applied, failed, err := s.ExportCSV(dir)
	must(t, err)
	if applied != 2 || failed != 1 {
		t.Errorf("counts %d %d", applied, failed)
	}
	text, _ := os.ReadFile(filepath.Join(dir, AppliedCSVName))
	lines := strings.Split(strings.TrimSpace(string(text)), "\n")
	if lines[0] != strings.Join(AppliedColumns, ",") || !strings.HasPrefix(lines[1], `1,"Dev, Sênior",ACME`) || !strings.HasPrefix(lines[2], "2,") {
		t.Errorf("applied csv:\n%s", text)
	}
	rows, err := ReadCSV(filepath.Join(dir, FailedCSVName))
	must(t, err)
	if len(rows) != 1 || rows[0]["Assumed Reason"] != "modal closed" || rows[0]["Screenshot Name"] != "3.png" {
		t.Errorf("failed csv: %+v", rows)
	}
	// Round trip.
	back, _ := ReadCSV(filepath.Join(dir, AppliedCSVName))
	if j := JobFromAppliedRow(back[1]); j.Outcome != External || j.ExternalLink != "https://acme/jobs" {
		t.Errorf("external round trip: %+v", j)
	}
	if j := JobFromAppliedRow(back[0]); j.Outcome != Applied || j.Title != "Dev, Sênior" {
		t.Errorf("applied round trip: %+v", j)
	}
	if rows, err := ReadCSV(filepath.Join(dir, "nope.csv")); rows != nil || err != nil {
		t.Errorf("missing csv should be empty: %v %v", rows, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
