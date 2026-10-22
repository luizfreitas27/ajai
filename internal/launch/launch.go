// Package launch turns a loaded configuration into a run ready to go: the run
// lock, Chrome, the LinkedIn session, the applier and the runner. `ajai run` and
// the daemon both start runs through it, so a daemon run is a terminal run with
// nobody at the terminal.
package launch

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/luizfreitas27/ajai/internal/ai"
	"github.com/luizfreitas27/ajai/internal/apply"
	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/chrome"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/markets"
	"github.com/luizfreitas27/ajai/internal/memory"
	"github.com/luizfreitas27/ajai/internal/notify"
	"github.com/luizfreitas27/ajai/internal/pacing"
	"github.com/luizfreitas27/ajai/internal/paths"
	"github.com/luizfreitas27/ajai/internal/questions"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/store"
)

// Plan is a run worked out from the configuration before anything is opened,
// so the caller can show what it is about to do.
type Plan struct {
	Config     config.Config
	Market     markets.Resolved
	AI         *ai.Client // nil when the AI is off
	ResumePath string     // "" keeps LinkedIn's previous resume
	ResumeNote string     // why a configured resume is not used, when it is not
}

// NewPlan resolves the market, the AI client and the resume.
func NewPlan(l *config.Loaded, p paths.Paths, logger *slog.Logger) (*Plan, error) {
	cfg := l.Config
	market := markets.Resolve(cfg)
	aiClient, err := ai.FromConfig(cfg, l.Secrets, market, logger)
	if err != nil {
		return nil, err
	}
	resume, note := run.ResolveResume(p, market, cfg.Questions.Resume)
	return &Plan{Config: cfg, Market: market, AI: aiClient, ResumePath: resume, ResumeNote: note}, nil
}

// Deps are what the caller adds to a plan to open it.
type Deps struct {
	Origin   run.Origin       // who starts the run, recorded in the run lock
	Window   int64            // the schedule window that starts it (OriginSchedule)
	Paths    paths.Paths      // the data directory (lock, state, stop requests) and screenshots
	Store    *store.Store     // required
	Logger   *slog.Logger     // required
	Notifier *notify.Telegram // nil: no Telegram
	Events   func(run.Event)  // nil: nobody watches the events
	// Helper answers help pauses; used only when the run is attended.
	Helper apply.Helper
	// AfterFilters is the filter pause; used only when Options.PausesAfterFilters.
	AfterFilters func(ctx context.Context, term string) (keepPausing bool)
	// OpenBrowser opens Chrome (browser.Open; tests fake it). Required.
	OpenBrowser browser.Opener
}

// Opened is a run with its lock and Chrome: Session runs it, Close releases both.
type Opened struct {
	Runner *run.Runner
	opts   run.Options
	lock   *run.Lock
	close  func()
}

// Session runs the run (or non-stop session), ending it cleanly, with the
// request's reason, when a stop request arrives.
func (o *Opened) Session(ctx context.Context) ([]run.Summary, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go o.lock.WatchStops(ctx, cancel)
	return o.Runner.Session(ctx, o.opts)
}

// Close closes Chrome (quitting it on the ajai profile, detaching from the
// user's) and releases the run lock.
func (o *Opened) Close() { o.close() }

// Open takes the machine-wide run lock (a *run.HeldError when another run has
// it), finds and opens Chrome and wires the runner.
func (p *Plan) Open(ctx context.Context, opts run.Options, d Deps) (*Opened, error) {
	cfg := p.Config
	lock, err := run.AcquireLock(d.Paths.DataDir, run.Holder{Origin: d.Origin, Window: d.Window, Started: time.Now()})
	if err != nil {
		return nil, err
	}
	br, _, err := OpenChrome(ctx, cfg, d.Paths, d.OpenBrowser, d.Logger)
	if err != nil {
		lock.Release()
		return nil, err
	}

	pacer := pacing.New(cfg.Pacing, opts.Fast)
	session := linkedin.NewSession(br.Main(), br, pacer, d.Logger)
	session.CloseTabs, session.ScreenshotDir = cfg.Settings.CloseTabs, d.Paths.ScreenshotDir
	answerer := questions.New(cfg, memory.New(d.Store, d.Logger), nil, d.Logger)
	if p.AI != nil {
		answerer.AI = p.AI
	}
	applier := &apply.Applier{Page: br.Main(), Pacer: pacer, Answerer: answerer, Logger: d.Logger, ScreenshotDir: d.Paths.ScreenshotDir}
	if !opts.Unattended {
		applier.Helper = d.Helper
	}
	events := func(e run.Event) {
		if d.Events != nil {
			d.Events(e)
		}
		if e.Kind == run.EventLogin {
			d.Notifier.Send(ctx, "ajai is waiting for you to sign in to LinkedIn in the Chrome window.")
		}
	}
	runner := &run.Runner{Cfg: cfg, Market: p.Market, Site: session, Applier: applier, Store: d.Store, Pacer: pacer, Logger: d.Logger,
		Events: events, StatePath: run.StatePath(d.Paths.DataDir), ResumePath: p.ResumePath}
	if d.Notifier != nil {
		runner.Notifier = d.Notifier
	}
	if opts.PausesAfterFilters(cfg) {
		runner.AfterFilters = d.AfterFilters
	}
	return &Opened{Runner: runner, opts: opts, lock: lock, close: func() {
		_ = br.Close()
		lock.Release()
	}}, nil
}

