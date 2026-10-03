package run

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/config"
)

func TestTheFilterCycleChangesFiltersBetweenRunsLikeThePythonBot(t *testing.T) {
	cases := []struct {
		name              string
		sort, date        string
		alternate, narrow bool
		want              [][2]string // sort, date for runs 1..n
	}{
		// The shipped defaults: what the Python bot did at run time.
		{"python defaults", "", "Past week", true, true, [][2]string{
			{"", "Past week"},
			{"Most relevant", "Past 24 hours"}, {"Most recent", "Past 24 hours"},
			{"Most relevant", "Past 24 hours"}, {"Most recent", "Past 24 hours"},
		}},
		{"most relevant configured", "Most relevant", "Past month", true, true, [][2]string{
			{"Most relevant", "Past month"},
			{"Most recent", "Past 24 hours"}, {"Most relevant", "Past 24 hours"},
		}},
		{"most recent configured", "Most recent", "Any time", true, false, [][2]string{
			{"Most recent", "Any time"}, {"Most relevant", "Any time"}, {"Most recent", "Any time"},
		}},
		{"only the date, from no date filter", "Most recent", "", false, true, [][2]string{
			{"Most recent", ""}, {"Most recent", "Past 24 hours"}, {"Most recent", "Past 24 hours"},
		}},
		{"neither", "Most recent", "Past week", false, false, [][2]string{
			{"Most recent", "Past week"}, {"Most recent", "Past week"},
		}},
	}
	for _, tc := range cases {
		base := config.Defaults().Search
		base.SortBy, base.DatePosted = tc.sort, tc.date
		c := NewFilterCycle(base, tc.alternate, tc.narrow)
		for i, want := range tc.want {
			peek := c.Peek()
			got := c.Next()
			if got.SortBy != want[0] || got.DatePosted != want[1] || peek.SortBy != got.SortBy || peek.DatePosted != got.DatePosted {
				t.Errorf("%s run %d: got (%q, %q) peek (%q, %q), want %v", tc.name, i+1, got.SortBy, got.DatePosted, peek.SortBy, peek.DatePosted, want)
			}
		}
	}
}

func TestOnlyACompletePassOrThePerRunCapStartsAnotherNonStopRun(t *testing.T) {
	for end, want := range map[End]bool{EndAllTerms: true, EndPerRunCap: true, EndHourlyCap: false, EndMax: false,
		EndDailyLimit: false, EndStopped: false, EndTimedOut: false, EndError: false} {
		if end.Continues() != want {
			t.Errorf("%s continues = %v", end, !want)
		}
	}
}

func nonStopRunner(t *testing.T, site *fakeSite) (*Runner, *[]time.Duration) {
	t.Helper()
	c := cfg()
	c.Settings.RunNonStop, c.Settings.AlternateSortBy, c.Settings.CycleDatePosted = true, false, true
	c.Search.DatePosted = "Past month"
	r, _, _ := runner(t, site, c)
	var waits []time.Duration
	r.Sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	return r, &waits
}

// recordingSite remembers the date filter each search ran with.
type recordingSite struct {
	*fakeSite
	dates []string
}

func (s *recordingSite) ApplyFilters(ctx context.Context, f config.Search, loc string) error {
	s.dates = append(s.dates, f.DatePosted)
	return s.fakeSite.ApplyFilters(ctx, f, loc)
}

func TestANonStopSessionRunsAgainAfterTheWaitAndStopsAtTheDailyLimit(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	rec := &recordingSite{fakeSite: site}
	r, waits := nonStopRunner(t, site)
	r.Site = rec
	runs := 0
	r.Events = func(e Event) {
		if e.Kind == EventRunEnd {
			runs++
			if runs == 3 {
				site.dailyLimit = true // the third run's successor finds LinkedIn's limit
				site.jobs["1"] = fakeJob{card: site.jobs["1"].card, desc: "Go", descOK: true, apply: ""}
			}
		}
	}
	sums, err := r.Session(context.Background(), Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 4 || sums[3].End != EndDailyLimit || len(*waits) != 3 || (*waits)[0] != defaultNonStopWait {
		t.Fatalf("%d runs, waits %v, last %+v", len(sums), *waits, sums[len(sums)-1])
	}
	if got := rec.dates; len(got) != 4 || got[0] != "Past month" || got[1] != "Past 24 hours" || got[2] != "Past 24 hours" || got[3] != "Past 24 hours" {
		t.Errorf("date filter per run: %v", got)
	}
	if r.Cfg.Search.DatePosted != "Past month" {
		t.Error("the configuration must be restored after the session")
	}
}

func TestCtrlCDuringTheWaitEndsTheSessionAndAnErrorOrHourlyCapNeverRetries(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, _ := nonStopRunner(t, site)
	ctx, cancel := context.WithCancel(context.Background())
	var waiting State
	r.Sleep = func(context.Context, time.Duration) error {
		waiting, _ = ReadState(r.StatePath) // what ajai status sees during the wait
		cancel()
		return context.Canceled
	}
	sums, err := r.Session(ctx, Options{DryRun: true})
	if err != nil || len(sums) != 1 {
		t.Errorf("%d runs %v", len(sums), err)
	}
	if waiting.NextRun.IsZero() || waiting.PID == 0 {
		t.Errorf("the wait must be visible to ajai status: %+v", waiting)
	}
	if _, err := ReadState(r.StatePath); !errors.Is(err, ErrNoState) {
		t.Error("the waiting state must be removed when the session ends")
	}

	site2 := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go"), "2": job("2", "Go Dev", "B", "Go")}, pages: [][]string{{"1", "2"}}}
	r2, waits := nonStopRunner(t, site2)
	r2.Cfg.Pacing.MaxPerHour = 1
	r2.Pacer = newPacer(r2.Cfg)
	sums, _ = r2.Session(context.Background(), Options{DryRun: true})
	if len(sums) != 1 || sums[0].End != EndHourlyCap || len(*waits) != 0 {
		t.Errorf("the hourly cap ends the session: %d runs, %+v", len(sums), sums)
	}

	failing := &failingSite{fakeSite{jobs: map[string]fakeJob{}, pages: [][]string{{}}}}
	r3, waits3 := nonStopRunner(t, &failing.fakeSite)
	r3.Site = failing
	if sums, err := r3.Session(context.Background(), Options{DryRun: true}); err == nil || len(sums) != 1 || len(*waits3) != 0 {
		t.Errorf("an error ends the session: %d runs %v", len(sums), err)
	}
}

func TestASingleRunWithoutNonStopIsOneRun(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, _, _ := runner(t, site, cfg())
	sums, err := r.Session(context.Background(), Options{DryRun: true})
	if err != nil || len(sums) != 1 || sums[0].End != EndAllTerms {
		t.Errorf("%+v %v", sums, err)
	}
}

func TestThePauseAfterFiltersRunsPerTermUntilTheUserTypesOff(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	r, _, _ := runner(t, site, cfg())
	var paused []string
	r.AfterFilters = func(_ context.Context, term string) bool {
		paused = append(paused, term)
		return term != "B" // the user types "off" at the second pause
	}
	if _, err := r.Run(context.Background(), Options{DryRun: true, Terms: []string{"A", "B", "C"}}); err != nil {
		t.Fatal(err)
	}
	if len(paused) != 2 || paused[0] != "A" || paused[1] != "B" {
		t.Errorf("%v", paused)
	}
}
