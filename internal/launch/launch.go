// Package launch turns a loaded configuration into a run ready to go: the run
// lock, Chrome, the LinkedIn session, the applier and the runner. `aja run` and
// the daemon both start runs through it, so a daemon run is a terminal run with
// nobody at the terminal.
package launch

import (
	"context"
	"log/slog"
	"time"

	"github.com/luizfreitas27/aja/internal/ai"
	"github.com/luizfreitas27/aja/internal/apply"
	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/chrome"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/questions"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/store"
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
	OpenBrowser func(ctx context.Context, o browser.Options) (browser.Browser, error)
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

// Close closes Chrome (detaching from it) and releases the run lock.
func (o *Opened) Close() { o.close() }

// Open takes the machine-wide run lock (a *run.HeldError when another run has
// it), finds and opens Chrome and wires the runner.
func (p *Plan) Open(ctx context.Context, opts run.Options, d Deps) (*Opened, error) {
	cfg := p.Config
	lock, err := run.AcquireLock(d.Paths.DataDir, d.Origin, time.Now())
	if err != nil {
		return nil, err
	}
	bin, err := chrome.Find(cfg.Settings.ChromeBinary)
	if err != nil {
		lock.Release()
		return nil, err
	}
	profile := cfg.Settings.ChromeProfileDir
	if profile == "" {
		profile = chrome.DefaultProfileDir()
	}
	br, err := d.OpenBrowser(ctx, browser.Options{Bin: bin, ProfileDir: profile, DisableExtensions: cfg.Settings.DisableExtensions,
		SmoothScroll: cfg.Settings.SmoothScroll, Logger: d.Logger})
	if err != nil {
		lock.Release()
		return nil, err
	}

	pacer := pacing.New(cfg.Pacing, opts.Fast)
	session := linkedin.NewSession(br.Main(), br, pacer, d.Logger)
	session.CloseTabs = cfg.Settings.CloseTabs
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
			d.Notifier.Send(ctx, "aja is waiting for you to sign in to LinkedIn in the Chrome window.")
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
