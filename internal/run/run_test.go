package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/apply"
	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/markets"
	"github.com/luizfreitas27/ajai/internal/pacing"
	"github.com/luizfreitas27/ajai/internal/paths"
	"github.com/luizfreitas27/ajai/internal/store"
)

// fakeCard stands in for a browser.Element; the fake site maps it back to its data.
type fakeCard struct {
	nullElement
	id string
}

// nullElement satisfies browser.Element with no behaviour; the fake site never touches cards.
type nullElement struct{}

func (nullElement) Text() (string, error)                      { return "", nil }
func (nullElement) Attr(string) (string, bool)                 { return "", false }
func (nullElement) Property(string) (string, error)            { return "", nil }
func (nullElement) Click() error                               { return nil }
func (nullElement) ClickAtCentre() error                       { return nil }
func (nullElement) Input(string) error                         { return nil }
func (nullElement) Clear() error                               { return nil }
func (nullElement) Focus() error                               { return nil }
func (nullElement) ScrollIntoView() error                      { return nil }
func (nullElement) Visible() bool                              { return true }
func (nullElement) Element(string) (browser.Element, error)    { return nil, &browser.ErrNotFound{} }
func (nullElement) Elements(string) ([]browser.Element, error) { return nil, nil }
func (nullElement) SelectOption(string) error                  { return nil }
func (nullElement) SetFiles([]string) error                    { return nil }

type fakeJob struct {
	card          linkedin.Card
	about, desc   string
	descOK        bool
	apply         linkedin.ApplyKind
	appliedOnPage bool
}

type fakeSite struct {
	loggedIn   bool
	jobs       map[string]fakeJob
	pages      [][]string // card ids per results page
	page       int
	opened     string
	openedAll  []string // every card opened, in order
	searched   []string
	filters    int
	dailyLimit bool
	loginWaits int
	linkFails  bool // the external application's tab does not open
}

func (f *fakeSite) EnsureLoggedIn(ctx context.Context, _ time.Duration, onWait func()) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !f.loggedIn {
		f.loginWaits++
		onWait()
		f.loggedIn = true
	}
	return nil
}
func (f *fakeSite) Search(ctx context.Context, term string) error {
	f.searched = append(f.searched, term)
	f.page = 0
	return nil
}
func (f *fakeSite) ApplyFilters(context.Context, config.Search, string) error {
	f.filters++
	return nil
}
func (f *fakeSite) WaitForCards(context.Context) error { return nil }
func (f *fakeSite) Cards() ([]browser.Element, error) {
	var out []browser.Element
	for _, id := range f.pages[f.page] {
		out = append(out, &fakeCard{id: id})
	}
	return out, nil
}
func (f *fakeSite) ReadCard(card browser.Element) (linkedin.Card, error) {
	return f.jobs[card.(*fakeCard).id].card, nil
}
func (f *fakeSite) OpenCard(_ context.Context, card browser.Element) error {
	f.opened = card.(*fakeCard).id
	f.openedAll = append(f.openedAll, f.opened)
	return nil
}
func (f *fakeSite) AlreadyAppliedOnPage() bool { return f.jobs[f.opened].appliedOnPage }
func (f *fakeSite) AboutCompany() string       { return f.jobs[f.opened].about }
func (f *fakeSite) Description(context.Context) (string, bool) {
	j := f.jobs[f.opened]
	return j.desc, j.descOK
}
func (f *fakeSite) DatePosted(now time.Time) (time.Time, bool, bool) {
	return now.Add(-24 * time.Hour), false, true
}
func (f *fakeSite) FindApplyButton() (browser.Element, linkedin.ApplyKind, string) {
	kind := f.jobs[f.opened].apply
	if kind == "" {
		kind = linkedin.NoApplyButton
	}
	return nil, kind, "apply button id"
}
func (f *fakeSite) DailyLimitReached() bool { return f.dailyLimit }
func (f *fakeSite) ExternalLink(context.Context, browser.Element) (string, error) {
	if f.linkFails {
		return "", errors.New("no new tab")
	}
	return "https://" + f.opened + ".example/apply", nil
}

