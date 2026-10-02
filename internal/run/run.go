// Package run is the run loop: for every search term, search, filter, walk the
// job cards, screen each job and decide whether to apply. It works against a
// Site interface (the LinkedIn session) so the loop is tested with a fake, and
// records every job and the run itself in the store.
package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/screening"
	"github.com/luizfreitas27/aja/internal/store"
)

// Site is the slice of the LinkedIn session the loop needs.
type Site interface {
	EnsureLoggedIn(ctx context.Context, timeout time.Duration, onWait func()) error
	Search(ctx context.Context, term string) error
	ApplyFilters(ctx context.Context, f config.Search, location string) error
	WaitForCards(ctx context.Context) error
	Cards() ([]browser.Element, error)
	ReadCard(card browser.Element) (linkedin.Card, error)
	OpenCard(ctx context.Context, card browser.Element) error
	AboutCompany() string
	Description(ctx context.Context) (string, bool)
	DatePosted(now time.Time) (posted time.Time, reposted, ok bool)
	FindApplyButton() (browser.Element, string, bool)
	DailyLimitReached() bool
	NextPage(ctx context.Context) (bool, error)
}

// Options for one run.
type Options struct {
	DryRun     bool
	Unattended bool
	Fast       bool
	Max        int      // stop after this many applications (0: the pacing cap)
	Terms      []string // override the market's search terms
	Location   string   // override the market's search location
}

// Event is something the user should see as it happens.
type Event struct {
	Kind string // "login", "term", "job", "skip", "would-apply", "limit", "warn", "page"
	Text string
	Card *linkedin.Card
}

// Summary is the run summary.
type Summary struct {
	RunID      int64
	Market     string
	DryRun     bool
	Applied    int
	External   int
	Failed     int
	Skipped    int
	WouldApply int
	Reason     string
}

// ErrApplyNotImplemented is returned while the Easy Apply loop (F1-5) is not here yet.
var ErrApplyNotImplemented = errors.New("applying is not implemented yet; run with --dry-run")

// Runner holds the wiring for one run.
type Runner struct {
	Cfg    config.Config
	Market markets.Resolved
	Site   Site
	Store  *store.Store
	Pacer  *pacing.Pacer
	Logger *slog.Logger
	Events func(Event)
	Now    func() time.Time
	// LoginTimeout is how long to wait for a manual login (default 10 minutes).
	LoginTimeout time.Duration
}

func (r *Runner) emit(kind, text string, card *linkedin.Card) {
	if r.Events != nil {
		r.Events(Event{Kind: kind, Text: text, Card: card})
	}
}

