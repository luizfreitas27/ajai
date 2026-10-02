package store

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The Python bot's spreadsheets, column for column.
const (
	AppliedCSVName = "all_applied_applications_history.csv"
	FailedCSVName  = "all_failed_applications_history.csv"
)

// AppliedColumns and FailedColumns are the CSV headers the Python bot wrote.
var (
	AppliedColumns = []string{"Job ID", "Title", "Company", "Work Location", "Work Style", "About Job", "Experience required",
		"Skills required", "HR Name", "HR Link", "Resume", "Re-posted", "Date Posted", "Date Applied", "Job Link",
		"External Job link", "Questions Found", "Connect Request"}
	FailedColumns = []string{"Job ID", "Job Link", "Resume Tried", "Date listed", "Date Tried", "Assumed Reason", "Stack Trace",
		"External Job link", "Screenshot Name"}
)

// AppliedRow renders an applied or external job in the applied spreadsheet's columns.
func AppliedRow(j Job) []string {
	return []string{j.JobID, j.Title, j.Company, j.WorkLocation, j.WorkStyle, j.Description, j.ExperienceRequired,
		j.Skills, j.HRName, j.HRLink, j.Resume, j.Reposted, j.DatePosted, j.DateApplied, j.JobLink, j.ExternalLink,
		j.Questions, j.ConnectRequest}
}

// FailedRow renders a failed job in the failed spreadsheet's columns.
func FailedRow(j Job) []string {
	return []string{j.JobID, j.JobLink, j.Resume, j.DatePosted, j.RecordedAt, j.Reason, j.StackTrace, j.ExternalLink, j.Screenshot}
}

// JobFromAppliedRow reads a row of the applied spreadsheet (header-keyed). A
// row whose Date Applied is "Pending" with an external link is an external job.
func JobFromAppliedRow(row map[string]string) Job {
	j := Job{Outcome: Applied, JobID: row["Job ID"], Title: row["Title"], Company: row["Company"],
		WorkLocation: row["Work Location"], WorkStyle: row["Work Style"], Description: row["About Job"],
		ExperienceRequired: row["Experience required"], Skills: row["Skills required"], HRName: row["HR Name"],
		HRLink: row["HR Link"], Resume: row["Resume"], Reposted: row["Re-posted"], DatePosted: row["Date Posted"],
		DateApplied: row["Date Applied"], JobLink: row["Job Link"], ExternalLink: row["External Job link"],
		Questions: row["Questions Found"], ConnectRequest: row["Connect Request"]}
	if j.ExternalLink != "" && j.DateApplied == "Pending" {
		j.Outcome = External
	}
	// The moment the row was written: when it was applied, else (external, "Pending") when posted.
	for _, candidate := range []string{j.DateApplied, j.DatePosted} {
		if t, ok := ParseTime(candidate); ok {
			j.RecordedAt = Timestamp(t)
			break
		}
	}
	return j
}

// JobFromFailedRow reads a row of the failed spreadsheet.
func JobFromFailedRow(row map[string]string) Job {
	return Job{Outcome: Failed, JobID: row["Job ID"], JobLink: row["Job Link"], Resume: row["Resume Tried"],
		DatePosted: row["Date listed"], RecordedAt: NormalizeTimestamp(row["Date Tried"]), Reason: row["Assumed Reason"],
		StackTrace: row["Stack Trace"], ExternalLink: row["External Job link"], Screenshot: row["Screenshot Name"]}
}

// ExportCSV writes both spreadsheets into dir and returns how many rows each got.
func (s *Store) ExportCSV(dir string) (applied, failed int, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, 0, err
	}
	jobs, err := s.ListJobs(JobFilter{Outcomes: []Outcome{Applied, External}})
	if err != nil {
		return 0, 0, err
	}
	if applied, err = writeCSV(filepath.Join(dir, AppliedCSVName), AppliedColumns, jobs, AppliedRow); err != nil {
		return 0, 0, err
	}
	fails, err := s.ListJobs(JobFilter{Outcomes: []Outcome{Failed}})
	if err != nil {
		return 0, 0, err
	}
	if failed, err = writeCSV(filepath.Join(dir, FailedCSVName), FailedColumns, fails, FailedRow); err != nil {
		return 0, 0, err
	}
	return applied, failed, nil
}

func writeCSV(path string, header []string, jobs []Job, row func(Job) []string) (int, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		_ = f.Close()
		return 0, err
	}
	// Oldest first, like an append-only spreadsheet.
	for i := len(jobs) - 1; i >= 0; i-- {
		if err := w.Write(row(jobs[i])); err != nil {
			_ = f.Close()
			return 0, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		_ = f.Close()
		return 0, err
	}
	return len(jobs), f.Close()
}

// ReadCSV parses a header-keyed spreadsheet. Missing files yield nil, nil.
func ReadCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []map[string]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		row := map[string]string{}
		for i, h := range header {
			if i < len(rec) {
				row[h] = rec[i]
			}
		}
		out = append(out, row)
	}
	return out, nil
}