// fakeApplier returns a scripted outcome per job id; unknown ids reach Review.
type fakeApplier struct {
	outcomes map[string]apply.Outcome
	errs     map[string]error
	calls    []apply.Options
	jobs     []linkedin.Card
}

func (f *fakeApplier) Apply(_ context.Context, job linkedin.Card, _ browser.Element, opts apply.Options) (apply.Outcome, error) {
	f.calls, f.jobs = append(f.calls, opts), append(f.jobs, job)
	if err := f.errs[job.ID]; err != nil {
		return apply.Outcome{Kind: apply.Failed, Stage: "clicking Submit", Screenshot: job.ID + "-shot.png"}, err
	}
	if o, ok := f.outcomes[job.ID]; ok {
		return o, nil
	}
	if opts.DryRun {
		return apply.Outcome{Kind: apply.DryRun, Answered: []string{"Phone: 555"}}, nil
	}
	return apply.Outcome{Kind: apply.Applied, Resume: "cv.pdf", Answered: []string{"Phone: 555"}}, nil
}
func (f *fakeSite) NextPage(context.Context) (bool, error) {
	if f.page+1 < len(f.pages) {
		f.page++
		return true, nil
	}
	return false, nil
}

func job(id, title, company, desc string) fakeJob {
	return fakeJob{card: linkedin.Card{ID: id, Title: title, Company: company, Location: "Remote", Style: "Remote", Link: linkedin.JobURL + id},
		desc: desc, descOK: true, apply: linkedin.EasyApply}
}

func runner(t *testing.T, site *fakeSite, cfg config.Config) (*Runner, *store.Store, *[]Event) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := newPacer(cfg)
	var events []Event
	r := &Runner{Cfg: cfg, Market: markets.Resolve(cfg), Site: site, Applier: &fakeApplier{}, Store: st, Pacer: p,
		Events:    func(e Event) { events = append(events, e) },
		Now:       func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) },
		StatePath: filepath.Join(t.TempDir(), "run.pid")}
	return r, st, &events
}

func cfg() config.Config {
	c := config.Defaults()
	c.Personals.FirstName, c.Personals.LastName = "A", "B"
	c.Search.Terms = []string{"Go Developer"}
	c.Search.BadWords = []string{"PHP"}
	c.Search.AboutCompanyBadWords = []string{"Crossover"}
	c.Search.CurrentExperience = 5
	c.Pacing.BetweenJobsMin, c.Pacing.BetweenJobsMax = 0, 0
	return c
}

