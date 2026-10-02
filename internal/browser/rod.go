package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/luizfreitas27/aja/internal/procs"
)

// Options for opening Chrome.
type Options struct {
	Bin               string // Chrome executable; "" lets rod look it up
	ProfileDir        string // user-data directory to attach to
	DisableExtensions bool
	// SmoothScroll scrolls elements into view smoothly (settings.smooth_scroll).
	SmoothScroll bool
	// HeadlessForTests is honoured ONLY by the browser-tagged tests; the CLI never sets it
	// and a run is never headless (ADR 0001).
	HeadlessForTests bool
	// QuitOnClose: the profile is the aja profile, so Close quits Chrome (gracefully, the
	// profile kept) and the next run finds it free. Otherwise Close only detaches.
	QuitOnClose bool
	Logger      *slog.Logger
}

// quitWait bounds how long Close waits for Chrome to exit on its own.
const quitWait = 15 * time.Second

// exited waits up to d for process pid to end.
func exited(pid int, d time.Duration) bool {
	for deadline := time.Now().Add(d); procs.Alive(pid); {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

// ErrProfileInUse means the profile directory is locked by a running Chrome.
var ErrProfileInUse = errors.New("your Chrome profile is in use: close Chrome (every window) and run again, or leave settings.chrome_profile_dir blank to use the aja profile")

// Open launches Chrome on the given profile and connects to it.
func Open(ctx context.Context, opts Options) (Browser, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.HeadlessForTests && !testing.Testing() {
		return nil, errors.New("a headless browser is allowed only inside a test binary (ADR 0001)")
	}
	l := launcher.New().
		Headless(opts.HeadlessForTests).
		Leakless(false). // the user's Chrome must survive aja exiting
		Set("disable-blink-features", "AutomationControlled").
		Delete("enable-automation").
		Delete("no-startup-window")
	if opts.Bin != "" {
		l = l.Bin(opts.Bin)
	}
	if opts.ProfileDir != "" {
		l = l.UserDataDir(opts.ProfileDir)
	}
	if opts.DisableExtensions {
		l = l.Set("disable-extensions")
	}
	if opts.ProfileDir != "" && profileLocked(opts.ProfileDir) {
		return nil, ErrProfileInUse
	}
	controlURL, err := l.Launch()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "profile") || strings.Contains(err.Error(), "already running") {
			return nil, fmt.Errorf("%w (%v)", ErrProfileInUse, err)
		}
		return nil, fmt.Errorf("launching Chrome: %w", err)
	}
	b := rod.New().ControlURL(controlURL).Context(ctx)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("connecting to Chrome: %w", err)
	}
	pages, err := b.Pages()
	if err != nil {
		return nil, err
	}
	var main *rod.Page
	if len(pages) > 0 {
		main = pages[0]
	} else if main, err = b.Page(proto.TargetCreateTarget{URL: "about:blank"}); err != nil {
		return nil, err
	}
	opts.Logger.Info("chrome attached", "profile", opts.ProfileDir, "headless", opts.HeadlessForTests)
	return &rodBrowser{b: b, main: &rodPage{p: main, smooth: opts.SmoothScroll}, launcher: l, logger: opts.Logger,
		testBrowser: opts.HeadlessForTests, quit: opts.QuitOnClose}, nil
}

// profileLocked reports whether a LIVE Chrome holds the profile's singleton
// lock. On Linux and macOS the lock is a symlink named "<host>-<pid>"; a lock
// left by a crashed Chrome points at a dead pid and does not count.
func profileLocked(dir string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	if i := strings.LastIndex(target, "-"); i >= 0 {
		if pid, err := strconv.Atoi(target[i+1:]); err == nil {
			return procs.Alive(pid)
		}
	}
	return true
}

type rodBrowser struct {
	b           *rod.Browser
	main        *rodPage
	launcher    *launcher.Launcher
	logger      *slog.Logger
	testBrowser bool // a headless test browser: shut down, profile and all
	quit        bool // the aja profile: quit Chrome on Close, keep the profile
}

func (r *rodBrowser) Main() Page { return r.main }

func (r *rodBrowser) Pages() ([]Page, error) {
	pages, err := r.b.Pages()
	if err != nil {
		return nil, err
	}
	out := []Page{r.main}
	for _, p := range pages {
		if p.TargetID != r.main.p.TargetID {
			out = append(out, &rodPage{p: p, smooth: r.main.smooth})
		}
	}
	return out, nil
}

// Close leaves Chrome as it should be after aja: on the user's own profile it
// only detaches (the window is theirs; rod's Browser.Close would shut it); on
// the aja profile it quits Chrome gracefully, so cookies are saved and the
// profile is free for the next run. Never launcher.Cleanup on a real profile: it
// deletes the user-data directory.
func (r *rodBrowser) Close() error {
	if r.quit {
		// Its own context: by now Ctrl-C may have cancelled the one Chrome was opened with.
		err := r.b.Context(context.Background()).Close()
		pid := r.launcher.PID()
		if !exited(pid, quitWait) {
			_ = syscall.Kill(pid, syscall.SIGTERM) // Chrome still shuts down cleanly on SIGTERM
			if !exited(pid, 5*time.Second) {
				r.launcher.Kill()
			}
		}
		r.logger.Info("closed chrome on the aja profile")
		return err
	}
	if r.testBrowser {
		err := r.b.Close()
		r.launcher.Kill()
		r.launcher.Cleanup()
		return err
	}
	r.logger.Info("detached from chrome; the window stays open")
	return nil
}

type rodPage struct {
	p      *rod.Page
	smooth bool
}

