// Package store is the local SQLite database: runs, jobs (applied, external,
// failed, skipped), remembered answers, schedule windows and the openings
// handled, the Telegram messages that show remembered answers, and small
// key/value state. Pure-Go
// driver, embedded migrations applied on open, CSV export in the layout the
// Python bot wrote.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"modernc.org/sqlite" // the database/sql driver
)

// ajai_contains(text, needle) is 1 when text holds needle, ignoring case in
// every alphabet (SQLite's LIKE folds ASCII only: "SÃO" would miss "São");
// needle comes lowered.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("ajai_contains", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		text := func(v driver.Value) string {
			switch v := v.(type) {
			case string:
				return v
			case []byte:
				return string(v)
			}
			return ""
		}
		if strings.Contains(strings.ToLower(text(args[0])), text(args[1])) {
			return int64(1), nil
		}
		return int64(0), nil
	})
}

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps the database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating when needed) the database at path and applies pending
// migrations. One connection: SQLite serialises writers anyway and this keeps
// a process's own goroutines from stepping on each other.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating %s: %w", path, err)
	}
	return s, nil
}

// WithClock stamps records with now instead of the wall clock (tests).
func (s *Store) WithClock(now func() time.Time) *Store {
	s.now = now
	return s
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, name, Timestamp(s.now())); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------------ state

// SetState stores a small value under key (daily-limit marks, last run, ...).
func (s *Store) SetState(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO state (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, Timestamp(s.now()))
	return err
}