// Messaging is Chrome and a LinkedIn session for ajai messages send, under the
// run lock: Send sends, Close releases both.
type Messaging struct {
	Runner *run.Runner
	lock   *run.Lock
	close  func()
}

// Send sends the pending recruiter messages (run.Runner.SendPending), ending
// cleanly, with the request's reason, when a stop request arrives.
func (m *Messaging) Send(ctx context.Context, opts run.PendingOptions, report func(run.SendLine)) (run.PendingSummary, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go m.lock.WatchStops(ctx, cancel)
	return m.Runner.SendPending(ctx, opts, report)
}

// Close closes Chrome and releases the run lock, as Opened.Close does.
func (m *Messaging) Close() { m.close() }

// OpenMessaging is Open for ajai messages send: the run lock (with
// run.OriginMessages), Chrome on the configured profile and a session, with
// no applier, so nothing can search or apply.
func (p *Plan) OpenMessaging(ctx context.Context, d Deps) (*Messaging, error) {
	cfg := p.Config
	lock, err := run.AcquireLock(d.Paths.DataDir, run.Holder{Origin: run.OriginMessages, Started: time.Now()})
	if err != nil {
		return nil, err
	}
	br, _, err := OpenChrome(ctx, cfg, d.Paths, d.OpenBrowser, d.Logger)
	if err != nil {
		lock.Release()
		return nil, err
	}
	pacer := pacing.New(cfg.Pacing, false)
	session := linkedin.NewSession(br.Main(), br, pacer, d.Logger)
	session.CloseTabs, session.ScreenshotDir = cfg.Settings.CloseTabs, d.Paths.ScreenshotDir
	runner := &run.Runner{Cfg: cfg, Market: p.Market, Site: session, Store: d.Store, Pacer: pacer, Logger: d.Logger, Events: d.Events}
	return &Messaging{Runner: runner, lock: lock, close: func() {
		_ = br.Close()
		lock.Release()
	}}, nil
}

// ErrOwnProfileInUse: the ajai profile is open in another Chrome window. It is
// also a browser.ErrProfileInUse for errors.Is.
var ErrOwnProfileInUse error = ownProfileInUse{}

type ownProfileInUse struct{}

func (ownProfileInUse) Error() string {
	return "the ajai profile is in use by another Chrome window (a run, or ajai login): close it, or wait for the run to end, and try again"
}

func (ownProfileInUse) Is(target error) bool { return target == browser.ErrProfileInUse }

// OpenChrome finds Chrome and opens it on the configured profile, which it
// returns: the ajai profile (created when missing, and quit again by Close)
// unless settings.chrome_profile_dir names another.
func OpenChrome(ctx context.Context, cfg config.Config, p paths.Paths, open browser.Opener, logger *slog.Logger) (browser.Browser, paths.Profile, error) {
	profile := p.ChromeProfile(cfg.Settings.ChromeProfileDir)
	bin, err := chrome.Find(cfg.Settings.ChromeBinary)
	if err != nil {
		return nil, profile, err
	}
	if profile.Own {
		if err := os.MkdirAll(profile.Dir, 0o700); err != nil { // cookies live here: the user's eyes only
			return nil, profile, err
		}
	}
	br, err := open(ctx, browser.Options{Bin: bin, ProfileDir: profile.Dir, DisableExtensions: cfg.Settings.DisableExtensions,
		SmoothScroll: cfg.Settings.SmoothScroll, Window: browser.Window(cfg.Settings.Window),
		QuitOnClose: profile.Own, Logger: logger})
	if profile.Own && errors.Is(err, browser.ErrProfileInUse) {
		return nil, profile, ErrOwnProfileInUse
	}
	return br, profile, err
}
