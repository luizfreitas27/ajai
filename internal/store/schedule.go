package store

import (
	"database/sql"
	"errors"

	"github.com/luizfreitas27/aja/internal/schedule"
)

// AddWindow stores a schedule window (already validated) and returns its id.
func (s *Store) AddWindow(w schedule.Window) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO schedule_windows (days, start_min, end_min, market, dry_run, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		schedule.EncodeDays(w.Days), int(w.Start), int(w.End), w.Market, boolInt(w.DryRun), boolInt(w.Enabled), Timestamp(s.now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const windowColumns = `id, days, start_min, end_min, market, dry_run, enabled`

func scanWindow(row interface{ Scan(...any) error }) (schedule.Window, error) {
	var w schedule.Window
	var days string
	var start, end, dry, enabled int
	if err := row.Scan(&w.ID, &days, &start, &end, &w.Market, &dry, &enabled); err != nil {
		return w, err
	}
	parsed, err := schedule.ParseDays(days)
	if err != nil {
		return w, err
	}
	w.Days, w.Start, w.End, w.DryRun, w.Enabled = parsed, schedule.Clock(start), schedule.Clock(end), dry == 1, enabled == 1
	return w, nil
}

// ListWindows returns every window, enabled or not, oldest first.
func (s *Store) ListWindows() ([]schedule.Window, error) {
	rows, err := s.db.Query(`SELECT ` + windowColumns + ` FROM schedule_windows ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []schedule.Window
	for rows.Next() {
		w, err := scanWindow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWindow returns one window, or nil when there is no such id.
func (s *Store) GetWindow(id int64) (*schedule.Window, error) {
	w, err := scanWindow(s.db.QueryRow(`SELECT `+windowColumns+` FROM schedule_windows WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// DeleteWindow removes a window and its handled openings; false when there was none.
func (s *Store) DeleteWindow(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM schedule_windows WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetWindowEnabled turns a window on or off; false when there is no such id.
func (s *Store) SetWindowEnabled(id int64, enabled bool) (bool, error) {
	res, err := s.db.Exec(`UPDATE schedule_windows SET enabled = ? WHERE id = ?`, boolInt(enabled), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AddValidWindow adds w once it passes schedule.Validate against the windows
// there are (markets are the valid market names): what `aja schedule add` and
// the TUI both do.
func (s *Store) AddValidWindow(w schedule.Window, markets []string) (int64, error) {
	others, err := s.ListWindows()
	if err != nil {
		return 0, err
	}
	if err := schedule.Validate(w, others, markets); err != nil {
		return 0, err
	}
	return s.AddWindow(w)
}

// ErrNoWindow: no window has that id.
var ErrNoWindow = errors.New("no such schedule window")

// SetWindowEnabledValid turns window id on or off; on, it must first pass
// schedule.Validate against the other windows (one may have been added over it
// meanwhile). It returns the window as it is now.
func (s *Store) SetWindowEnabledValid(id int64, on bool, markets []string) (*schedule.Window, error) {
	w, err := s.GetWindow(id)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, ErrNoWindow
	}
	w.Enabled = on
	if on {
		windows, err := s.ListWindows()
		if err != nil {
			return nil, err
		}
		if err := schedule.Validate(*w, windows, markets); err != nil {
			return nil, err
		}
	}
	switch found, err := s.SetWindowEnabled(id, on); {
	case err != nil:
		return nil, err
	case !found: // deleted meanwhile
		return nil, ErrNoWindow
	}
	return w, nil
}

// RecordOpening records what the scheduler did with an opening; recording it
// again replaces the earlier outcome. runID is 0 when no run started.
func (s *Store) RecordOpening(k schedule.Key, outcome schedule.Outcome, detail string, runID int64) error {
	_, err := s.db.Exec(`INSERT INTO schedule_openings (window_id, date, outcome, detail, run_id, handled_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (window_id, date) DO UPDATE SET outcome = excluded.outcome, detail = excluded.detail,
		run_id = excluded.run_id, handled_at = excluded.handled_at`,
		k.WindowID, k.Date, string(outcome), detail, nullInt(runID), Timestamp(s.now()))
	return err
}

// ForgetOpening removes the record of an opening, so the scheduler treats it as
// not handled (the daemon stopped in the middle of its run).
func (s *Store) ForgetOpening(k schedule.Key) error {
	_, err := s.db.Exec(`DELETE FROM schedule_openings WHERE window_id = ? AND date = ?`, k.WindowID, k.Date)
	return err
}

// HandledOpenings returns the openings handled on or after a local date ("2006-01-02").
func (s *Store) HandledOpenings(since string) (map[schedule.Key]bool, error) {
	rows, err := s.db.Query(`SELECT window_id, date FROM schedule_openings WHERE date >= ?`, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[schedule.Key]bool{}
	for rows.Next() {
		var k schedule.Key
		if err := rows.Scan(&k.WindowID, &k.Date); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// OpeningRecord is what the scheduler recorded for one opening.
type OpeningRecord struct {
	Outcome schedule.Outcome
	Detail  string
	RunID   int64 // 0 when no run started (or it was never recorded)
}

// GetOpening returns the record of one opening, or nil when it was not handled.
func (s *Store) GetOpening(k schedule.Key) (*OpeningRecord, error) {
	var r OpeningRecord
	var outcome string
	var runID sql.NullInt64
	err := s.db.QueryRow(`SELECT outcome, detail, run_id FROM schedule_openings WHERE window_id = ? AND date = ?`, k.WindowID, k.Date).
		Scan(&outcome, &r.Detail, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Outcome, r.RunID = schedule.Outcome(outcome), runID.Int64
	return &r, nil
}
