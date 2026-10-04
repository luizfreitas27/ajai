package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/schedule"
)

func TestTheScheduleMigrationUpgradesAPhaseOneDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ajai.db")
	// A database as phase 1 left it: only the first migration applied.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := migrations.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`, string(body),
		`INSERT INTO schema_migrations VALUES ('migrations/0001_initial.sql', '2026-10-01T10:00:00Z')`,
		`INSERT INTO runs (started_at, market) VALUES ('2026-10-01T10:00:00Z', 'brasil')`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if runs, _ := s.ListRuns(0); len(runs) != 1 {
		t.Error("the history must survive the migration")
	}
	if ws, err := s.ListWindows(); err != nil || len(ws) != 0 {
		t.Errorf("%v %v", ws, err)
	}
}

func TestWindowsAndOpenings(t *testing.T) {
	s := open(t)
	w := schedule.Window{Days: []time.Weekday{time.Monday, time.Friday}, Start: 9 * 60, End: schedule.EndOfDay, Market: "brasil", DryRun: true, Enabled: true}
	id, err := s.AddWindow(w)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetWindow(id)
	w.ID = id
	if err != nil || got == nil || got.String() != w.String() || got.ID != id || !got.Enabled || !got.DryRun {
		t.Fatalf("%+v %v", got, err)
	}
	id2, _ := s.AddWindow(schedule.Window{Days: []time.Weekday{time.Sunday}, Start: 60, End: 120, Enabled: true})
	if ok, err := s.SetWindowEnabled(id2, false); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.SetWindowEnabled(999, true); ok {
		t.Error("no such window")
	}
	all, _ := s.ListWindows()
	if len(all) != 2 || all[0].ID != id || all[1].Enabled {
		t.Errorf("both, oldest first, the second disabled: %+v", all)
	}
	if none, err := s.GetWindow(999); none != nil || err != nil {
		t.Errorf("%v %v", none, err)
	}

	// Openings: one record per window and date, replaced when recorded again.
	runID, _ := s.StartRun(context.Background(), "brasil", true)
	k := schedule.Key{WindowID: id, Date: "2026-10-02"}
	if err := s.RecordOpening(k, schedule.OutcomeSkipped, "a terminal run was in progress", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOpening(k, schedule.OutcomeStarted, "", runID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOpening(schedule.Key{WindowID: id, Date: "2026-09-25"}, schedule.OutcomeStarted, "", 0); err != nil {
		t.Fatal(err)
	}
	handled, err := s.HandledOpenings("2026-10-01")
	if err != nil || len(handled) != 1 || !handled[k] {
		t.Errorf("only openings since the date: %v %v", handled, err)
	}
	if rec, err := s.GetOpening(k); err != nil || rec == nil || rec.Outcome != schedule.OutcomeStarted || rec.RunID != runID || rec.Detail != "" {
		t.Errorf("replaced: %+v %v", rec, err)
	}
	if rec, err := s.GetOpening(schedule.Key{WindowID: id, Date: "2030-01-01"}); rec != nil || err != nil {
		t.Errorf("never handled: %+v %v", rec, err)
	}

	// Deleting a window forgets its openings.
	if ok, err := s.DeleteWindow(id); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.DeleteWindow(id); ok {
		t.Error("already gone")
	}
	if handled, _ := s.HandledOpenings("2000-01-01"); len(handled) != 0 {
		t.Errorf("openings of a deleted window must go too: %v", handled)
	}

	// The schema refuses what validation should have caught.
	if _, err := s.AddWindow(schedule.Window{Days: []time.Weekday{time.Monday}, Start: 600, End: 540, Enabled: true}); err == nil {
		t.Error("an end before the start must not be stored")
	}
	if err := s.RecordOpening(schedule.Key{WindowID: id2, Date: "2026-10-02"}, "maybe", "", 0); err == nil {
		t.Error("an unknown outcome must not be stored")
	}
}

func TestWindowsAreValidatedAgainstTheOthersBeforeTheyAreAddedOrTurnedOn(t *testing.T) {
	s := open(t)
	markets := []string{"brasil", "internacional"}
	monday := func(start, end schedule.Clock) schedule.Window {
		return schedule.Window{Days: []time.Weekday{time.Monday}, Start: start, End: end, Enabled: true}
	}
	first, err := s.AddValidWindow(monday(9*60, 11*60), markets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddValidWindow(monday(10*60, 12*60), markets); err == nil || !strings.Contains(err.Error(), "overlaps window") {
		t.Fatalf("an overlap is refused: %v", err)
	}
	if w, err := s.SetWindowEnabledValid(first, false, markets); err != nil || w.Enabled {
		t.Fatalf("%+v %v", w, err)
	}
	if _, err := s.AddValidWindow(monday(10*60, 12*60), markets); err != nil { // over a window that is off
		t.Fatal(err)
	}
	if _, err := s.SetWindowEnabledValid(first, true, markets); err == nil || !strings.Contains(err.Error(), "overlaps window") {
		t.Errorf("turning on a window something now overlaps is refused: %v", err)
	}
	if _, err := s.SetWindowEnabledValid(999, true, markets); !errors.Is(err, ErrNoWindow) {
		t.Errorf("%v", err)
	}
	if _, err := s.AddValidWindow(schedule.Window{Days: []time.Weekday{time.Friday}, Start: 9 * 60, End: 10 * 60, Market: "usa", Enabled: true}, markets); err == nil {
		t.Error("an unknown market is refused")
	}
}

func TestAWindowsLastOpeningAndItsRun(t *testing.T) {
	s := open(t)
	id, _ := s.AddWindow(schedule.Window{Days: []time.Weekday{time.Tuesday}, Start: 14 * 60, End: 16 * 60, Enabled: true})
	other, _ := s.AddWindow(schedule.Window{Days: []time.Weekday{time.Friday}, Start: 9 * 60, End: 10 * 60, Enabled: true})
	if last, err := s.LastOpening(id); last != nil || err != nil {
		t.Fatalf("no opening yet: %+v %v", last, err)
	}
	runID, _ := s.StartRun(context.Background(), "brasil", true)
	if err := s.FinishRun(context.Background(), Run{ID: runID, Applied: 3, Skipped: 4, Reason: "the search ran out of jobs"}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []struct {
		k       schedule.Key
		outcome schedule.Outcome
		detail  string
		run     int64
	}{
		{schedule.Key{WindowID: id, Date: "2026-09-29"}, schedule.OutcomeStarted, "", runID},
		{schedule.Key{WindowID: id, Date: "2026-09-22"}, schedule.OutcomeSkipped, "another run was in progress", 0},
		{schedule.Key{WindowID: other, Date: "2026-10-02"}, schedule.OutcomeFailed, "no Chrome", 0},
	} {
		if err := s.RecordOpening(o.k, o.outcome, o.detail, o.run); err != nil {
			t.Fatal(err)
		}
	}
	last, err := s.LastOpening(id)
	if err != nil || last == nil || last.Date != "2026-09-29" || last.Outcome != schedule.OutcomeStarted || last.RunID != runID {
		t.Fatalf("the latest of the window's own: %+v %v", last, err)
	}
	run, err := s.GetRun(last.RunID)
	if err != nil || run == nil || run.Applied != 3 || run.Skipped != 4 || run.Reason != "the search ran out of jobs" || !run.DryRun {
		t.Errorf("%+v %v", run, err)
	}
	if none, err := s.GetRun(999); none != nil || err != nil {
		t.Errorf("no such run: %+v %v", none, err)
	}
}