func (p *rodPage) Navigate(ctx context.Context, url string) error {
	page := p.p.Context(ctx)
	if err := page.Navigate(url); err != nil {
		return err
	}
	return page.WaitLoad()
}

func (p *rodPage) URL() (string, error) {
	info, err := p.p.Info()
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

func (p *rodPage) Element(xpath string) (Element, error) {
	els, err := p.p.ElementsX(xpath)
	return first(els, err, xpath, p.smooth)
}

// first turns an ElementsX result into one element or ErrNotFound.
func first(els rod.Elements, err error, xpath string, smooth bool) (Element, error) {
	if err != nil {
		return nil, err
	}
	if len(els) == 0 {
		return nil, &ErrNotFound{XPath: xpath}
	}
	return &rodElement{el: els[0], smooth: smooth}, nil
}

func (p *rodPage) Elements(xpath string) ([]Element, error) {
	els, err := p.p.ElementsX(xpath)
	if err != nil {
		return nil, err
	}
	return wrap(els, p.smooth), nil
}

func (p *rodPage) WaitElement(ctx context.Context, xpath string, timeout time.Duration) (Element, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	page := p.p.Context(ctx)
	for {
		els, err := page.ElementsX(xpath)
		if err == nil {
			for _, el := range els {
				if v, _ := el.Visible(); v {
					return &rodElement{el: el.Context(context.Background()), smooth: p.smooth}, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, &ErrNotFound{XPath: xpath}
			}
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (p *rodPage) Screenshot(path string) error {
	data, err := p.p.Screenshot(false, nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (p *rodPage) Press(key string) error {
	k, ok := keys[key]
	if !ok {
		return fmt.Errorf("unknown key %q", key)
	}
	return p.p.Keyboard.Type(k)
}

var keys = map[string]input.Key{
	"Escape": input.Escape, "Enter": input.Enter, "ArrowDown": input.ArrowDown, "Tab": input.Tab,
}

func (p *rodPage) Close() error { return p.p.Close() }

func wrap(els rod.Elements, smooth bool) []Element {
	out := make([]Element, len(els))
	for i, el := range els {
		out[i] = &rodElement{el: el, smooth: smooth}
	}
	return out
}

type rodElement struct {
	el     *rod.Element
	smooth bool
}

func (e *rodElement) Text() (string, error) { return e.el.Text() }

func (e *rodElement) Attr(name string) (string, bool) {
	v, err := e.el.Attribute(name)
	if err != nil || v == nil {
		return "", false
	}
	return *v, true
}

func (e *rodElement) Property(name string) (string, error) {
	v, err := e.el.Property(name)
	if err != nil {
		return "", err
	}
	if v.Nil() {
		return "", nil
	}
	if b, ok := v.Val().(bool); ok {
		if b {
			return "true", nil
		}
		return "false", nil
	}
	return v.String(), nil
}

func (e *rodElement) Click() error {
	_ = e.ScrollIntoView()
	return e.el.Click(proto.InputMouseButtonLeft, 1)
}

func (e *rodElement) Input(text string) error { return e.el.Input(text) }

func (e *rodElement) Clear() error {
	if err := e.el.SelectAllText(); err != nil {
		return err
	}
	return e.el.Input("")
}

func (e *rodElement) Focus() error { return e.el.Focus() }

// ScrollIntoView brings the element to the middle of the viewport, smoothly when
// settings.smooth_scroll is on (the Python bot's scroll_to_view), and waits for a
// smooth scroll to settle so the next click lands where it should. Click scrolls
// through it too.
func (e *rodElement) ScrollIntoView() error {
	if _, err := e.el.Eval(scrollScript(e.smooth)); err != nil {
		return err
	}
	if e.smooth {
		e.waitScrollSettle()
	}
	return nil
}

// smoothScrollLimit bounds the wait for a smooth scroll (a long one takes about a second).
const smoothScrollLimit = 3 * time.Second

// waitScrollSettle returns once the element has stopped moving: two readings of
// its position 50 ms apart agree. Watching the element, not the window, also
// covers a scroll inside a container (LinkedIn's job list), and ends at the
// limit or when the page's context ends.
func (e *rodElement) waitScrollSettle() {
	ctx := e.el.GetContext()
	deadline := time.Now().Add(smoothScrollLimit)
	last := ""
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
		v, err := e.el.Eval(`() => { const r = this.getBoundingClientRect(); return r.top + "," + r.left }`)
		if err != nil {
			return
		}
		pos := v.Value.String()
		if pos == last {
			return
		}
		last = pos
	}
}

// scrollScript is the JavaScript that scrolls an element (this) into the middle
// of the viewport.
func scrollScript(smooth bool) string {
	behavior := "instant"
	if smooth {
		behavior = "smooth"
	}
	return `() => this.scrollIntoView({block: "center", behavior: "` + behavior + `"})`
}

func (e *rodElement) Visible() bool {
	v, err := e.el.Visible()
	return err == nil && v
}

func (e *rodElement) Element(xpath string) (Element, error) {
	els, err := e.el.ElementsX(xpath)
	return first(els, err, xpath, e.smooth)
}

func (e *rodElement) Elements(xpath string) ([]Element, error) {
	els, err := e.el.ElementsX(xpath)
	if err != nil {
		return nil, err
	}
	return wrap(els, e.smooth), nil
}

func (e *rodElement) SelectOption(text string) error {
	return e.el.Select([]string{text}, true, rod.SelectorTypeText)
}

func (e *rodElement) SetFiles(paths []string) error { return e.el.SetFiles(paths) }
