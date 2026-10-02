package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/store"
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

// selectJob moves the selection to the job titled title.
func (h *harness) selectJob(title string) {
	h.t.Helper()
	h.press("g")
	for range 200 {
		for _, l := range strings.Split(h.screen(), "\n") {
			if strings.HasPrefix(l, "> ") && strings.Contains(l, title) {
				return
			}
		}
		h.press("down")
	}
	h.t.Fatalf("no job %q:\n%s", title, h.screen())
}

func (h *harness) someJobs() {
	h.recordJob(1, store.Job{JobID: "1", Outcome: store.Applied, Title: "Go Developer", Company: "Acme", WorkLocation: "Recife",
		WorkStyle: "Remote", DatePosted: "2026-10-03", JobLink: "https://www.linkedin.com/jobs/view/1/", Questions: "Years of Go? 3"})
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
	h.shows("4 Jobs", "Jobs  all", "applied 1 · external 1 · failed 0 · skipped 1",
		"05 Oct 08:03  skipped   Senior Java", "Initech", "Go Developer")
	h.selectJob("Go Developer")
	h.shows("Go Developer · Acme · Recife (Remote)", "applied Mon 05 Oct 08:01 · posted 2026-10-03", "job: https://www.linkedin.com/jobs/view/1/",
		"questions: Years of Go? 3")
	h.golden("jobs")
	h.press("o")
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
	h.shows("Jobs  applied", "Go Developer")
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
}

func TestTheHistoryIsReadPageByPageAndKeepsUp(t *testing.T) {
	h := newHarness(t)
	for i := range 150 {
		h.recordJob(i, store.Job{JobID: fmt.Sprint(i), Outcome: store.Skipped, Title: fmt.Sprintf("Job %03d", i)})
	}
	h.open(80, 24)
	h.jobsTab()
	h.golden("jobs-smallest")
	h.press("G") // the end of the first page: the next is read
	h.press("G")
	h.shows("> 05 Oct 08:00  skipped   Job 000")

	// A run records a job while the tab is shown.
	h.recordJob(200, store.Job{JobID: "new", Outcome: store.Applied, Title: "Just applied"})
	h.refresh()
	h.press("g")
	h.shows("> 05 Oct 11:20  applied   Just applied")
}