func kinds(events []Event, kind EventKind) []Event {
	var out []Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestADryRunScreensEveryCardAndRecordsWhatItWouldApplyTo(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "Go Dev", "ACME", "Build services in Go. 3 years of experience."),
		"2": job("2", "PHP Dev", "Other", "PHP and Laravel"),
		"3": job("3", "Staff Eng", "Big", "10+ years of experience required"),
		"4": job("4", "Dev", "Crossover", "Fine job"),
		"5": job("5", "Dev", "Applied Inc", "Fine job"),
		"6": job("6", "Dev", "NoButton", "Fine job"),
		"7": job("7", "Dev", "Unreadable", ""),
		"8": job("8", "Dev", "External Co", "Fine job"),
		"9": job("9", "Dev", "Page Applied", "Fine job"),
	}, pages: [][]string{{"1", "2", "3", "4"}, {"5", "6", "7", "8", "9"}}}
	site.jobs["4"] = fakeJob{card: site.jobs["4"].card, about: "Crossover staffing", desc: "Fine job", descOK: true, apply: linkedin.EasyApply}
	site.jobs["5"] = fakeJob{card: linkedin.Card{ID: "5", Title: "Dev", Company: "Applied Inc", AlreadyApplied: true}, desc: "x", descOK: true, apply: linkedin.EasyApply}
	site.jobs["6"] = fakeJob{card: site.jobs["6"].card, desc: "Fine job", descOK: true, apply: linkedin.NoApplyButton}
	site.jobs["7"] = fakeJob{card: site.jobs["7"].card, desc: "", descOK: false, apply: linkedin.EasyApply}
	site.jobs["8"] = fakeJob{card: site.jobs["8"].card, desc: "Fine job", descOK: true, apply: linkedin.ExternalApply}
	site.jobs["9"] = fakeJob{card: site.jobs["9"].card, desc: "Fine job", descOK: true, apply: linkedin.EasyApply, appliedOnPage: true}

	r, st, events := runner(t, site, cfg())
	sum, err := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil {
		t.Fatal(err)
	}
	// 1 would apply; 2,3,4,6,7 skipped by rules; 5 and 9 already applied (counted, not stored); 8 external with easy_apply_only.
	if sum.WouldApply != 1 || sum.Skipped != 8 || sum.External != 0 || sum.Reason != "all search terms visited" || site.filters != 1 || site.loginWaits != 1 {
		t.Errorf("%+v filters=%d logins=%d", sum, site.filters, site.loginWaits)
	}
	if len(kinds(*events, EventWouldApply)) != 1 || kinds(*events, EventWouldApply)[0].Card.ID != "1" || len(kinds(*events, EventLogin)) != 1 {
		t.Errorf("%+v", *events)
	}
	jobs, _ := st.ListJobs(store.JobFilter{})
	reasons := map[string]string{}
	for _, j := range jobs {
		reasons[j.JobID] = j.Reason
	}
	want := map[string]string{"1": "dry run", "2": "bad word in the job description", "3": "required experience is high",
		"4": "blacklisted word in About the company", "6": "no apply button on the job page", "7": "unable to read the job description",
		"8": "external application (easy_apply_only)"}
	for id, reason := range want {
		if reasons[id] != reason {
			t.Errorf("job %s: %q, want %q", id, reasons[id], reason)
		}
	}
	for _, id := range []string{"5", "9"} {
		if _, recorded := reasons[id]; recorded {
			t.Errorf("job %s: LinkedIn's own Applied mark is not a row of ours", id)
		}
	}
	runs, _ := st.ListRuns(1)
	if len(runs) != 1 || !runs[0].DryRun || runs[0].Skipped != 8 || runs[0].Reason != "all search terms visited" || runs[0].EndedAt == "" {
		t.Errorf("%+v", runs)
	}
	if err := st.UpsertAnswer(store.Answer{Label: "Q", Normalised: "q", Kind: "text", Source: "form", State: "pending"}); err != nil {
		t.Fatal(err)
	}
	site.page = 0
	sum, _ = r.Run(context.Background(), Options{DryRun: true, Fast: true})
	runs, _ = st.ListRuns(1)
	if sum.PendingAnswers != 1 || runs[0].PendingAnswers != 1 {
		t.Errorf("the run summary reports the review queue: %+v %+v", sum, runs[0])
	}
	if _, err := ReadState(r.StatePath); !errors.Is(err, ErrNoState) {
		t.Errorf("the state file must be removed when the run ends: %v", err)
	}
}

func TestExternalJobsAreCollectedWhenEasyApplyOnlyIsOff(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"8": job("8", "Dev", "External Co", "Fine job")}, pages: [][]string{{"8"}}}
	site.jobs["8"] = fakeJob{card: site.jobs["8"].card, desc: "Fine job", descOK: true, apply: linkedin.ExternalApply}
	c := cfg()
	c.Search.EasyApplyOnly = false
	r, st, _ := runner(t, site, c)
	sum, err := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil || sum.External != 1 || sum.Skipped != 0 {
		t.Errorf("%+v %v", sum, err)
	}
	if jobs, _ := st.ListJobs(store.JobFilter{Outcomes: []store.Outcome{store.External}}); len(jobs) != 1 || jobs[0].DateApplied != "Pending" || jobs[0].ExternalLink != "" {
		t.Errorf("a dry run does not open the company's site: %+v", jobs)
	}
	// Live: the external tab is opened and its link kept.
	r, st, _ = runner(t, site, c)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if jobs, _ := st.ListJobs(store.JobFilter{Outcomes: []store.Outcome{store.External}}); jobs[0].ExternalLink != "https://8.example/apply" {
		t.Errorf("%+v", jobs)
	}
}

