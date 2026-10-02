// Package store is the local SQLite database: runs, jobs (applied, external,
// failed, skipped), remembered answers and small key/value state. Pure-Go
// driver, embedded migrations applied on open, CSV export in the layout the
// Python bot wrote.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database/sql driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps the database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating when needed) the database at path and applies pending
// migrations. One connection: SQLite serialises writers anyway and this keeps
// the panel and the bot from stepping on each other later.
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
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, name, s.now().Format(time.RFC3339)); err != nil {
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
		key, value, s.now().Format(time.RFC3339))
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
		j.RecordedAt = s.now().Format(time.RFC3339)
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

// JobFilter narrows ListJobs. Zero values mean "any".
type JobFilter struct {
	Outcomes []Outcome
	Since    time.Time // recorded_at on or after
	Limit    int
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
	q := `SELECT job_id, outcome, reason, title, company, work_location, work_style, description, experience_required,
		skills, hr_name, hr_link, resume, reposted, date_posted, date_applied, job_link, external_link, questions,
		connect_request, stack_trace, screenshot, COALESCE(run_id, 0), recorded_at FROM jobs WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY recorded_at DESC, rowid DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
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
}

// Valid answer vocabularies.
var (
	Kinds   = []string{"text", "textarea", "select", "radio", "checkbox"}
	Sources = []string{"ai", "user", "form"}
	States  = []string{"pending", "approved"}
)

// UpsertAnswer inserts or replaces an answer. The (kind, normalised) pair is
// unique: a second entry for the same question replaces the first.
func (s *Store) UpsertAnswer(a Answer) error {
	now := s.now().Format("2006-01-02T15:04:05")
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	if a.UpdatedAt == "" {
		a.UpdatedAt = now
	}
	_, err := s.db.Exec(`INSERT INTO answers (id, label, normalised, kind, answer, source, state, uses, last_job_link, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(kind, normalised) DO UPDATE SET id=excluded.id, label=excluded.label, answer=excluded.answer,
		source=excluded.source, state=excluded.state, uses=excluded.uses, last_job_link=excluded.last_job_link,
		created_at=excluded.created_at, updated_at=excluded.updated_at`,
		a.ID, a.Label, a.Normalised, a.Kind, a.Answer, a.Source, a.State, a.Uses, a.LastJobLink, a.CreatedAt, a.UpdatedAt)
	return err
}

// ListAnswers returns answers, newest updated first; state "" means all.
func (s *Store) ListAnswers(state string) ([]Answer, error) {
	q := `SELECT id, label, normalised, kind, answer, source, state, uses, last_job_link, created_at, updated_at FROM answers`
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
		var a Answer
		if err := rows.Scan(&a.ID, &a.Label, &a.Normalised, &a.Kind, &a.Answer, &a.Source, &a.State, &a.Uses, &a.LastJobLink, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
		s.now().Format(time.RFC3339), market, boolInt(dryRun))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun records the counts and the reason a run ended.
func (s *Store) FinishRun(ctx context.Context, r Run) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET ended_at = ?, applied = ?, external = ?, failed = ?, skipped = ?,
		pending_answers = ?, reason = ? WHERE id = ?`,
		s.now().Format(time.RFC3339), r.Applied, r.External, r.Failed, r.Skipped, r.PendingAnswers, r.Reason, r.ID)
	return err
}

// ListRuns returns runs newest first.
func (s *Store) ListRuns(limit int) ([]Run, error) {
	rows, err := s.db.Query(`SELECT id, started_at, COALESCE(ended_at, ''), market, dry_run, applied, external, failed, skipped,
		pending_answers, COALESCE(reason, '') FROM runs ORDER BY id DESC LIMIT ?`, limit)
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