// GetState returns the value under key, or "" when absent.
func (s *Store) GetState(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM state WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// ------------------------------------------------------------------ jobs

// Outcome of a job the tool looked at.
type Outcome string

// Outcomes, matching CONTEXT.md.
const (
	Applied  Outcome = "applied"
	External Outcome = "external"
	Failed   Outcome = "failed"
	Skipped  Outcome = "skipped"
)

// Job is one row of the jobs table. Text fields mirror the Python CSV columns so
// import and export are lossless.
type Job struct {
	JobID              string
	Outcome            Outcome
	Reason             string // skip reason or failure reason
	Title              string
	Company            string
	WorkLocation       string
	WorkStyle          string
	Description        string
	ExperienceRequired string
	Skills             string
	HRName             string
	HRLink             string
	Resume             string
	Reposted           string
	DatePosted         string
	DateApplied        string
	JobLink            string
	ExternalLink       string
	Questions          string
	ConnectRequest     string
	StackTrace         string
	Screenshot         string
	RunID              int64
	RecordedAt         string
}

// UpsertJob inserts or replaces a job by its LinkedIn id.
func (s *Store) UpsertJob(j Job) error {
	if j.RecordedAt == "" {
		j.RecordedAt = Timestamp(s.now())
	} else {
		j.RecordedAt = NormalizeTimestamp(j.RecordedAt)
	}
	_, err := s.db.Exec(`INSERT INTO jobs (job_id, outcome, reason, title, company, work_location, work_style, description,
		experience_required, skills, hr_name, hr_link, resume, reposted, date_posted, date_applied, job_link, external_link,
		questions, connect_request, stack_trace, screenshot, run_id, recorded_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id) DO UPDATE SET outcome=excluded.outcome, reason=excluded.reason, title=excluded.title,
		company=excluded.company, work_location=excluded.work_location, work_style=excluded.work_style,
		description=excluded.description, experience_required=excluded.experience_required, skills=excluded.skills,
		hr_name=excluded.hr_name, hr_link=excluded.hr_link, resume=excluded.resume, reposted=excluded.reposted,
		date_posted=excluded.date_posted, date_applied=excluded.date_applied, job_link=excluded.job_link,
		external_link=excluded.external_link, questions=excluded.questions, connect_request=excluded.connect_request,
		stack_trace=excluded.stack_trace, screenshot=excluded.screenshot, run_id=excluded.run_id, recorded_at=excluded.recorded_at`,
		j.JobID, string(j.Outcome), j.Reason, j.Title, j.Company, j.WorkLocation, j.WorkStyle, j.Description,
		j.ExperienceRequired, j.Skills, j.HRName, j.HRLink, j.Resume, j.Reposted, j.DatePosted, j.DateApplied, j.JobLink,
		j.ExternalLink, j.Questions, j.ConnectRequest, j.StackTrace, j.Screenshot, nullInt(j.RunID), j.RecordedAt)
	return err
}

// GetJob returns the job with this LinkedIn id, or nil when unknown.
func (s *Store) GetJob(jobID string) (*Job, error) {
	jobs, err := s.listJobs("job_id = ?", []any{jobID}, 1, 0)
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return &jobs[0], nil
}

// JobFilter narrows ListJobs. Zero values mean "any".
type JobFilter struct {
	Outcomes []Outcome
	Since    time.Time // recorded_at on or after
	Text     string    // in the title, the company or the location, ignoring case
	Limit    int
	Offset   int // rows skipped first, for paging
}

// ListJobs returns jobs newest first.
func (s *Store) ListJobs(f JobFilter) ([]Job, error) {
	where, args := []string{"1=1"}, []any{}
	if len(f.Outcomes) > 0 {
		marks := make([]string, len(f.Outcomes))
		for i, o := range f.Outcomes {
			marks[i] = "?"
			args = append(args, string(o))
		}
		where = append(where, "outcome IN ("+strings.Join(marks, ",")+")")
	}
	if !f.Since.IsZero() {
		where = append(where, "recorded_at >= ?")
		args = append(args, f.Since.Format(time.RFC3339))
	}
	if text := strings.ToLower(strings.TrimSpace(f.Text)); text != "" {
		where = append(where, `(ajai_contains(title, ?) OR ajai_contains(company, ?) OR ajai_contains(work_location, ?))`)
		args = append(args, text, text, text)
	}
	return s.listJobs(strings.Join(where, " AND "), args, f.Limit, f.Offset)
}

func (s *Store) listJobs(where string, args []any, limit, offset int) ([]Job, error) {
	q := `SELECT job_id, outcome, reason, title, company, work_location, work_style, description, experience_required,
		skills, hr_name, hr_link, resume, reposted, date_posted, date_applied, job_link, external_link, questions,
		connect_request, stack_trace, screenshot, COALESCE(run_id, 0), recorded_at FROM jobs WHERE ` +
		where + ` ORDER BY recorded_at DESC, rowid DESC`
	switch {
	case limit > 0:
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, max(0, offset))
	case offset > 0:
		q += fmt.Sprintf(" LIMIT -1 OFFSET %d", offset)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		var j Job
		var outcome string
		if err := rows.Scan(&j.JobID, &outcome, &j.Reason, &j.Title, &j.Company, &j.WorkLocation, &j.WorkStyle, &j.Description,
			&j.ExperienceRequired, &j.Skills, &j.HRName, &j.HRLink, &j.Resume, &j.Reposted, &j.DatePosted, &j.DateApplied,
			&j.JobLink, &j.ExternalLink, &j.Questions, &j.ConnectRequest, &j.StackTrace, &j.Screenshot, &j.RunID, &j.RecordedAt); err != nil {
			return nil, err
		}
		j.Outcome = Outcome(outcome)
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountJobs returns how many jobs have each outcome.
func (s *Store) CountJobs() (map[Outcome]int, error) {
	rows, err := s.db.Query(`SELECT outcome, COUNT(*) FROM jobs GROUP BY outcome`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[Outcome]int{}
	for rows.Next() {
		var o string
		var n int
		if err := rows.Scan(&o, &n); err != nil {
			return nil, err
		}
		out[Outcome(o)] = n
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------ answers

// Answer is one remembered answer, the same shape as the Python memory entry.
type Answer struct {
	ID          string
	Label       string
	Normalised  string
	Kind        string // text, textarea, select, radio, checkbox
	Answer      string
	Source      string // ai, user, form
	State       string // pending, approved
	Uses        int
	LastJobLink string
	CreatedAt   string
	UpdatedAt   string
	// Options are the texts a select or radio offered when it was remembered
	// (placeholders left out); empty for other kinds and older entries.
	Options []string
}

// NewAnswerID returns a fresh 12-hex-digit id, the Python memory's shape.
func NewAnswerID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// UpsertAnswer inserts or replaces an answer. The (kind, normalised) pair is
// unique: a second entry for the same question replaces the first. A blank id
// gets a new one; timestamps are normalised to Timestamp's format.
func (s *Store) UpsertAnswer(a Answer) error {
	now := Timestamp(s.now())
	if a.ID == "" {
		a.ID = NewAnswerID()
	}
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	if a.UpdatedAt == "" {
		a.UpdatedAt = now
	}
	a.CreatedAt, a.UpdatedAt = NormalizeTimestamp(a.CreatedAt), NormalizeTimestamp(a.UpdatedAt)
	// Two statements rather than two ON CONFLICT clauses: the same question may
	// arrive under a new id (re-import) or the same id under a new label (edit).
	if _, err := s.db.Exec(`DELETE FROM answers WHERE (kind = ? AND normalised = ?) OR id = ?`, a.Kind, a.Normalised, a.ID); err != nil {
		return err
	}
	options, _ := json.Marshal(append([]string{}, a.Options...)) // [] rather than null
	_, err := s.db.Exec(`INSERT INTO answers (`+answerColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Label, a.Normalised, a.Kind, a.Answer, a.Source, a.State, a.Uses, a.LastJobLink, a.CreatedAt, a.UpdatedAt, string(options))
	return err
}

const answerColumns = `id, label, normalised, kind, answer, source, state, uses, last_job_link, created_at, updated_at, options`

// scanAnswer reads one row selected with answerColumns.
func scanAnswer(row interface{ Scan(...any) error }) (Answer, error) {
	var a Answer
	var options string
	err := row.Scan(&a.ID, &a.Label, &a.Normalised, &a.Kind, &a.Answer, &a.Source, &a.State, &a.Uses, &a.LastJobLink, &a.CreatedAt, &a.UpdatedAt, &options)
	// Unreadable options (a hand-edited database) are none: they are only a check.
	if err == nil && (json.Unmarshal([]byte(options), &a.Options) != nil || len(a.Options) == 0) {
		a.Options = nil
	}
	return a, err
}

// GetAnswer returns the answer with this id, or nil.
func (s *Store) GetAnswer(id string) (*Answer, error) {
	return s.oneAnswer(`id = ?`, id)
}

// DeleteAnswer forgets an answer; true when something was removed.
func (s *Store) DeleteAnswer(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM answers WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteAnswersInState removes every answer in state and returns how many.
func (s *Store) DeleteAnswersInState(state string) (int, error) {
	res, err := s.db.Exec(`DELETE FROM answers WHERE state = ?`, state)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CountAnswers returns how many answers are in state ("" for all).
func (s *Store) CountAnswers(state string) (int, error) {
	var n int
	var err error
	if state == "" {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM answers`).Scan(&n)
	} else {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM answers WHERE state = ?`, state).Scan(&n)
	}
	return n, err
}

func (s *Store) oneAnswer(where string, args ...any) (*Answer, error) {
	a, err := scanAnswer(s.db.QueryRow(`SELECT `+answerColumns+` FROM answers WHERE `+where, args...))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// FindAnswer returns the answer remembered for this question, or nil.
func (s *Store) FindAnswer(kind, normalised string) (*Answer, error) {
	return s.oneAnswer(`kind = ? AND normalised = ?`, kind, normalised)
}

// ListAnswers returns answers, newest updated first; state "" means all.
func (s *Store) ListAnswers(state string) ([]Answer, error) {
	q := `SELECT ` + answerColumns + ` FROM answers`
	var args []any
	if state != "" {
		q += ` WHERE state = ?`
		args = append(args, state)
	}
	rows, err := s.db.Query(q+` ORDER BY updated_at DESC, rowid DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Answer
	for rows.Next() {
		a, err := scanAnswer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TelegramMessage names one message a bot sent: message ids are counted per
// bot and chat.
type TelegramMessage struct {
	Bot, Chat string
	ID        int64
}

// LinkTelegramMessage records that m shows the answer answerID, so a reply to
// it reaches the answer.
func (s *Store) LinkTelegramMessage(m TelegramMessage, answerID string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO telegram_messages (bot_id, chat_id, message_id, answer_id, sent_at) VALUES (?,?,?,?,?)`,
		m.Bot, m.Chat, m.ID, answerID, Timestamp(s.now()))
	return err
}

// TelegramMessageAnswer returns the id of the answer m shows, or "" when it
// shows none.
func (s *Store) TelegramMessageAnswer(m TelegramMessage) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT answer_id FROM telegram_messages WHERE bot_id = ? AND chat_id = ? AND message_id = ?`, m.Bot, m.Chat, m.ID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// ForgetTelegramMessages drops the links recorded before cutoff and those to
// answers no longer in the memory.
func (s *Store) ForgetTelegramMessages(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM telegram_messages WHERE sent_at < ? OR answer_id NOT IN (SELECT id FROM answers)`, Timestamp(cutoff))
	return err
}

// ------------------------------------------------------------------ runs

// Run is one row of the runs table.
type Run struct {
	ID             int64
	StartedAt      string
	EndedAt        string
	Market         string
	DryRun         bool
	Applied        int
	External       int
	Failed         int
	Skipped        int
	PendingAnswers int
	Reason         string
}

// StartRun records a run beginning and returns its id.
func (s *Store) StartRun(ctx context.Context, market string, dryRun bool) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO runs (started_at, market, dry_run) VALUES (?, ?, ?)`,
		Timestamp(s.now()), market, boolInt(dryRun))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun records the counts and the reason a run ended.
func (s *Store) FinishRun(ctx context.Context, r Run) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET ended_at = ?, applied = ?, external = ?, failed = ?, skipped = ?,
		pending_answers = ?, reason = ? WHERE id = ?`,
		Timestamp(s.now()), r.Applied, r.External, r.Failed, r.Skipped, r.PendingAnswers, r.Reason, r.ID)
	return err
}

// EndAbandonedRun ends a run its process never ended (killed, crashed) with
// the counts it last saved and r.Reason, unless it has ended already: false
// then, and the run stays as it is.
func (s *Store) EndAbandonedRun(ctx context.Context, r Run) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE runs SET ended_at = ?, applied = ?, external = ?, failed = ?, skipped = ?,
		reason = ? WHERE id = ? AND ended_at IS NULL`,
		Timestamp(s.now()), r.Applied, r.External, r.Failed, r.Skipped, r.Reason, r.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListRuns returns runs newest first; limit 0 means all.
func (s *Store) ListRuns(limit int) ([]Run, error) {
	if limit <= 0 {
		limit = -1 // SQLite: no limit
	}
	return s.listRuns(`ORDER BY id DESC LIMIT ?`, limit)
}

// GetRun returns the run with id, or nil when there is none.
func (s *Store) GetRun(id int64) (*Run, error) {
	runs, err := s.listRuns(`WHERE id = ?`, id)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

func (s *Store) listRuns(tail string, args ...any) ([]Run, error) {
	rows, err := s.db.Query(`SELECT id, started_at, COALESCE(ended_at, ''), market, dry_run, applied, external, failed, skipped,
		pending_answers, COALESCE(reason, '') FROM runs `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		var dry int
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.EndedAt, &r.Market, &dry, &r.Applied, &r.External, &r.Failed, &r.Skipped, &r.PendingAnswers, &r.Reason); err != nil {
			return nil, err
		}
		r.DryRun = dry == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
