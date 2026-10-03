package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/store"
)

// recordJob stores a job recorded minutes after 08:00 on h's day.
func (h *harness) recordJob(minutes int, j store.Job) {
	h.t.Helper()
	day := time.Date(h.now.Year(), h.now.Month(), h.now.Day(), 8, 0, 0, 0, time.Local)
	j.RecordedAt = store.Timestamp(day.Add(time.Duration(minutes) * time.Minute))
	if err := h.st.UpsertJob(j); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) jobsTab() { h.t.Helper(); h.press(tabKey(tabJobs)) }

// refreshJobs lets enough refreshes go by for the Jobs tab to read again.
func (h *harness) refreshJobs() {
	for range jobsEvery {
		h.refresh()
	}
}

// selectJob moves the selection to the job titled title.
func (h *harness) selectJob(title string) {
	h.t.Helper()
	h.selectRow(fmt.Sprintf("job %q", title), []string{"g"}, 200, func(row string) bool { return strings.Contains(row, title) })
}

func (h *harness) someJobs() {
	h.recordJob(1, store.Job{JobID: "1", Outcome: store.Applied, Title: "Go Developer", Company: "Acme", WorkLocation: "Recife",
		WorkStyle: "Remote", DatePosted: "2026-10-03", JobLink: "https://www.linkedin.com/jobs/view/1/", Questions: "Years of Go? 3; Relocate? Yes"})
	h.recordJob(2, store.Job{JobID: "2", Outcome: store.External, Title: "Backend Engineer", Company: "Initech", WorkLocation: "Lisboa",
		JobLink: "https://www.linkedin.com/jobs/view/2/", ExternalLink: "https://initech.example/apply"})
	h.recordJob(3, store.Job{JobID: "3", Outcome: store.Skipped, Title: "Senior Java", Company: "Umbrella", WorkLocation: "Remote",
		Reason: "the description asks for 10 years"})
}

func TestTheJobsTabListsTheHistoryWithCountsAndDetails(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(100, 30)
	h.jobsTab()
	h.shows("4 Jobs", "Jobs  [all]", "applied 1 · external 1 · failed 0 · skipped 1",
		"05 Oct 08:03  skipped   Senior Java", "Initech", "Go Developer")
	h.selectJob("Go Developer")
	h.shows("Go Developer · Acme · Recife (Remote)", "applied Mon 05 Oct 08:01 · posted 2026-10-03", "job: https://www.linkedin.com/jobs/view/1/",
		"questions answered (2): Years of Go? 3 · Relocate? Yes")
	h.golden("jobs")
	h.press("o")
	h.shows("Opened https://www.linkedin.com/jobs/view/1/")
	if !strings.Contains(h.logs.String(), `"msg":"job opened from the TUI","job":"1"`) {
		t.Errorf("not logged: %s", h.logs)
	}
	h.selectJob("Backend Engineer")
	h.shows("applies at: https://initech.example/apply")
	h.press("o")
	h.selectJob("Senior Java")
	h.shows("the description asks for 10 years")
	h.press("o")
	h.shows("This job has no link.")
	if want := []string{"https://www.linkedin.com/jobs/view/1/", "https://initech.example/apply"}; !reflect.DeepEqual(h.opened, want) {
		t.Errorf("opened %q, want %q (an external job opens where it applies)", h.opened, want)
	}
}

func TestJobsAreFilteredByOutcomeAndSearched(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(100, 30)
	h.jobsTab()
	h.press("tab")
	h.shows("[applied]", "Go Developer")
	if strings.Contains(h.screen(), "Initech") {
		t.Errorf("only applied jobs:\n%s", h.screen())
	}
	h.press("tab", "tab", "tab", "tab") // back to every job
	h.press("/", "q1umbr")              // typed, not keys
	h.shows("search: q1umbr", "enter searches · esc cancels")
	h.press("ctrl+u", "umbr", "enter")
	h.shows(`matching "umbr" (c clears)`, "Senior Java")
	if strings.Contains(h.screen(), "Go Developer") {
		t.Errorf("only the match:\n%s", h.screen())
	}
	h.press("c")
	h.shows("Go Developer", "Senior Java")

	// A read made for a filter no longer shown is dropped when it comes back.
	h.press("tab")
	h.send(jobsLoadedMsg{filter: 0, read: readFirst, jobs: []store.Job{{JobID: "2", Outcome: store.External, Title: "Backend Engineer", Company: "Initech"}}})
	if strings.Contains(h.screen(), "Initech") {
		t.Errorf("an old read replaced the applied jobs:\n%s", h.screen())
	}
}