func TestSwitchNumberCountsApplicationsNotCards(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "PHP Dev", "A", "PHP"), "2": job("2", "PHP Dev", "B", "PHP"), "3": job("3", "Go Dev", "C", "Go"),
	}, pages: [][]string{{"1", "2", "3"}}}
	c := cfg()
	c.Search.SwitchNumber = 2
	r, _, _ := runner(t, site, c)
	sum, _ := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if sum.WouldApply != 1 || sum.Skipped != 2 {
		t.Errorf("skipped cards must not use up the per-term budget: %+v", sum)
	}
}

// opened lists the jobs the applier was given, in order.
func (f *fakeApplier) opened() []string {
	var ids []string
	for _, c := range f.jobs {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestAJobListedAgainInTheRunIsNotOpenedAgain(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}, {"1"}}}
	r, st, events := runner(t, site, cfg())
	applier := &fakeApplier{}
	r.Applier = applier
	sum, err := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil || sum.WouldApply != 1 || sum.Skipped != 1 || strings.Join(applier.opened(), ",") != "1" {
		t.Errorf("%+v %v, the applier had %v", sum, err, applier.opened())
	}
	if skips := kinds(*events, EventSkip); len(skips) != 1 || skips[0].Text != "already handled in this run" {
		t.Errorf("%+v", skips)
	}
	if jobs, _ := st.ListJobs(store.JobFilter{}); len(jobs) != 1 || jobs[0].Reason != "dry run" {
		t.Errorf("the pass is counted, not stored: %+v", jobs)
	}
	// Live, an application is settled the same way: neither it nor a repost is opened again.
	site = &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go"), "2": job("2", "Go Dev", "ACME", "Go")},
		pages: [][]string{{"1"}, {"1", "2"}}}
	r, _, _ = runner(t, site, cfg())
	if sum, err := r.Run(context.Background(), Options{}); err != nil || sum.Applied != 1 || sum.Skipped != 2 || strings.Join(site.openedAll, ",") != "1" {
		t.Errorf("%+v %v, opened %v", sum, err, site.openedAll)
	}
}

func TestTheSameRolePostedTwiceIsHandledOnce(t *testing.T) {
	elsewhere := job("3", "Java Specialist", "Inter", "Java") // the same title in another city: another role
	elsewhere.card.Location = "Lisbon"
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "Java Specialist", "Inter", "Java"),
		"2": job("2", "Java  Specialist", "inter", "Java"), // the same role, posted again
		"3": elsewhere,
	}, pages: [][]string{{"1", "2", "3"}}}
	r, _, events := runner(t, site, cfg())
	applier := &fakeApplier{outcomes: map[string]apply.Outcome{"1": {Kind: apply.Unanswered, Blocked: []string{"CPF"}}}}
	r.Applier = applier
	sum, err := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil || sum.WouldApply != 1 || sum.Skipped != 2 || strings.Join(site.openedAll, ",") != "1,3" {
		t.Errorf("%+v %v, opened %v", sum, err, site.openedAll)
	}
	var texts []string
	for _, e := range kinds(*events, EventSkip) {
		texts = append(texts, e.Card.ID+": "+e.Text)
	}
	if !slices.Contains(texts, "2: the same posting as job 1, already handled in this run") {
		t.Errorf("%q", texts)
	}
}

