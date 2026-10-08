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
	s, err := Open(filepath.Join(t.TempDir(), "sub", "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesTheSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ajai.db")
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

func TestAnswerOptionsRoundTrip(t *testing.T) {
	s := open(t)
	must(t, s.UpsertAnswer(Answer{ID: "r", Label: "Relocate?", Normalised: "relocate", Kind: "radio", Source: "form", State: "pending", Options: []string{"Yes", "No, not now"}}))
	must(t, s.UpsertAnswer(Answer{ID: "t", Label: "Years", Normalised: "years", Kind: "text", Answer: "3", Source: "ai", State: "pending"}))
	got, _ := s.GetAnswer("r")
	found, _ := s.FindAnswer("radio", "relocate")
	all, _ := s.ListAnswers("")
	if got == nil || strings.Join(got.Options, "|") != "Yes|No, not now" || found == nil || len(found.Options) != 2 || len(all) != 2 {
		t.Fatalf("get %+v find %+v list %+v", got, found, all)
	}
	if text, _ := s.GetAnswer("t"); text.Options != nil {
		t.Errorf("a text question has no options: %#v", text.Options)
	}
}

func TestTelegramMessagesLeadToTheirAnswerUntilForgotten(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	must(t, s.UpsertAnswer(Answer{ID: "a1", Label: "Years", Normalised: "years", Kind: "text", Source: "form", State: "pending"}))
	must(t, s.UpsertAnswer(Answer{ID: "a2", Label: "City", Normalised: "city", Kind: "text", Source: "form", State: "pending"}))
	must(t, s.UpsertAnswer(Answer{ID: "a3", Label: "Shift", Normalised: "shift", Kind: "text", Source: "form", State: "pending"}))
	s.now = func() time.Time { return t0 }
	must(t, s.LinkTelegramMessage(TelegramMessage{"1", "42", 7}, "a1"))
	must(t, s.LinkTelegramMessage(TelegramMessage{"1", "42", 8}, "a2"))
	s.now = func() time.Time { return t0.Add(40 * 24 * time.Hour) }
	must(t, s.LinkTelegramMessage(TelegramMessage{"1", "42", 9}, "a3"))
	if id, err := s.TelegramMessageAnswer(TelegramMessage{"1", "42", 7}); id != "a1" || err != nil {
		t.Errorf("message 7: %q %v", id, err)
	}
	for _, other := range []TelegramMessage{{"1", "43", 7}, {"2", "42", 7}} {
		if id, _ := s.TelegramMessageAnswer(other); id != "" {
			t.Errorf("%+v is another chat's or another bot's message: %q", other, id)
		}
	}
	if _, err := s.DeleteAnswer("a1"); err != nil {
		t.Fatal(err)
	}
	must(t, s.ForgetTelegramMessages(t0.Add(10*24*time.Hour)))
	for msg, want := range map[int64]string{7: "", 8: "", 9: "a3"} {
		if id, _ := s.TelegramMessageAnswer(TelegramMessage{"1", "42", msg}); id != want {
			t.Errorf("message %d after forgetting: %q, want %q (a deleted answer's and an old link go)", msg, id, want)
		}
	}
}

func TestTimestampsAreNormalisedAndBlankIdsGenerated(t *testing.T) {
	s := open(t)
	must(t, s.UpsertAnswer(Answer{Label: "Q", Normalised: "q", Kind: "text", Answer: "a", Source: "ai", State: "pending", CreatedAt: "2026-10-01T16:45:25", UpdatedAt: "2026-10-01 16:45:25.5"}))
	all, _ := s.ListAnswers("")
	if len(all[0].ID) != 12 || !strings.HasPrefix(all[0].CreatedAt, "2026-10-01T16:45:25") || !strings.HasPrefix(all[0].UpdatedAt, "2026-10-01T16:45:25") || strings.Contains(all[0].UpdatedAt, " ") {
		t.Errorf("%+v", all[0])
	}
	// The same id with a new label replaces the old row instead of failing.
	must(t, s.UpsertAnswer(Answer{ID: all[0].ID, Label: "Q2", Normalised: "q2", Kind: "text", Answer: "b", Source: "ai", State: "pending"}))
	all, _ = s.ListAnswers("")
	if len(all) != 1 || all[0].Normalised != "q2" {
		t.Errorf("%+v", all)
	}
	must(t, s.UpsertJob(Job{JobID: "7", Outcome: Failed, RecordedAt: "2026-10-01 11:00:00.123456"}))
	j, _ := s.GetJob("7")
	if strings.Contains(j.RecordedAt, " ") {
		t.Errorf("job timestamp not normalised: %q", j.RecordedAt)
	}
	if missing, err := s.GetJob("nope"); missing != nil || err != nil {
		t.Errorf("%v %v", missing, err)
	}
	cases := map[string]bool{"2026-10-01": true, "2026-10-01 11:00:00": true, "2026-10-01T11:00:00-03:00": true, "Pending": false, "": false, "Unknown": false}
	for in, ok := range cases {
		if _, got := ParseTime(in); got != ok {
			t.Errorf("ParseTime(%q) ok = %v", in, got)
		}
	}
}

func TestRunsRecordStartAndFinish(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, err := s.StartRun(ctx, "brasil", true)
	must(t, err)
	must(t, s.FinishRun(ctx, Run{ID: id, Applied: 2, Skipped: 5, PendingAnswers: 1, Reason: "pacing limit"}))
	runs, _ := s.ListRuns(0)
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

func TestJobsAreFoundByTextAndPaged(t *testing.T) {
	s := open(t)
	for i, j := range []Job{
		{JobID: "1", Outcome: Applied, Title: "Go Developer", Company: "Acme", WorkLocation: "Recife"},
		{JobID: "2", Outcome: Skipped, Title: "Python dev", Company: "GoFast Ltd", WorkLocation: "Lisboa"},
		{JobID: "3", Outcome: Failed, Title: "QA", Company: "Initech", WorkLocation: "São Paulo (go_live)"},
		{JobID: "4", Outcome: Applied, Title: "Data 100% remote", Company: "Umbrella", WorkLocation: "Remote"},
	} {
		j.RecordedAt = Timestamp(time.Date(2026, 10, 1, 9, i, 0, 0, time.UTC))
		must(t, s.UpsertJob(j))
	}
	ids := func(f JobFilter) string {
		t.Helper()
		jobs, err := s.ListJobs(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, j := range jobs {
			out = append(out, j.JobID)
		}
		return strings.Join(out, ",")
	}
	for text, want := range map[string]string{
		"go":    "3,2,1", // title, company and location, ignoring case
		"GO_":   "3",     // _ is the letter, not a wildcard
		"100%":  "4",     // % too
		"lisb":  "2",
		"nope":  "",
		"  qa ": "3",
		"SÃO":   "3", // case folded in every alphabet
	} {
		if got := ids(JobFilter{Text: text}); got != want {
			t.Errorf("text %q: %s, want %s", text, got, want)
		}
	}
	if got := ids(JobFilter{Text: "go", Outcomes: []Outcome{Applied}}); got != "1" {
		t.Errorf("text and outcome: %s", got)
	}
	if got := ids(JobFilter{Limit: 2, Offset: 1}); got != "3,2" {
		t.Errorf("page: %s", got)
	}
	if got := ids(JobFilter{Offset: 3}); got != "1" {
		t.Errorf("offset alone: %s", got)
	}
}

func TestTheDatabaseIsTheUsersAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "ajai.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_ = os.Chmod(path, 0o644) // a database from before
	_ = os.Chmod(filepath.Dir(path), 0o755)
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for file, want := range map[string]os.FileMode{path: 0o600, path + "-wal": 0o600, path + "-shm": 0o600, filepath.Dir(path): 0o700} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: %o, want %o", file, info.Mode().Perm(), want)
		}
	}
}

func TestExportedSpreadsheetsAreTheUsersAloneInAFolderTheyChose(t *testing.T) {
	s := open(t)
	must(t, s.UpsertJob(Job{JobID: "1", Outcome: Applied, Title: "Dev", Company: "ACME"}))
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o755)
	must(t, os.WriteFile(filepath.Join(dir, AppliedCSVName), nil, 0o644)) // exported before
	if _, _, err := s.ExportCSV(dir); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]os.FileMode{dir: 0o755, filepath.Join(dir, AppliedCSVName): 0o600, filepath.Join(dir, FailedCSVName): 0o600} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: %o, want %o", file, info.Mode().Perm(), want)
		}
	}
}