// Run executes one run and returns its summary. A cancelled context ends the
// run cleanly with reason "stopped by user".
func (r *Runner) Run(ctx context.Context, opts Options) (Summary, error) {
	if r.Logger == nil {
		r.Logger = slog.New(slog.DiscardHandler)
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.LoginTimeout == 0 {
		r.LoginTimeout = 10 * time.Minute
	}
	if opts.Fast && !opts.DryRun {
		return Summary{}, errors.New("--fast is allowed only together with --dry-run")
	}
	sum := Summary{Market: string(r.Market.Name), DryRun: opts.DryRun}
	id, err := r.Store.StartRun(context.Background(), sum.Market, opts.DryRun) // the record must exist even when ctx is already cancelled
	if err != nil {
		return sum, err
	}
	sum.RunID = id
	defer func() {
		_ = r.Store.FinishRun(context.Background(), store.Run{ID: id, Applied: sum.Applied, External: sum.External,
			Failed: sum.Failed, Skipped: sum.Skipped, Reason: sum.Reason})
	}()

	err = r.Site.EnsureLoggedIn(ctx, r.LoginTimeout, func() {
		r.emit("login", "Sign in to LinkedIn in the Chrome window; aja continues when the feed appears.", nil)
	})
	if err != nil {
		if ctx.Err() != nil {
			return r.stop(&sum, ctx.Err())
		}
		sum.Reason = "login: " + err.Error()
		return sum, err
	}

	terms := opts.Terms
	if len(terms) == 0 {
		terms = r.Market.Terms
	}
	if r.Cfg.Search.RandomizeOrder {
		rand.Shuffle(len(terms), func(i, j int) { terms[i], terms[j] = terms[j], terms[i] })
	}
	location := opts.Location
	if location == "" {
		location = r.Market.Location
	}

	applied, err := r.appliedIDs()
	if err != nil {
		return sum, err
	}
	rules := screening.FromConfig(r.Cfg)
	blacklisted, rejected := map[string]bool{}, map[string]bool{}

	for _, term := range terms {
		r.emit("term", term, nil)
		if err := r.Site.Search(ctx, term); err != nil {
			return r.stop(&sum, err)
		}
		if err := r.Site.ApplyFilters(ctx, r.Cfg.Search, location); err != nil {
			if ctx.Err() != nil {
				return r.stop(&sum, ctx.Err())
			}
			r.emit("warn", "filters: "+err.Error(), nil)
		}
		if err := r.Site.WaitForCards(ctx); err != nil {
			if ctx.Err() != nil {
				return r.stop(&sum, ctx.Err())
			}
			r.emit("warn", "no job cards appeared for this term", nil)
			continue
		}
		perTerm := 0
	pages:
		for {
			for index := 0; ; index++ {
				if ctx.Err() != nil {
					return r.stop(&sum, ctx.Err())
				}
				cards, err := r.Site.Cards() // re-queried: the list is virtualised
				if err != nil {
					return r.stop(&sum, err)
				}
				if index >= len(cards) {
					break
				}
				if perTerm >= r.Cfg.Search.SwitchNumber || (opts.Max > 0 && r.Pacer.Applied() >= opts.Max) {
					break pages
				}
				done, err := r.handleCard(ctx, &sum, opts, rules, cards[index], applied, blacklisted, rejected)
				if err != nil {
					return r.stop(&sum, err)
				}
				if done {
					return sum, nil
				}
				perTerm++
			}
			more, err := r.Site.NextPage(ctx)
			if err != nil {
				return r.stop(&sum, err)
			}
			if !more {
				break
			}
		}
		if opts.Max > 0 && r.Pacer.Applied() >= opts.Max {
			sum.Reason = "--max reached"
			r.emit("limit", sum.Reason, nil)
			return sum, nil
		}
	}
	sum.Reason = "all search terms visited"
	return sum, nil
}

// handleCard processes one card; done is true when the run must end (a cap or
// LinkedIn's daily limit).
func (r *Runner) handleCard(ctx context.Context, sum *Summary, opts Options, rules screening.Rules, card browser.Element,
	applied, blacklisted, rejected map[string]bool) (done bool, err error) {
	c, err := r.Site.ReadCard(card)
	if err != nil {
		r.Logger.Warn("unreadable card", "error", err)
		return false, nil
	}
	job := screening.Job{ID: c.ID, Title: c.Title, Company: c.Company, AlreadyApplied: c.AlreadyApplied}
	if v := rules.Card(job, blacklisted, rejected, applied); v.Skip {
		r.emit("skip", v.Reason, &c)
		return false, nil
	}
	if err := r.Site.OpenCard(ctx, card); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		r.Logger.Warn("could not open card", "job", c.ID, "error", err)
		return false, nil
	}
	job.AboutCompany = r.Site.AboutCompany()
	if v := rules.Company(job); v.Skip {
		rejected[c.ID] = true
		if v.BlacklistCompany {
			blacklisted[c.Company] = true
		}
		return false, r.skip(sum, c, v)
	}
	job.Description, job.DescriptionOK = r.Site.Description(ctx)
	v := rules.Description(job)
	if v.Skip {
		rejected[c.ID] = true
		return false, r.skip(sum, c, v)
	}
	if _, how, found := r.Site.FindApplyButton(); !found {
		if r.Site.DailyLimitReached() {
			sum.Reason = "LinkedIn's daily Easy Apply limit reached"
			r.emit("limit", sum.Reason, &c)
			return true, nil
		}
		return false, r.skip(sum, c, screening.Verdict{Reason: "no apply button on the job page"})
	} else {
		r.Logger.Info("apply button found", "job", c.ID, "how", how)
	}
	if err := r.Pacer.Allow(); err != nil {
		sum.Reason = err.Error()
		r.emit("limit", sum.Reason, &c)
		return true, nil
	}
	if !opts.DryRun {
		return false, ErrApplyNotImplemented
	}
	// Dry run, this ticket: record what would be applied to.
	posted, reposted, _ := r.Site.DatePosted(r.Now())
	row := store.Job{JobID: c.ID, Outcome: store.Skipped, Reason: "dry run (would apply)", Title: c.Title, Company: c.Company,
		WorkLocation: c.Location, WorkStyle: c.Style, Description: job.Description, JobLink: c.Link,
		ExperienceRequired: fmt.Sprint(v.ExperienceRequired), Reposted: fmt.Sprint(reposted), RunID: sum.RunID}
	if !posted.IsZero() {
		row.DatePosted = store.Timestamp(posted)
	}
	if err := r.Store.UpsertJob(row); err != nil {
		return false, err
	}
	sum.WouldApply++
	sum.Skipped++
	r.Pacer.RecordApplication()
	r.emit("would-apply", "", &c)
	if err := r.Pacer.BetweenJobs(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func (r *Runner) skip(sum *Summary, c linkedin.Card, v screening.Verdict) error {
	sum.Skipped++
	r.emit("skip", v.Reason, &c)
	r.Logger.Info("skipped", "job", c.ID, "title", c.Title, "company", c.Company, "reason", v.Reason, "detail", v.Detail)
	return r.Store.UpsertJob(store.Job{JobID: c.ID, Outcome: store.Skipped, Reason: v.Reason, Title: c.Title, Company: c.Company,
		WorkLocation: c.Location, WorkStyle: c.Style, JobLink: c.Link, RunID: sum.RunID})
}

func (r *Runner) stop(sum *Summary, err error) (Summary, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		sum.Reason = "stopped by user"
		r.emit("limit", sum.Reason, nil)
		return *sum, nil
	}
	sum.Reason = "error: " + err.Error()
	return *sum, err
}

func (r *Runner) appliedIDs() (map[string]bool, error) {
	jobs, err := r.Store.ListJobs(store.JobFilter{Outcomes: []store.Outcome{store.Applied}})
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		ids[j.JobID] = true
	}
	return ids, nil
}