func TestARepostOfAFailedJobIsTried(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "Software Engineer", "Quectel", "Go"), "2": job("2", "Software Engineer", "Quectel", "Go"),
	}, pages: [][]string{{"1", "2"}}}
	r, _, _ := runner(t, site, cfg())
	r.Applier = &fakeApplier{errs: map[string]error{"1": errors.New("element detached")}}
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Failed != 1 || sum.Applied != 1 || strings.Join(site.openedAll, ",") != "1,2" {
		t.Errorf("%+v %v, opened %v", sum, err, site.openedAll)
	}
}

func TestAFailedJobListedAgainIsTriedAgain(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"3": job("3", "Go Dev", "Flaky", "Go")}, pages: [][]string{{"3"}, {"3"}}}
	r, _, _ := runner(t, site, cfg())
	r.Applier = &fakeApplier{errs: map[string]error{"3": errors.New("element detached")}}
	if sum, err := r.Run(context.Background(), Options{}); err != nil || sum.Failed != 2 || sum.Skipped != 0 {
		t.Errorf("%+v %v", sum, err)
	}
	// An external application whose tab did not open failed too.
	external := job("8", "Dev", "External Co", "Fine job")
	external.apply = linkedin.ExternalApply
	site = &fakeSite{jobs: map[string]fakeJob{"8": external}, pages: [][]string{{"8"}, {"8"}}, linkFails: true}
	c := cfg()
	c.Search.EasyApplyOnly = false
	r, _, _ = runner(t, site, c)
	if sum, err := r.Run(context.Background(), Options{}); err != nil || sum.Failed != 2 || sum.Skipped != 0 {
		t.Errorf("%+v %v", sum, err)
	}
}

func TestAClosedOrExternalPostingDoesNotSettleTheRole(t *testing.T) {
	closed, external := job("1", "Go Dev", "ACME", "Go"), job("3", "Rust Dev", "Big", "Rust")
	closed.apply, external.apply = linkedin.NoApplyButton, linkedin.ExternalApply
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": closed, "2": job("2", "Go Dev", "ACME", "Go"), // no apply button any more; the repost takes Easy Apply
		"3": external, "4": job("4", "Rust Dev", "Big", "Rust"), // external; the repost takes Easy Apply
	}, pages: [][]string{{"1", "2", "3", "4"}}}
	r, _, _ := runner(t, site, cfg())
	sum, err := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil || sum.WouldApply != 2 || strings.Join(site.openedAll, ",") != "1,2,3,4" {
		t.Errorf("%+v %v, opened %v", sum, err, site.openedAll)
	}
}

func TestARunStartingEndsTheOneThatDiedBeforeIt(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, st, _ := runner(t, site, cfg())
	killed, _ := st.StartRun(context.Background(), "brasil", true)
	if err := WriteState(r.StatePath, State{PID: deadPID(t), RunID: killed, Applied: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), Options{DryRun: true, Fast: true}); err != nil {
		t.Fatal(err)
	}
	if old, _ := st.GetRun(killed); old.Reason != StaleReason || old.EndedAt == "" || old.Applied != 3 {
		t.Errorf("the killed run: %+v", old)
	}
}

func TestTheLiveStateFileShowsProgressWhileRunning(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, _, _ := runner(t, site, cfg())
	var seen State
	r.Events = func(e Event) {
		if e.Kind == EventWouldApply {
			// The state is written after the card is handled; peek at the term already recorded.
			seen, _ = ReadState(r.StatePath)
		}
	}
	if _, err := r.Run(context.Background(), Options{DryRun: true, Fast: true}); err != nil {
		t.Fatal(err)
	}
	if seen.Term != "Go Developer" || !seen.DryRun || seen.RunID == 0 || seen.PID == 0 {
		t.Errorf("%+v", seen)
	}
}