func TestAFailedJobShowsAllItsDetailsInTheSmallestTerminal(t *testing.T) {
	h := newHarness(t)
	for i := range 30 { // a full list: the details take room from it
		h.recordJob(-60+i, store.Job{JobID: fmt.Sprint(100 + i), Outcome: store.Skipped, Title: fmt.Sprintf("Job %03d", i)})
	}
	h.recordJob(1, store.Job{JobID: "7", Outcome: store.Failed, Title: "Platform Engineer", Company: "Hooli", WorkLocation: "Porto Alegre",
		WorkStyle: "Hybrid", DatePosted: "2026-10-01", Reason: "next: the form kept asking for the same page after every answer was given, and the page it asked for had no question left to answer",
		StackTrace: "next: element not found: button[aria-label='Continue']\ngoroutine 7 [running]", Screenshot: "7-failed-20261005-080100.png",
		JobLink: "https://www.linkedin.com/jobs/view/7/", ExternalLink: "https://hooli.example/apply", Questions: "Years of Go? 3; Relocate? Yes; Notice period? 30 days"})
	h.open(80, 24)
	h.jobsTab()
	h.shows("Platform Engineer · Hooli · Porto Alegre (Hybrid)", "failed Mon 05 Oct 08:01 · posted 2026-10-01 · next: the form",
		"error: next: element not found: button[aria-label='Continue']", "job: https://www.linkedin.com/jobs/view/7/",
		"applies at: https://hooli.example/apply", "screenshot: ~/screenshots/7-failed-20261005-080100.png", "questions answered (3): Years of Go? 3")
	if strings.Contains(h.screen(), "goroutine") {
		t.Errorf("only the error's first line:\n%s", h.screen())
	}
	h.golden("jobs-failed")
}

func TestOnlyWebLinksAreOpened(t *testing.T) {
	h := newHarness(t)
	h.recordJob(1, store.Job{JobID: "1", Outcome: store.External, Title: "Odd", JobLink: "https://www.linkedin.com/jobs/view/1/",
		ExternalLink: "file:///etc/passwd"})
	h.open(100, 30)
	h.jobsTab()
	h.press("o")
	h.shows("Not opened: file:///etc/passwd is not a web link.")
	if len(h.opened) > 0 {
		t.Errorf("opened %q", h.opened)
	}

	h.recordJob(2, store.Job{JobID: "2", Outcome: store.Applied, Title: "Fine", JobLink: "https://www.linkedin.com/jobs/view/2/"})
	h.refreshJobs()
	h.noOpen = errors.New("xdg-open: not found")
	h.selectJob("Fine")
	h.press("o")
	h.shows("Could not open https://www.linkedin.com/jobs/view/2/: xdg-open: not found")
	if !strings.Contains(h.logs.String(), `"msg":"could not open a job from the TUI","job":"2"`) {
		t.Errorf("not logged: %s", h.logs)
	}
}

func TestAFailedReadKeepsTheJobsShown(t *testing.T) {
	h := newHarness(t)
	h.someJobs()
	h.open(100, 30)
	h.jobsTab()
	_ = h.st.Close()
	h.refreshJobs()
	h.shows("Could not read the job history:", "Go Developer", "Senior Java")
}

func TestTheHistoryIsReadPageByPageAndKeepsUp(t *testing.T) {
	h := newHarness(t)
	for i := range 150 {
		h.recordJob(i, store.Job{JobID: fmt.Sprint(i), Outcome: store.Skipped, Title: fmt.Sprintf("Job %03d", i)})
	}
	h.open(80, 24)
	h.jobsTab()
	h.golden("jobs-smallest")

	// A run records a job before the next page is read: the page starts a row
	// later, and the row both pages have is listed once.
	h.recordJob(200, store.Job{JobID: "new", Outcome: store.Applied, Title: "Just applied"})
	h.press("G") // the end of the first page: the next is read
	h.selectJob("Job 050")
	h.press("down")
	h.shows(selected + "05 Oct 08:49  skipped   Job 049")
	h.press("G")
	h.shows(selected + "05 Oct 08:00  skipped   Job 000")

	// Read again, the list keeps every job shown, and the selection its job.
	h.recordJob(201, store.Job{JobID: "newer", Outcome: store.Applied, Title: "Applied again"})
	h.refreshJobs()
	h.shows(selected + "05 Oct 08:00  skipped   Job 000")
	h.selectJob("Job 120")
	h.recordJob(202, store.Job{JobID: "newest", Outcome: store.Applied, Title: "Applied once more"})
	h.refreshJobs()
	h.shows(selected + "05 Oct 10:00  skipped   Job 120")
	h.press("g")
	h.shows(selected+"05 Oct 11:22  applied   Applied once more", "Applied again", "Just applied")
}
