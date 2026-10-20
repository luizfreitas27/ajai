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
	"os"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/apply"
	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/markets"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/pacing"
	"github.com/luizfreitas27/ajai/internal/paths"
	"github.com/luizfreitas27/ajai/internal/screening"
	"github.com/luizfreitas27/ajai/internal/store"
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
	AlreadyAppliedOnPage() bool
	AboutCompany() string
	Description(ctx context.Context) (string, bool)
	DatePosted(now time.Time) (posted time.Time, reposted, ok bool)
	FindApplyButton() (browser.Element, linkedin.ApplyKind, string)
	ExternalLink(ctx context.Context, button browser.Element) (string, error)
	DailyLimitReached() bool
	NextPage(ctx context.Context) (bool, error)
	// HiringTeam reads the hirer a free Message button is offered for on the job
	// page; ok is false when there is none.
	HiringTeam(ctx context.Context) (messages.Recipient, bool, error)
	// SendMessage sends text to the hirer of job jobID, after checking that the hirer
	// is still to (see linkedin.SendResult).
	SendMessage(ctx context.Context, to messages.Recipient, jobID, text string) (linkedin.SendResult, error)
}

// Applier works one Easy Apply modal; apply.Applier in production.
type Applier interface {
	Apply(ctx context.Context, job linkedin.Card, button browser.Element, opts apply.Options) (apply.Outcome, error)
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

// ResolveUnattended decides whether a run may pause for help: only with a person
// at a terminal on both ends. --unattended, --json (output for a script), a piped
// stdout or a non-terminal stdin (the daemon, a script) all mean nobody can answer.
func ResolveUnattended(flag, jsonOutput, stdinIsTerminal, stdoutIsTerminal bool) bool {
	return flag || jsonOutput || !stdinIsTerminal || !stdoutIsTerminal
}

// PausesAfterFilters: search.pause_after_filters applies only with someone at
// the terminal.
func (o Options) PausesAfterFilters(c config.Config) bool {
	return c.Search.PauseAfterFilters && !o.Unattended
}

// errInvalidOptions marks a mistake in the command line: the person typing it
// sees the message, so it is not sent to Telegram.
var errInvalidOptions = errors.New("invalid options")

// Validate rejects option combinations that make no sense.
func (o Options) Validate() error {
	if o.Fast && !o.DryRun {
		return fmt.Errorf("%w: --fast is allowed only together with --dry-run", errInvalidOptions)
	}
	return nil
}

// EventKind classifies an Event.
type EventKind string

// The event kinds.
const (
	EventLogin      EventKind = "login"
	EventTerm       EventKind = "term"
	EventSkip       EventKind = "skip"
	EventWouldApply EventKind = "would-apply"
	EventApplied    EventKind = "applied"
	EventFailed     EventKind = "failed"
	EventExternal   EventKind = "external"
	EventLimit      EventKind = "limit"
	EventWarn       EventKind = "warn"
	EventRunEnd     EventKind = "run-end" // Summary is set
	EventStart      EventKind = "start"   // the run's settings, before it begins
	EventWait       EventKind = "wait"    // a non-stop session waits before the next run
)

// Event is something the user should see as it happens.
type Event struct {
	Kind    EventKind
	Text    string
	Card    *linkedin.Card
	Summary *Summary
}

// Summary is the run summary.
type Summary struct {
	RunID      int64  `json:"run_id"`
	Market     string `json:"market"`
	DryRun     bool   `json:"dry_run"`
	Applied    int    `json:"applied"`
	External   int    `json:"external"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	WouldApply int    `json:"would_apply"`
	// Captured counts answers the user typed during help pauses and ajai remembered.
	Captured int `json:"captured"`
	// PendingAnswers is the review queue's size when the run ended.
	PendingAnswers int `json:"pending_answers"`
	// Reason says, in words, why the run ended; End says it for code.
	Reason string `json:"reason"`
	End    End    `json:"end"`
}

// End classifies why a run ended.
type End string

// The ways a run ends.
const (
	EndAllTerms   End = "all-terms"   // every search term visited
	EndPerRunCap  End = "per-run-cap" // pacing.max_per_run
	EndHourlyCap  End = "hourly-cap"  // pacing.max_applications_per_hour
	EndMax        End = "max"         // --max
	EndDailyLimit End = "daily-limit" // LinkedIn's daily Easy Apply limit
	EndStopped    End = "stopped"     // Ctrl-C
	EndTimedOut   End = "timed-out"
	EndError      End = "error"
)

// ended records how and why a run ended.
func (s *Summary) ended(e End, reason string) {
	s.End, s.Reason = e, reason
}

// Continues reports whether a non-stop session starts another run after one
// that ended this way: only after a complete pass or the per-run cap. LinkedIn's
// daily limit, the hourly cap, --max, Ctrl-C and errors end the session.
func (e End) Continues() bool { return e == EndAllTerms || e == EndPerRunCap }

// Runner holds the wiring for one run.
type Runner struct {
	Cfg     config.Config
	Market  markets.Resolved
	Site    Site
	Applier Applier
	Store   *store.Store
	Pacer   *pacing.Pacer
	Logger  *slog.Logger
	Events  func(Event)
	Now     func() time.Time
	// LoginTimeout is how long to wait for a manual login (default 10 minutes).
	LoginTimeout time.Duration
	// StatePath, when set, receives the live State after every change.
	StatePath string
	// ResumePath is the absolute resume to upload ("" keeps LinkedIn's previous one).
	ResumePath string
	// Notifier, when set, gets exactly one message when the run ends: the summary,
	// or the error with what was done until then.
	Notifier Notifier
	// AfterFilters, when set (an attended run with search.pause_after_filters), is
	// called once a term's filters are applied, so the user can check them in Chrome.
	// It returns false to stop pausing for the rest of the session.
	AfterFilters func(ctx context.Context, term string) (keepPausing bool)
	// NonStopWait is the pause between the runs of a non-stop session (default 10 minutes,
	// as the Python bot waited).
	NonStopWait time.Duration
	// Sleep waits d or until ctx ends; time-based by default, replaced in tests.
	Sleep func(ctx context.Context, d time.Duration) error

	filtersPaused bool // the user typed "off" at a filter pause

	started    time.Time
	inProgress *Summary // the summary run() is filling, for Run's crash path
}

func (r *Runner) emit(kind EventKind, text string, card *linkedin.Card) {
	r.emitEvent(Event{Kind: kind, Text: text, Card: card})
}

func (r *Runner) emitEvent(e Event) {
	if r.Events != nil {
		r.Events(e)
	}
}

// runState is what one run accumulates while walking the terms.
type runState struct {
	applied     map[string]bool // our own history
	blacklisted map[string]bool // companies blacklisted during this run
	rejected    map[string]bool // job ids rejected during this run
	// handled maps each posting (linkedin.Card.Posting) this run settled to the
	// job it was settled under, so neither that job nor a repost of the role is
	// opened again in this run. Only verdicts about the role itself settle it: a
	// failure, a missing apply button or an external application does not.
	handled map[string]string
}

// handle settles the job's posting for the rest of the run (a settled posting
// is never opened again, so it is settled once).
func (s *runState) handle(c linkedin.Card) { s.handled[c.Posting()] = c.ID }

// reject rejects the job for the rest of the run, and settles its posting.
func (s *runState) reject(c linkedin.Card) {
	s.rejected[c.ID] = true
	s.handle(c)
}

func (r *Runner) saveState(sum *Summary, term string) {
	if r.StatePath == "" {
		return
	}
	st := State{PID: os.Getpid(), Started: r.started, RunID: sum.RunID, DryRun: sum.DryRun, Market: sum.Market,
		Applied: sum.Applied, External: sum.External, Failed: sum.Failed, Skipped: sum.Skipped, WouldApply: sum.WouldApply, Term: term}
	if err := WriteState(r.StatePath, st); err != nil {
		r.Logger.Warn("could not write the run state", "error", err)
	}
}

// Run executes one run and returns its summary. A cancelled context ends the
// run cleanly with reason "stopped by user" (or a stop request's). The run is recorded in the store,
// with the review queue's size, however it ends.
func (r *Runner) Run(ctx context.Context, opts Options) (sum Summary, err error) {
	r.inProgress = nil
	defer func() {
		// A bug must not end a run silently: record it and report it, then crash on
		// with a *Crash, so a caller that recovers (the daemon) knows it is done.
		if p := recover(); p != nil {
			crash := &Crash{Value: p}
			if r.inProgress != nil {
				sum = *r.inProgress
			}
			if sum.Reason == "" {
				sum.ended(EndError, "crashed: "+fmt.Sprint(p))
			}
			r.record(&sum)
			r.notifyEnd(sum, crash)
			panic(crash)
		}
	}()
	sum, err = r.run(ctx, opts)
	if err != nil && sum.Reason == "" {
		sum.ended(EndError, "error: "+err.Error())
	}
	if errors.Is(err, errInvalidOptions) {
		return sum, err // a mistake on the command line, not a run
	}
	if err == nil && !opts.DryRun {
		r.sendMessages(ctx)
	}
	r.record(&sum)
	r.notifyEnd(sum, err)
	ended := sum
	r.emitEvent(Event{Kind: EventRunEnd, Text: sum.Reason, Summary: &ended})
	return sum, err
}

// record writes how the run ended to the store and removes the live state.
// A run that could not even be recorded (RunID 0) has nothing to finish.
func (r *Runner) record(sum *Summary) {
	if sum.RunID == 0 {
		return
	}
	if n, cerr := r.Store.CountAnswers("pending"); cerr == nil {
		sum.PendingAnswers = n
	}
	_ = r.Store.FinishRun(context.Background(), store.Run{ID: sum.RunID, Applied: sum.Applied, External: sum.External,
		Failed: sum.Failed, Skipped: sum.Skipped, PendingAnswers: sum.PendingAnswers, Reason: sum.Reason})
	if r.StatePath != "" {
		RemoveState(r.StatePath)
	}
}

// endAbandoned ends the runs dead processes left in progress (killed,
// crashed). A run holds the run lock and has no row yet, so no run is in
// progress: the run a state file names, if it has no end, was left by a dead
// process and ends with the counts it last saved (the file stays: this run
// writes its own next), and any other run left without an end ends too.
func (r *Runner) endAbandoned() {
	if old, err := ReadState(r.StatePath); r.StatePath != "" && err == nil {
		if _, err := EndStale(context.Background(), r.Store, r.StatePath, old, true); err != nil {
			r.Logger.Warn("could not end the run a dead process left in progress", "run", old.RunID, "error", err)
		}
	}
	n, err := r.Store.EndAbandonedRuns(context.Background(), StaleReason)
	switch {
	case err != nil:
		r.Logger.Warn("could not end the runs dead processes left in progress", "error", err)
	case n > 0:
		r.Logger.Info("ended runs dead processes left in progress", "runs", n)
	}
}

// Crash is what Run panics with after recording and reporting a panic in a run.
type Crash struct{ Value any }

// Error says the run crashed, with what.
func (c *Crash) Error() string { return fmt.Sprintf("ajai crashed: %v", c.Value) }

func (r *Runner) run(ctx context.Context, opts Options) (Summary, error) {
	r.defaults()
	if err := opts.Validate(); err != nil {
		return Summary{}, err
	}
	r.started = r.Now()
	sum := Summary{Market: string(r.Market.Name), DryRun: opts.DryRun}
	r.inProgress = &sum
	r.endAbandoned()
	id, err := r.Store.StartRun(context.Background(), sum.Market, opts.DryRun) // the record must exist even when ctx is already cancelled
	if err != nil {
		return sum, err
	}
	sum.RunID = id
	r.saveState(&sum, "")

	err = r.Site.EnsureLoggedIn(ctx, r.LoginTimeout, func() {
		r.emit(EventLogin, "Sign in to LinkedIn in the Chrome window; ajai continues when the feed appears.", nil)
	})
	if err != nil {
		if ctx.Err() != nil {
			return r.stop(ctx, &sum, ctx.Err())
		}
		sum.ended(EndError, "login: "+err.Error())
		return sum, err
	}

	terms := append([]string(nil), opts.Terms...)
	if len(terms) == 0 {
		terms = append(terms, r.Market.Terms...)
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
	state := runState{applied: applied, blacklisted: map[string]bool{}, rejected: map[string]bool{}, handled: map[string]string{}}

	for _, term := range terms {
		r.emit(EventTerm, term, nil)
		r.saveState(&sum, term)
		if err := r.Site.Search(ctx, term); err != nil {
			return r.stop(ctx, &sum, err)
		}
		if err := r.Site.ApplyFilters(ctx, r.Cfg.Search, location); err != nil {
			if ctx.Err() != nil {
				return r.stop(ctx, &sum, ctx.Err())
			}
			r.emit(EventWarn, "filters: "+err.Error(), nil)
		}
		if r.AfterFilters != nil && !r.filtersPaused {
			if !r.AfterFilters(ctx, term) {
				r.filtersPaused = true
			}
			if ctx.Err() != nil {
				return r.stop(ctx, &sum, ctx.Err())
			}
		}
		if err := r.Site.WaitForCards(ctx); err != nil {
			if ctx.Err() != nil {
				return r.stop(ctx, &sum, ctx.Err())
			}
			r.emit(EventWarn, "no job cards appeared for this term", nil)
			continue
		}
		perTerm := 0 // applications (or would-applies) for this term: switch_number counts those, not cards
	pages:
		for {
			for index := 0; ; index++ {
				if ctx.Err() != nil {
					return r.stop(ctx, &sum, ctx.Err())
				}
				cards, err := r.Site.Cards() // re-queried: the list is virtualised
				if err != nil {
					return r.stop(ctx, &sum, err)
				}
				if index >= len(cards) {
					break
				}
				if perTerm >= r.Cfg.Search.SwitchNumber || (opts.Max > 0 && r.Pacer.Total() >= opts.Max) {
					break pages
				}
				outcome, err := r.handleCard(ctx, &sum, opts, rules, cards[index], &state)
				if err != nil {
					return r.stop(ctx, &sum, err)
				}
				r.saveState(&sum, term)
				switch outcome {
				case cardEndsRun:
					return sum, nil
				case cardCounted:
					perTerm++
				}
			}
			more, err := r.Site.NextPage(ctx)
			if err != nil {
				return r.stop(ctx, &sum, err)
			}
			if !more {
				break
			}
		}
		if opts.Max > 0 && r.Pacer.Total() >= opts.Max {
			sum.ended(EndMax, "--max reached")
			r.emit(EventLimit, sum.Reason, nil)
			return sum, nil
		}
	}
	sum.ended(EndAllTerms, "all search terms visited")
	return sum, nil
}

// cardOutcome says what handleCard did with a card.
type cardOutcome int

const (
	cardPassed  cardOutcome = iota // skipped or unreadable: nothing counted toward switch_number
	cardCounted                    // applied, collected or would-apply: counts toward switch_number
	cardEndsRun                    // a cap or LinkedIn's daily limit
)

// DailyLimitStateKey marks, in the store's state table, when LinkedIn's daily limit was last hit.
const DailyLimitStateKey = "daily_limit_reached_at"

// handleCard processes one card.
func (r *Runner) handleCard(ctx context.Context, sum *Summary, opts Options, rules screening.Rules, card browser.Element, st *runState) (cardOutcome, error) {
	c, err := r.Site.ReadCard(card)
	if err != nil {
		r.Logger.Warn("unreadable card", "error", err)
		return cardPassed, nil
	}
	job := screening.Job{ID: c.ID, Title: c.Title, Company: c.Company, AlreadyApplied: c.AlreadyApplied}
	if v := rules.Card(job, st.blacklisted, st.rejected, st.applied); v.Skip {
		// Counted, not stored: LinkedIn's own badge and our history are not new facts about the job.
		sum.Skipped++
		r.emit(EventSkip, v.Reason, &c)
		return cardPassed, nil
	}
	if first, ok := st.handled[c.Posting()]; ok { // listed again, or the same role posted again
		reason := "already handled in this run"
		if first != c.ID {
			reason = "the same posting as job " + first + ", already handled in this run"
		}
		sum.Skipped++
		r.emit(EventSkip, reason, &c)
		return cardPassed, nil
	}
	if err := r.Site.OpenCard(ctx, card); err != nil {
		if ctx.Err() != nil {
			return cardPassed, ctx.Err()
		}
		r.Logger.Warn("could not open card", "job", c.ID, "error", err)
		return cardPassed, nil
	}
	if r.Site.AlreadyAppliedOnPage() {
		st.handle(c)
		sum.Skipped++
		r.emit(EventSkip, "already applied", &c)
		return cardPassed, nil
	}
	job.AboutCompany = r.Site.AboutCompany()
	if v := rules.Company(job); v.Skip {
		st.reject(c)
		if v.BlacklistCompany {
			st.blacklisted[c.Company] = true
		}
		return cardPassed, r.skip(sum, c, v)
	}
	job.Description, job.DescriptionOK = r.Site.Description(ctx)
	v := rules.Description(job)
	if v.Skip {
		st.reject(c)
		return cardPassed, r.skip(sum, c, v)
	}
	button, kind, how := r.Site.FindApplyButton()
	switch kind {
	case linkedin.NoApplyButton:
		if r.Site.DailyLimitReached() {
			sum.ended(EndDailyLimit, "LinkedIn's daily Easy Apply limit reached")
			_ = r.Store.SetState(DailyLimitStateKey, store.Timestamp(r.Now()))
			r.emit(EventLimit, sum.Reason, &c)
			return cardEndsRun, nil
		}
		return cardPassed, r.skip(sum, c, screening.Verdict{Reason: "no apply button on the job page"})
	case linkedin.ExternalApply:
		if r.Cfg.Search.EasyApplyOnly {
			return cardPassed, r.skip(sum, c, screening.Verdict{Reason: "external application (easy_apply_only)"})
		}
		link := ""
		if !opts.DryRun {
			if link, err = r.Site.ExternalLink(ctx, button); err != nil {
				if ctx.Err() != nil {
					return cardPassed, ctx.Err()
				}
				sum.Failed++
				r.emit(EventFailed, "could not open the external application", &c)
				return cardPassed, r.Store.UpsertJob(store.Job{JobID: c.ID, Outcome: store.Failed, Reason: "could not open the external application",
					StackTrace: err.Error(), Title: c.Title, Company: c.Company, WorkLocation: c.Location, WorkStyle: c.Style, JobLink: c.Link, RunID: sum.RunID})
			}
		}
		sum.External++
		r.emit(EventExternal, link, &c)
		return cardCounted, r.Store.UpsertJob(store.Job{JobID: c.ID, Outcome: store.External, Title: c.Title, Company: c.Company,
			WorkLocation: c.Location, WorkStyle: c.Style, JobLink: c.Link, ExternalLink: link, DateApplied: "Pending", RunID: sum.RunID})
	}
	r.Logger.Info("easy apply button found", "job", c.ID, "how", how)
	if err := r.Pacer.Allow(); err != nil {
		end := EndError
		var le *pacing.LimitError
		if errors.As(err, &le) {
			end = map[string]End{pacing.CapPerRun: EndPerRunCap, pacing.CapPerHour: EndHourlyCap}[le.Cap]
		}
		sum.ended(end, err.Error())
		r.emit(EventLimit, sum.Reason, &c)
		return cardEndsRun, nil
	}
	posted, reposted, _ := r.Site.DatePosted(r.Now())
	row := store.Job{JobID: c.ID, Title: c.Title, Company: c.Company, WorkLocation: c.Location, WorkStyle: c.Style,
		Description: job.Description, JobLink: c.Link, ExperienceRequired: fmt.Sprint(v.ExperienceRequired),
		Reposted: fmt.Sprint(reposted), RunID: sum.RunID, ConnectRequest: "In Development", HRName: "Unknown", HRLink: "Unknown"}
	if !posted.IsZero() {
		row.DatePosted = store.Timestamp(posted)
	}
	outcome, err := r.Applier.Apply(ctx, c, button, apply.Options{DryRun: opts.DryRun, ResumePath: r.ResumePath,
		FollowCompanies: r.Cfg.Settings.FollowCompanies, Description: job.Description})
	row.Resume, row.Screenshot = outcome.Resume, outcome.Screenshot
	sum.Captured += outcome.Captured
	if outcome.Captured > 0 || outcome.Queued > 0 {
		r.Logger.Info("answer memory grew", "job", c.ID, "captured", outcome.Captured, "queued_for_review", outcome.Queued)
	}
	counted := false
	switch outcome.Kind {
	case apply.Applied:
		// Recorded BEFORE the context check: a submitted application must never be lost
		// because Ctrl-C landed a moment later.
		sum.Applied++
		row.Outcome, row.DateApplied, row.Questions = store.Applied, store.Timestamp(r.Now()), strings.Join(outcome.Answered, "; ")
		r.Pacer.RecordApplication()
		st.handle(c)
		r.emit(EventApplied, "", &c)
		counted = true
	case apply.DryRun:
		sum.WouldApply++
		row.Outcome, row.Reason = store.Skipped, "dry run"
		r.Pacer.RecordApplication()
		st.handle(c)
		r.emit(EventWouldApply, "", &c)
		counted = true
	case apply.Unanswered:
		sum.Skipped++
		st.reject(c)
		row.Outcome, row.Reason = store.Skipped, "unanswered questions: "+strings.Join(outcome.Blocked, " | ")
		text := row.Reason
		if outcome.Queued > 0 {
			text += fmt.Sprintf(" (%d added to the review queue: ajai answers review)", outcome.Queued)
		}
		r.emit(EventSkip, text, &c)
	case apply.Stopped:
		return cardPassed, ctx.Err()
	default: // Failed
		sum.Failed++
		row.Outcome, row.Reason = store.Failed, outcome.Stage
		if err != nil {
			row.StackTrace = err.Error()
		}
		r.emit(EventFailed, outcome.Stage, &c)
		r.Logger.Warn("application failed", "job", c.ID, "stage", outcome.Stage, "error", err)
	}
	if err := r.Store.UpsertJob(row); err != nil {
		return cardPassed, err
	}
	if row.Outcome == store.Applied && !opts.DryRun {
		r.queueMessage(ctx, c, sum.RunID)
	}
	if ctx.Err() != nil {
		return cardPassed, ctx.Err()
	}
	if counted {
		if err := r.Pacer.BetweenJobs(ctx); err != nil {
			return cardPassed, err
		}
		return cardCounted, nil
	}
	return cardPassed, nil
}

// ResolveResume picks the file to upload: the market's resume, else the general
// one, each relative to the resumes directory unless absolute. "" (and a note
// why) when neither exists, in which case LinkedIn's previous upload is used.
func ResolveResume(p paths.Paths, market markets.Resolved, general string) (path string, note string) {
	for _, candidate := range []string{market.Resume, general} {
		if candidate == "" {
			continue
		}
		full := p.ResumePath(candidate)
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			return full, ""
		}
		note += full + " not found; "
	}
	return "", strings.TrimSuffix(note, "; ") + "; LinkedIn's previously uploaded resume will be used"
}

func (r *Runner) skip(sum *Summary, c linkedin.Card, v screening.Verdict) error {
	sum.Skipped++
	r.emit(EventSkip, v.Reason, &c)
	r.Logger.Info("skipped", "job", c.ID, "title", c.Title, "company", c.Company, "reason", v.Reason, "detail", v.Detail)
	return r.Store.UpsertJob(store.Job{JobID: c.ID, Outcome: store.Skipped, Reason: v.Reason, Title: c.Title, Company: c.Company,
		WorkLocation: c.Location, WorkStyle: c.Style, JobLink: c.Link, RunID: sum.RunID})
}

// stop ends the run for err. A cancelled context is the user stopping it: Ctrl-C,
// or a stop request, whose reason is recorded.
func (r *Runner) stop(ctx context.Context, sum *Summary, err error) (Summary, error) {
	switch {
	case errors.Is(err, context.Canceled):
		reason := "stopped by user"
		var sr *StopRequested
		if errors.As(context.Cause(ctx), &sr) {
			reason = sr.Reason
		}
		sum.ended(EndStopped, reason)
		r.emit(EventLimit, sum.Reason, nil)
		return *sum, nil
	case errors.Is(err, context.DeadlineExceeded):
		sum.ended(EndTimedOut, "timed out")
		r.emit(EventLimit, sum.Reason, nil)
		return *sum, nil
	}
	sum.ended(EndError, "error: "+err.Error())
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
