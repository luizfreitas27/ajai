package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/store"
)

const (
	// jobsPage is how many jobs are read at a time; the next page is read when
	// the selection reaches the end of what is read.
	jobsPage = 100
	// jobsDetailLines is the room kept under the list for the selected job.
	jobsDetailLines = 7
)

// jobOutcomes are the Jobs tab's filters, in Tab order; "" is every job.
var jobOutcomes = []store.Outcome{"", store.Applied, store.External, store.Failed, store.Skipped}

// jobsTab is the Jobs tab: the job history newest first, filtered by outcome
// and by a text in the title, the company or the location, with the selected
// job's details; a job opens in the default browser.
type jobsTab struct {
	loaded   bool
	outcome  int    // index into jobOutcomes
	text     string // the search applied
	search   *textinput.Model
	jobs     []store.Job
	more     bool // the last page was full: there may be more
	paging   bool // the next page is being read
	counts   map[store.Outcome]int
	problem  string
	cursor   int
	selected string // the selected job's id: the selection follows it when the list is read again
	top      int
	width    int
	height   int
	notice   notice
}

type jobsLoadedMsg struct {
	outcome int
	text    string
	offset  int
	jobs    []store.Job
	more    bool
	counts  map[store.Outcome]int
	problem string
}

type jobOpenedMsg struct{ notice notice }

// loadJobs reads limit jobs from offset for the filter, and the counts.
func loadJobs(d Deps, outcome int, text string, offset, limit int) tea.Cmd {
	return func() tea.Msg {
		f := store.JobFilter{Text: text, Limit: limit, Offset: offset}
		if o := jobOutcomes[outcome]; o != "" {
			f.Outcomes = []store.Outcome{o}
		}
		msg := jobsLoadedMsg{outcome: outcome, text: text, offset: offset}
		var err error
		if msg.jobs, err = d.Store.ListJobs(f); err != nil {
			msg.problem = "Could not read the job history: " + err.Error()
		}
		msg.more = len(msg.jobs) == limit
		if msg.counts, err = d.Store.CountJobs(); err != nil && msg.problem == "" {
			msg.problem = "Could not count the jobs: " + err.Error()
		}
		return msg
	}
}

// reload reads the jobs shown again, as many as are read now.
func (j jobsTab) reload(d Deps) tea.Cmd {
	return loadJobs(d, j.outcome, j.text, 0, max(jobsPage, len(j.jobs)))
}

func (j jobsTab) editing() bool { return j.search != nil }

func (j jobsTab) resized(width, height int) jobsTab {
	j.width, j.height = width, height
	if j.search != nil {
		in := *j.search
		in.Width = inputWidth(width)
		j.search = &in
	}
	return j.settled()
}

func (j jobsTab) listHeight() int {
	head := 2 // title, column names
	if j.notice.text != "" || j.problem != "" || j.search != nil {
		head++
	}
	return max(1, j.height-head-jobsDetailLines)
}

func (j jobsTab) current() (store.Job, bool) {
	if !j.loaded || j.cursor >= len(j.jobs) {
		return store.Job{}, false
	}
	return j.jobs[j.cursor], true
}

// settled keeps the selection in the list and in view, and remembers whose
// it is.
func (j jobsTab) settled() jobsTab {
	j.cursor = max(0, min(j.cursor, len(j.jobs)-1))
	n := j.listHeight()
	if j.cursor < j.top {
		j.top = j.cursor
	}
	if j.cursor >= j.top+n {
		j.top = j.cursor - n + 1
	}
	j.selected = ""
	if job, ok := j.current(); ok {
		j.selected = job.JobID
	}
	return j
}

func (j jobsTab) update(msg tea.Msg, d Deps) (jobsTab, tea.Cmd) {
	switch msg := msg.(type) {
	case jobsLoadedMsg:
		if msg.outcome != j.outcome || msg.text != j.text {
			return j, nil // the filter changed meanwhile
		}
		j.loaded, j.counts, j.problem, j.more = true, msg.counts, msg.problem, msg.more
		if msg.offset == 0 {
			j.jobs = msg.jobs
		} else {
			j.jobs, j.paging = append(append([]store.Job(nil), j.jobs[:min(msg.offset, len(j.jobs))]...), msg.jobs...), false
		}
		for i, job := range j.jobs {
			if job.JobID == j.selected {
				j.cursor = i
			}
		}
		return j.settled(), nil
	case jobOpenedMsg:
		j.notice = msg.notice
		return j.settled(), nil
	case tea.KeyMsg:
		if j.search != nil {
			return j.searchKey(msg, d)
		}
		return j.browseKey(msg, d)
	}
	return j, nil
}

func (j jobsTab) browseKey(msg tea.KeyMsg, d Deps) (jobsTab, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		j.cursor--
	case "down", "j":
		j.cursor++
	case "pgup":
		j.cursor -= 10
	case "pgdown":
		j.cursor += 10
	case "home", "g":
		j.cursor = 0
	case "end", "G":
		j.cursor = len(j.jobs) - 1
	case "tab":
		j.outcome = (j.outcome + 1) % len(jobOutcomes)
		return j.refilter(d)
	case "/":
		in := textinput.New()
		in.Prompt = "search: "
		in.Placeholder = "a title, a company or a location"
		in.SetValue(j.text)
		in.CursorEnd()
		in.Width = inputWidth(j.width)
		in.Cursor.SetMode(cursor.CursorStatic)
		in.Focus()
		j.search = &in
		return j.settled(), nil
	case "c":
		if j.text != "" {
			j.text = ""
			return j.refilter(d)
		}
	case "o":
		if job, ok := j.current(); ok {
			return j, openJob(d, job)
		}
	}
	j = j.settled()
	// The selection reached the end of what is read: read the next page.
	if j.more && !j.paging && len(j.jobs) > 0 && j.cursor == len(j.jobs)-1 {
		j.paging = true
		return j, loadJobs(d, j.outcome, j.text, len(j.jobs), jobsPage)
	}
	return j, nil
}

