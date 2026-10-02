package run

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/store"
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
	card        linkedin.Card
	about, desc string
	descOK      bool
	applyButton bool
}

type fakeSite struct {
	loggedIn   bool
	jobs       map[string]fakeJob
	pages      [][]string // card ids per results page
	page       int
	opened     string
	searched   []string
	filters    int
	dailyLimit bool
	loginWaits int
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
	return nil
}
func (f *fakeSite) AboutCompany() string { return f.jobs[f.opened].about }
func (f *fakeSite) Description(context.Context) (string, bool) {
	j := f.jobs[f.opened]
	return j.desc, j.descOK
}
func (f *fakeSite) DatePosted(now time.Time) (time.Time, bool, bool) {
	return now.Add(-24 * time.Hour), false, true
}
func (f *fakeSite) FindApplyButton() (browser.Element, string, bool) {
	return nil, "apply button id", f.jobs[f.opened].applyButton
}
func (f *fakeSite) DailyLimitReached() bool { return f.dailyLimit }
func (f *fakeSite) NextPage(context.Context) (bool, error) {
	if f.page+1 < len(f.pages) {
		f.page++
		return true, nil
	}
	return false, nil
}

func job(id, title, company, desc string) fakeJob {
	return fakeJob{card: linkedin.Card{ID: id, Title: title, Company: company, Location: "Remote", Style: "Remote", Link: linkedin.JobURL + id},
		desc: desc, descOK: true, applyButton: true}
}

func runner(t *testing.T, site *fakeSite, cfg config.Config) (*Runner, *store.Store, *[]Event) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "aja.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := pacing.New(cfg.Pacing, true)
	var events []Event
	r := &Runner{Cfg: cfg, Market: markets.Resolve(cfg), Site: site, Store: st, Pacer: p,
		Events: func(e Event) { events = append(events, e) },
		Now:    func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }}
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

func kinds(events []Event, kind string) []Event {
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
		"6": job("6", "Dev", "External", "Fine job"),
		"7": job("7", "Dev", "Unreadable", ""),
	}, pages: [][]string{{"1", "2", "3", "4"}, {"5", "6", "7"}}}
	site.jobs["4"] = fakeJob{card: site.jobs["4"].card, about: "Crossover staffing", desc: "Fine job", descOK: true, applyButton: true}
	site.jobs["5"] = fakeJob{card: linkedin.Card{ID: "5", Title: "Dev", Company: "Applied Inc", AlreadyApplied: true}, desc: "x", descOK: true, applyButton: true}
	site.jobs["6"] = fakeJob{card: site.jobs["6"].card, desc: "Fine job", descOK: true, applyButton: false}
	site.jobs["7"] = fakeJob{card: site.jobs["7"].card, desc: "", descOK: false, applyButton: true}

	r, st, events := runner(t, site, cfg())
	sum, err := r.Run(context.Background(), Options{DryRun: true, Fast: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum.WouldApply != 1 || sum.Reason != "all search terms visited" || site.filters != 1 || site.loginWaits != 1 {
		t.Errorf("%+v filters=%d logins=%d", sum, site.filters, site.loginWaits)
	}
	if len(kinds(*events, "would-apply")) != 1 || kinds(*events, "would-apply")[0].Card.ID != "1" || len(kinds(*events, "login")) != 1 {
		t.Errorf("%+v", *events)
	}
	jobs, _ := st.ListJobs(store.JobFilter{})
	reasons := map[string]string{}
	for _, j := range jobs {
		reasons[j.JobID] = j.Reason
	}
	want := map[string]string{"1": "dry run (would apply)", "2": "bad word in the job description", "3": "required experience is high",
		"4": "blacklisted word in About the company", "6": "no apply button on the job page", "7": "unable to read the job description"}
	for id, reason := range want {
		if reasons[id] != reason {
			t.Errorf("job %s: %q, want %q", id, reasons[id], reason)
		}
	}
	if _, recorded := reasons["5"]; recorded {
		t.Error("LinkedIn's own Applied badge is not a row of ours")
	}
	runs, _ := st.ListRuns(1)
	if len(runs) != 1 || !runs[0].DryRun || runs[0].Skipped != 6 || runs[0].Reason != "all search terms visited" || runs[0].EndedAt == "" {
		t.Errorf("%+v", runs)
	}
}

func TestWithoutDryRunTheLoopRefusesToApplyUntilF15(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, _, _ := runner(t, site, cfg())
	if _, err := r.Run(context.Background(), Options{}); !errors.Is(err, ErrApplyNotImplemented) {
		t.Errorf("%v", err)
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
	sum, _ := r.Run(context.Background(), Options{DryRun: true, Terms: []string{"QA", "SRE"}})
	if sum.WouldApply != 0 || len(site.searched) != 2 || site.searched[0] != "QA" {
		t.Errorf("%+v %v", sum, site.searched)
	}
}