func TestALiveRunAppliesRecordsFailuresAndUnansweredQuestions(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "Go Dev", "ACME", "Go"), "2": job("2", "Go Dev", "Boom", "Go"), "3": job("3", "Go Dev", "Quiz", "Go"),
	}, pages: [][]string{{"1", "2", "3"}}}
	r, st, events := runner(t, site, cfg())
	r.ResumePath = "/tmp/cv.pdf"
	applier := &fakeApplier{errs: map[string]error{"2": errors.New("element detached")},
		outcomes: map[string]apply.Outcome{"3": {Kind: apply.Unanswered, Blocked: []string{"Favourite colour [Red, Blue]"}, Queued: 1, Captured: 1}}}
	r.Applier = applier
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 1 || sum.Failed != 1 || sum.Skipped != 1 || sum.WouldApply != 0 {
		t.Errorf("%+v %v", sum, err)
	}
	if len(applier.calls) != 3 || applier.calls[0].DryRun || applier.calls[0].ResumePath != "/tmp/cv.pdf" {
		t.Errorf("%+v", applier.calls)
	}
	byID := map[string]store.Job{}
	jobs, _ := st.ListJobs(store.JobFilter{})
	for _, j := range jobs {
		byID[j.JobID] = j
	}
	if j := byID["1"]; j.Outcome != store.Applied || j.Resume != "cv.pdf" || j.DateApplied == "" || j.Questions != "Phone: 555" {
		t.Errorf("%+v", j)
	}
	if j := byID["2"]; j.Outcome != store.Failed || j.Reason != "clicking Submit" || !strings.Contains(j.StackTrace, "detached") || j.Screenshot != "2-shot.png" || j.Questions != "" {
		t.Errorf("answers are kept only for applied jobs: %+v", j)
	}
	if j := byID["3"]; j.Outcome != store.Skipped || !strings.Contains(j.Reason, "Favourite colour") {
		t.Errorf("%+v", j)
	}
	if len(kinds(*events, EventApplied)) != 1 || len(kinds(*events, EventFailed)) != 1 {
		t.Errorf("%+v", *events)
	}
	if sum.Captured != 1 || !strings.Contains(kinds(*events, EventSkip)[0].Text, "review queue") {
		t.Errorf("%+v %+v", sum, kinds(*events, EventSkip))
	}
	if _, err := r.Run(context.Background(), Options{Fast: true}); err == nil {
		t.Error("--fast without --dry-run must be refused")
	}
}

func TestPacingCapsMaxAndTheDailyLimitEndTheRunCleanly(t *testing.T) {
	jobs := map[string]fakeJob{}
	var ids []string
	for _, id := range []string{"1", "2", "3", "4"} {
		jobs[id] = job(id, "Go Dev", "ACME "+id, "Go")
		ids = append(ids, id)
	}
	site := &fakeSite{jobs: jobs, pages: [][]string{ids}}
	c := cfg()
	c.Pacing.MaxPerRun = 2
	r, _, _ := runner(t, site, c)
	sum, err := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil || sum.WouldApply != 2 || sum.Reason != "pacing limit reached: per run" {
		t.Errorf("%+v %v", sum, err)
	}
	site = &fakeSite{jobs: jobs, pages: [][]string{ids}}
	r, _, _ = runner(t, site, cfg())
	sum, _ = r.Run(context.Background(), Options{DryRun: true, Fast: true, Max: 1})
	if sum.WouldApply != 1 || sum.Reason != "--max reached" {
		t.Errorf("%+v", sum)
	}
	site = &fakeSite{jobs: map[string]fakeJob{"9": {card: linkedin.Card{ID: "9", Title: "x"}, desc: "x", descOK: true}}, pages: [][]string{{"9"}}, dailyLimit: true}
	r, _, _ = runner(t, site, cfg())
	sum, err = r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil || sum.Reason != "LinkedIn's daily Easy Apply limit reached" {
		t.Errorf("%+v %v", sum, err)
	}
}