// refilter reads the list again from the top for a new filter.
func (j jobsTab) refilter(d Deps) (jobsTab, tea.Cmd) {
	j.loaded, j.jobs, j.cursor, j.top, j.selected, j.paging, j.notice = false, nil, 0, 0, "", false, notice{}
	return j, loadJobs(d, j.outcome, j.text, 0, jobsPage)
}

func (j jobsTab) searchKey(msg tea.KeyMsg, d Deps) (jobsTab, tea.Cmd) {
	switch msg.String() {
	case "esc":
		j.search = nil
		return j.settled(), nil
	case "enter":
		j.text, j.search = strings.TrimSpace(j.search.Value()), nil
		return j.refilter(d)
	}
	in, cmd := j.search.Update(msg)
	j.search = &in
	return j, cmd
}

// openJob opens the job in the default browser: the external application for
// an external job, LinkedIn's page otherwise.
func openJob(d Deps, job store.Job) tea.Cmd {
	return func() tea.Msg {
		link := job.JobLink
		if job.Outcome == store.External && job.ExternalLink != "" {
			link = job.ExternalLink
		}
		switch {
		case link == "":
			return jobOpenedMsg{notice{"This job has no link.", levelWarn}}
		case d.OpenURL == nil:
			return jobOpenedMsg{notice{"No browser to open it in: " + link, levelWarn}}
		}
		if err := d.OpenURL(link); err != nil {
			return jobOpenedMsg{notice{"Could not open " + link + ": " + err.Error(), levelFail}}
		}
		return jobOpenedMsg{notice{text: "Opened " + link}}
	}
}

// jobRow lays out the list; it fits the smallest terminal.
const jobRow = "  %-12s  %-8s  %-24s  %-14s  %s"

func (j jobsTab) view(s styles, width int) string {
	filter := "all"
	if o := jobOutcomes[j.outcome]; o != "" {
		filter = string(o)
	}
	head := s.section.Render("Jobs") + "  " + s.activeTab.Render(filter)
	if j.text != "" {
		head += s.dim.Render(fmt.Sprintf(" matching %q (c clears)", j.text))
	}
	var counts []string
	for _, o := range jobOutcomes[1:] {
		counts = append(counts, fmt.Sprintf("%s %d", o, j.counts[o]))
	}
	lines := []string{head + "   " + s.dim.Render(strings.Join(counts, " · "))}
	switch {
	case j.search != nil:
		lines = append(lines, j.search.View())
	case j.problem != "":
		lines = append(lines, s.fail.Render(j.problem))
	case j.notice.text != "":
		lines = append(lines, s.byLevel(j.notice.level).Render(clip(j.notice.text, width)))
	}
	if !j.loaded {
		return strings.Join(append(lines, "Reading…"), "\n")
	}
	if len(j.jobs) == 0 {
		return strings.Join(append(lines, "", s.dim.Render("No jobs here yet.")), "\n")
	}
	lines = append(lines, s.dim.Render(fmt.Sprintf(jobRow, "recorded", "outcome", "title", "company", "location")))
	n := j.listHeight()
	for i := j.top; i < len(j.jobs) && i < j.top+n; i++ {
		job := j.jobs[i]
		mark := "  "
		if i == j.cursor {
			mark = s.key.Render("> ")
		}
		lines = append(lines, mark+strings.TrimPrefix(fmt.Sprintf(jobRow, when(job.RecordedAt, "02 Jan 15:04"), job.Outcome,
			clip(job.Title, 24), clip(job.Company, 14), job.WorkLocation), "  "))
	}
	lines = pad(lines, j.height-jobsDetailLines)
	return strings.Join(append(lines, j.detail(s, width)...), "\n")
}

// detail is the selected job in full.
func (j jobsTab) detail(s styles, width int) []string {
	lines := []string{s.dim.Render(strings.Repeat("─", width))}
	job, ok := j.current()
	if !ok {
		return lines
	}
	place := job.WorkLocation
	if job.WorkStyle != "" {
		place += " (" + job.WorkStyle + ")"
	}
	lines = append(lines, clip(strings.Join(nonEmpty(job.Title, job.Company, place), " · "), width))
	facts := []string{string(job.Outcome) + " " + when(job.RecordedAt, dayTime)}
	if job.DatePosted != "" {
		facts = append(facts, "posted "+job.DatePosted)
	}
	if job.Reason != "" {
		facts = append(facts, job.Reason)
	}
	lines = append(lines, wrap(strings.Join(facts, " · "), width)...)
	if job.JobLink != "" {
		lines = append(lines, s.dim.Render(clip("job: "+job.JobLink, width)))
	}
	if job.ExternalLink != "" {
		lines = append(lines, s.dim.Render(clip("applies at: "+job.ExternalLink, width)))
	}
	if job.Screenshot != "" {
		lines = append(lines, s.dim.Render(clip("screenshot: "+job.Screenshot, width)))
	}
	if q, _, _ := strings.Cut(strings.TrimSpace(job.Questions), "\n"); q != "" {
		lines = append(lines, s.dim.Render(clip("questions: "+q, width)))
	}
	return lines
}

// when writes a store timestamp as layout, local time; as it is if unreadable.
func when(stamp, layout string) string {
	if t, ok := store.ParseTime(stamp); ok {
		return t.Local().Format(layout)
	}
	return stamp
}

func nonEmpty(texts ...string) []string {
	var out []string
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			out = append(out, t)
		}
	}
	return out
}
