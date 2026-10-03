package tui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/store"
)

const (
	// jobsPage is how many jobs are read at a time; the next page is read when
	// the selection reaches the end of what is read.
	jobsPage = 100
	// jobsDetailLines is the least room kept under the list for the selected
	// job (on a terminal too narrow for the details beside it); the details
	// take more when they need it.
	jobsDetailLines = 6
	// jobsEvery: what the tab shows is read again this often while it is
	// shown (a run records jobs as it goes).
	jobsEvery = 5 // refreshes
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
	reading  bool // a read is under way: reads never overlap
	filters  int  // filters applied so far: a read belongs to the filter it was made for
	counts   map[store.Outcome]int
	problem  string
	cursor   int
	selected string // the selected job's id: the selection follows it when the list is read again
	top      int
	width    int
	height   int
	notice   notice
}

// jobsRead is what a read of the job history is.
type jobsRead int

const (
	readFirst jobsRead = iota // the first page, for a new filter
	readAgain                 // what is shown, and anything newer
	readNext                  // the next page
)

type jobsLoadedMsg struct {
	filter  int
	read    jobsRead
	jobs    []store.Job
	more    bool
	counts  map[store.Outcome]int
	problem string
}

type jobOpenedMsg struct{ notice notice }

// read reads the job history for the filter shown, unless a read is under
// way: read decides which rows.
func (j jobsTab) read(d Deps, read jobsRead) (jobsTab, tea.Cmd) {
	if j.reading {
		return j, nil
	}
	f := store.JobFilter{Text: j.text, Limit: jobsPage}
	if o := jobOutcomes[j.outcome]; o != "" {
		f.Outcomes = []store.Outcome{o}
	}
	oldest, shown := time.Time{}, false
	if len(j.jobs) > 0 {
		oldest, shown = store.ParseTime(j.jobs[len(j.jobs)-1].RecordedAt)
	}
	switch {
	case !shown:
		read = readFirst
	case read == readAgain: // every job down to the oldest shown: new ones come on top, none falls off
		f.Since, f.Limit = oldest, 0
	case read == readNext:
		f.Offset = len(j.jobs)
	}
	j.reading = true
	filter := j.filters
	return j, func() tea.Msg {
		msg := jobsLoadedMsg{filter: filter, read: read}
		var err error
		if msg.jobs, err = d.Store.ListJobs(f); err != nil {
			msg.problem = "Could not read the job history: " + err.Error()
		}
		msg.more = f.Limit > 0 && len(msg.jobs) == f.Limit
		if msg.counts, err = d.Store.CountJobs(); err != nil && msg.problem == "" {
			msg.problem = "Could not count the jobs: " + err.Error()
		}
		return msg
	}
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

func (j jobsTab) current() (store.Job, bool) {
	if !j.loaded || j.cursor >= len(j.jobs) {
		return store.Job{}, false
	}
	return j.jobs[j.cursor], true
}

// settled keeps the selection in the list and in view, and remembers whose
// it is.
func (j jobsTab) settled() jobsTab {
	j.cursor, j.top = scroll(j.cursor, j.top, len(j.jobs), j.frame(measure, Deps{}).visible(j.width, j.height))
	j.selected = ""
	if job, ok := j.current(); ok {
		j.selected = job.JobID
	}
	return j
}

func (j jobsTab) update(msg tea.Msg, d Deps) (jobsTab, tea.Cmd) {
	switch msg := msg.(type) {
	case jobsLoadedMsg:
		if msg.filter != j.filters {
			return j, nil // made for a filter no longer shown
		}
		j.reading, j.loaded, j.problem = false, true, msg.problem
		if msg.problem != "" {
			return j.settled(), nil // keep what is shown
		}
		j.counts = msg.counts
		switch msg.read {
		case readNext:
			j.jobs, j.more = appendNew(j.jobs, msg.jobs), msg.more
		case readAgain:
			j.jobs = msg.jobs // as many as before, and the new ones; whether there are more is unchanged
		default:
			j.jobs, j.more = msg.jobs, msg.more
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

// appendNew adds a page to shown, leaving out jobs shown already (a job
// recorded meanwhile moves the rows a page is read from).
func appendNew(shown, page []store.Job) []store.Job {
	seen := map[string]bool{}
	for _, job := range shown {
		seen[job.JobID] = true
	}
	out := append([]store.Job(nil), shown...)
	for _, job := range page {
		if !seen[job.JobID] {
			out = append(out, job)
		}
	}
	return out
}

func (j jobsTab) browseKey(msg tea.KeyMsg, d Deps) (jobsTab, tea.Cmd) {
	if cursor, ok := moved(msg.String(), j.cursor, len(j.jobs)); ok {
		j.cursor = cursor
		j = j.settled()
		if j.more && len(j.jobs) > 0 && j.cursor == len(j.jobs)-1 { // the end of what is read: the next page
			return j.read(d, readNext)
		}
		return j, nil
	}
	switch msg.String() {
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
	return j.settled(), nil
}

// refilter reads the list from the top for a new filter; reads made for the
// old one are dropped when they come back.
func (j jobsTab) refilter(d Deps) (jobsTab, tea.Cmd) {
	j.filters++
	j.loaded, j.reading, j.jobs, j.more, j.cursor, j.top, j.selected, j.notice = false, false, nil, false, 0, 0, "", notice{}
	return j.read(d, readFirst)
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
// an external job, LinkedIn's page otherwise. Only web links are opened: the
// external one comes from an employer's page.
func openJob(d Deps, job store.Job) tea.Cmd {
	return func() tea.Msg {
		link := job.JobLink
		if job.Outcome == store.External && job.ExternalLink != "" {
			link = job.ExternalLink
		}
		u, err := url.Parse(link)
		switch {
		case link == "":
			return jobOpenedMsg{notice{"This job has no link.", levelWarn}}
		case err != nil || (u.Scheme != "https" && u.Scheme != "http"):
			return jobOpenedMsg{notice{"Not opened: " + link + " is not a web link.", levelWarn}}
		case d.OpenURL == nil:
			return jobOpenedMsg{notice{"No browser to open it in: " + link, levelWarn}}
		}
		if err := d.OpenURL(link); err != nil {
			d.logger().Warn("could not open a job from the TUI", "job", job.JobID, "link", link, "error", err)
			return jobOpenedMsg{notice{"Could not open " + link + ": " + err.Error(), levelFail}}
		}
		d.logger().Info("job opened from the TUI", "job", job.JobID, "link", link)
		return jobOpenedMsg{notice{text: "Opened " + link}}
	}
}

// jobRow is a row of the list width wide: the date and the outcome, then the
// title, the company and the location sharing what is left.
func jobRow(width int, recorded, outcome, title, company, location string) string {
	rest := max(3, width-12-2-8-2-2-2)
	t, c := rest*45/100, rest*30/100
	return fmt.Sprintf("%-12s  %-8s  %-*s  %-*s  %s", recorded, outcome, t, clip(title, t), c, clip(company, c), clip(location, rest-t-c))
}

// frame is the tab for the frame to lay out.
func (j jobsTab) frame(s styles, d Deps) listFrame {
	filters := []string{"all"}
	for _, o := range jobOutcomes[1:] {
		filters = append(filters, string(o))
	}
	var counts []string
	for _, o := range jobOutcomes[1:] {
		counts = append(counts, fmt.Sprintf("%s %d", o, j.counts[o]))
	}
	summary := strings.Join(counts, " · ")
	if j.text != "" {
		summary = fmt.Sprintf("matching %q (c clears) · %s", j.text, summary)
	}
	var notes []string
	switch {
	case j.search != nil:
		notes = append(notes, j.search.View())
	case j.problem != "":
		notes = append(notes, s.fail.Render(clip(j.problem, j.width)))
	case j.notice.text != "":
		notes = append(notes, s.byLevel(j.notice.level).Render(clip(j.notice.text, j.width)))
	}
	return listFrame{title: "Jobs", filters: filters, filter: j.outcome, summary: summary, notes: notes,
		loading: !j.loaded, empty: "No jobs here yet.", rows: len(j.jobs), minDetails: jobsDetailLines,
		heading: func(width int) string { return jobRow(width, "recorded", "outcome", "title", "company", "location") },
		row: func(i, width int) string {
			job := j.jobs[i]
			return jobRow(width, localTime(job.RecordedAt, "02 Jan 15:04"), string(job.Outcome), job.Title, job.Company, job.WorkLocation)
		},
		details: func(s styles, width int, beside bool) []string { return j.detail(s, d, width, beside) },
	}
}

func (j jobsTab) view(s styles, d Deps, width int) string {
	return j.frame(s, d).view(s, width, j.height, j.cursor, j.top)
}

// detail is the selected job in full: what it is, what happened and why
// (a failure's error too), its links, its screenshot, the questions answered.
// Beside the list it has the room of its height: a line for each.
func (j jobsTab) detail(s styles, d Deps, width int, beside bool) []string {
	job, ok := j.current()
	if !ok {
		return nil
	}
	place := job.WorkLocation
	if job.WorkStyle != "" {
		place += " (" + job.WorkStyle + ")"
	}
	shot := job.Screenshot
	if shot != "" && !filepath.IsAbs(shot) && d.Paths.ScreenshotDir != "" { // runs record its name; it lives in the screenshots folder
		shot = filepath.Join(d.Paths.ScreenshotDir, shot)
	}
	failure := ""
	if job.Outcome == store.Failed {
		failure, _, _ = strings.Cut(strings.TrimSpace(job.StackTrace), "\n")
	}
	var answered []string
	if q := strings.TrimSpace(job.Questions); q != "" {
		answered = strings.Split(q, "; ") // runs record them joined so
	}
	if beside {
		lines := wrap(job.Title, width)
		lines = append(lines, wrap(strings.Join(nonEmpty(job.Company, place), " · "), width)...)
		lines = append(lines, "", string(job.Outcome)+" "+localTime(job.RecordedAt, dayTime))
		if job.DatePosted != "" {
			lines = append(lines, "posted "+job.DatePosted)
		}
		if job.Reason != "" {
			lines = append(lines, wrap(job.Reason, width)...)
		}
		if failure != "" {
			lines = append(lines, wrapStyled(s.fail, "error: "+failure, width)...)
		}
		lines = append(lines, "")
		for _, l := range [][2]string{{"job", job.JobLink}, {"applies at", job.ExternalLink}, {"screenshot", d.tilde(shot)}} {
			if l[1] != "" {
				lines = append(lines, s.dim.Render(l[0]+": ")+l[1])
			}
		}
		if len(answered) > 0 {
			lines = append(lines, "", fmt.Sprintf("questions answered (%d)", len(answered)))
			for _, q := range answered {
				lines = append(lines, "  "+q)
			}
		}
		return lines
	}
	lines := []string{clip(strings.Join(nonEmpty(job.Title, job.Company, place), " · "), width)}
	facts := []string{string(job.Outcome) + " " + localTime(job.RecordedAt, dayTime)}
	if job.DatePosted != "" {
		facts = append(facts, "posted "+job.DatePosted)
	}
	if job.Reason != "" {
		facts = append(facts, job.Reason)
	}
	why := wrap(strings.Join(facts, " · "), width)
	if len(why) > 2 {
		why = append(why[:1], clip(why[1]+" "+strings.Join(why[2:], " "), width))
	}
	lines = append(lines, why...)
	if failure != "" {
		lines = append(lines, s.fail.Render(clip("error: "+failure, width)))
	}
	if job.JobLink != "" {
		lines = append(lines, s.dim.Render(clip("job: "+job.JobLink, width)))
	}
	if job.ExternalLink != "" {
		lines = append(lines, s.dim.Render(clip("applies at: "+job.ExternalLink, width)))
	}
	if shot != "" {
		lines = append(lines, s.dim.Render(clip("screenshot: "+d.tilde(shot), width)))
	}
	if len(answered) > 0 {
		lines = append(lines, s.dim.Render(clip(fmt.Sprintf("questions answered (%d): %s", len(answered), strings.Join(answered, " · ")), width)))
	}
	return lines
}

// localTime writes a store timestamp as layout, in local time; as it is if
// unreadable.
func localTime(stamp, layout string) string {
	if t, ok := store.ParseTime(stamp); ok {
		return t.In(time.Local).Format(layout)
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