func TestAnApplicationSubmittedJustBeforeCtrlCIsStillRecorded(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, st, _ := runner(t, site, cfg())
	ctx, cancel := context.WithCancel(context.Background())
	r.Applier = applierFunc(func() (apply.Outcome, error) {
		cancel() // the user hits Ctrl-C the instant Submit was clicked
		return apply.Outcome{Kind: apply.Applied, Resume: apply.PreviousResume}, nil
	})
	sum, err := r.Run(ctx, Options{})
	if err != nil || sum.Applied != 1 || sum.Reason != "stopped by user" {
		t.Errorf("%+v %v", sum, err)
	}
	if jobs, _ := st.ListJobs(store.JobFilter{Outcomes: []store.Outcome{store.Applied}}); len(jobs) != 1 {
		t.Error("the submitted application must be in the store")
	}
}

type applierFunc func() (apply.Outcome, error)

func (f applierFunc) Apply(context.Context, linkedin.Card, browser.Element, apply.Options) (apply.Outcome, error) {
	return f()
}

func TestResolveResumePrefersTheMarketThenTheGeneralOne(t *testing.T) {
	home := t.TempDir()
	p := paths.Resolve("", func(k string) string {
		if k == "AJAI_HOME" {
			return home
		}
		return ""
	})
	if err := p.EnsureDataDirs(); err != nil {
		t.Fatal(err)
	}
	write := func(name string) { _ = os.WriteFile(filepath.Join(p.ResumeDir, name), []byte("%PDF"), 0o600) }
	m := markets.Resolved{Resume: "cv-pt.pdf"}
	if path, note := ResolveResume(p, m, "resume.pdf"); path != "" || !strings.Contains(note, "cv-pt.pdf not found") || !strings.Contains(note, "previously uploaded") {
		t.Errorf("%q %q", path, note)
	}
	write("resume.pdf")
	if path, note := ResolveResume(p, m, "resume.pdf"); path != filepath.Join(p.ResumeDir, "resume.pdf") || note != "" {
		t.Errorf("falls back to the general resume: %q %q", path, note)
	}
	write("cv-pt.pdf")
	if path, _ := ResolveResume(p, m, "resume.pdf"); path != filepath.Join(p.ResumeDir, "cv-pt.pdf") {
		t.Errorf("the market's resume wins: %q", path)
	}
}

func TestCtrlCStopsCleanlyAndRecordsTheRun(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, st, _ := runner(t, site, cfg())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sum, err := r.Run(ctx, Options{DryRun: true})
	if err != nil || sum.Reason != "stopped by user" {
		t.Errorf("%+v %v", sum, err)
	}
	runs, _ := st.ListRuns(1)
	if len(runs) != 1 || runs[0].Reason != "stopped by user" {
		t.Errorf("%+v", runs)
	}
}

func TestTermAndLocationOverridesAndOurOwnHistory(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, st, _ := runner(t, site, cfg())
	if err := st.UpsertJob(store.Job{JobID: "1", Outcome: store.Applied}); err != nil {
		t.Fatal(err)
	}
	terms := []string{"QA", "SRE"}
	r.Cfg.Search.RandomizeOrder = true
	sum, _ := r.Run(context.Background(), Options{DryRun: true, Terms: terms})
	if sum.WouldApply != 0 || sum.Skipped != 2 || len(site.searched) != 2 { // the same card under both terms
		t.Errorf("%+v %v", sum, site.searched)
	}
	if terms[0] != "QA" || terms[1] != "SRE" {
		t.Error("the caller's slice must not be shuffled in place")
	}
}

func newPacer(c config.Config) *pacing.Pacer { return pacing.New(c.Pacing, true) }

func TestARunThatCannotBeRecordedStillEndsWithASummary(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{}, pages: [][]string{{}}}
	r, st, events := runner(t, site, cfg())
	_ = st.Close() // StartRun fails
	sum, err := r.Run(context.Background(), Options{DryRun: true})
	if err == nil || sum.End != EndError {
		t.Fatalf("%+v %v", sum, err)
	}
	if ends := kinds(*events, EventRunEnd); len(ends) != 1 || ends[0].Summary == nil || ends[0].Summary.End != EndError {
		t.Errorf("--json and the terminal need the run-end event: %+v", *events)
	}
}
