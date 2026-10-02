package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Options for opening Chrome.
type Options struct {
	Bin               string // Chrome executable; "" lets rod look it up
	ProfileDir        string // user-data directory to attach to
	DisableExtensions bool
	// Headless is honoured ONLY by tests (the browser build tag); the CLI never sets it.
	Headless bool
	Logger   *slog.Logger
}

// ErrProfileInUse means the profile directory is locked by a running Chrome.
var ErrProfileInUse = errors.New("your Chrome profile is in use: close Chrome (every window) and run again, or set settings.chrome_profile_dir to a profile aja can own")

// Open launches Chrome on the given profile and connects to it.
func Open(ctx context.Context, opts Options) (Browser, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	l := launcher.New().
		Headless(opts.Headless).
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
	opts.Logger.Info("chrome attached", "profile", opts.ProfileDir, "headless", opts.Headless)
	return &rodBrowser{b: b, main: &rodPage{p: main}, launcher: l}, nil
}

// profileLocked reports whether another Chrome holds the profile's singleton lock.
func profileLocked(dir string) bool {
	for _, name := range []string{"SingletonLock", "lockfile"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

type rodBrowser struct {
	b        *rod.Browser
	main     *rodPage
	launcher *launcher.Launcher
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
			out = append(out, &rodPage{p: p})
		}
	}
	return out, nil
}

// Close disconnects from Chrome. The user's browser stays open: it is theirs.
func (r *rodBrowser) Close() error { return r.b.Close() }

type rodPage struct{ p *rod.Page }

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
	if err != nil {
		return nil, err
	}
	if len(els) == 0 {
		return nil, &ErrNotFound{XPath: xpath}
	}
	return &rodElement{el: els[0]}, nil
}

func (p *rodPage) Elements(xpath string) ([]Element, error) {
	els, err := p.p.ElementsX(xpath)
	if err != nil {
		return nil, err
	}
	return wrap(els), nil
}

func (p *rodPage) WaitElement(ctx context.Context, xpath string, timeout time.Duration) (Element, error) {
	deadline := time.Now().Add(timeout)
	for {
		els, err := p.p.ElementsX(xpath)
		if err == nil {
			for _, el := range els {
				if v, _ := el.Visible(); v {
					return &rodElement{el: el}, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, &ErrNotFound{XPath: xpath}
		}
		select {
		case <-ctx.Done():
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

func wrap(els rod.Elements) []Element {
	out := make([]Element, len(els))
	for i, el := range els {
		out[i] = &rodElement{el: el}
	}
	return out
}

type rodElement struct{ el *rod.Element }

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
	_ = e.el.ScrollIntoView()
	return e.el.Click(proto.InputMouseButtonLeft, 1)
}

func (e *rodElement) Input(text string) error { return e.el.Input(text) }

func (e *rodElement) Clear() error {
	if err := e.el.SelectAllText(); err != nil {
		return err
	}
	return e.el.Input("")
}

func (e *rodElement) Focus() error          { return e.el.Focus() }
func (e *rodElement) ScrollIntoView() error { return e.el.ScrollIntoView() }

func (e *rodElement) Visible() bool {
	v, err := e.el.Visible()
	return err == nil && v
}

func (e *rodElement) Element(xpath string) (Element, error) {
	els, err := e.el.ElementsX(xpath)
	if err != nil {
		return nil, err
	}
	if len(els) == 0 {
		return nil, &ErrNotFound{XPath: xpath}
	}
	return &rodElement{el: els[0]}, nil
}

func (e *rodElement) Elements(xpath string) ([]Element, error) {
	els, err := e.el.ElementsX(xpath)
	if err != nil {
		return nil, err
	}
	return wrap(els), nil
}

func (e *rodElement) SelectOption(text string) error {
	return e.el.Select([]string{text}, true, rod.SelectorTypeText)
}

func (e *rodElement) SetFiles(paths []string) error { return e.el.SetFiles(paths) }
