package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/run"
)

// loginPage is a tab whose URL is LinkedIn's sign-in page until signedInAfter
// URL reads, then the feed (0: never signs in).
type loginPage struct {
	browser.Page
	mu            sync.Mutex
	reads         int
	signedInAfter int
}

func (p *loginPage) Navigate(context.Context, string) error { return nil }

func (p *loginPage) URL() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	if p.signedInAfter > 0 && p.reads >= p.signedInAfter {
		return linkedin.FeedURL, nil
	}
	return "https://www.linkedin.com/login", nil
}

type loginBrowser struct {
	browser.Browser
	page   *loginPage
	closed bool
}

func (b *loginBrowser) Main() browser.Page { return b.page }
func (b *loginBrowser) Close() error       { b.closed = true; return nil }

// fakeChrome replaces OpenBrowser for one test and records what it was asked.
func fakeChrome(t *testing.T, br *loginBrowser, err error) *browser.Options {
	t.Helper()
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	var got browser.Options
	OpenBrowser = func(ctx context.Context, o browser.Options) (browser.Browser, error) {
		got = o
		if err != nil {
			return nil, err
		}
		return br, nil
	}
	return &got
}

func TestLoginWaitsForTheFeedInAjasOwnProfileAndClosesChrome(t *testing.T) {
	h := newHarness(t) // no config yet: login comes before init just as well
	br := &loginBrowser{page: &loginPage{signedInAfter: 2}}
	opts := fakeChrome(t, br, nil)
	code, out, errText := h.run("login", "--timeout", "200ms")
	if code != 0 || !strings.Contains(out, "LinkedIn session saved in "+filepath.Join(h.home, "chrome-profile")) || !strings.Contains(out, "Sign in to LinkedIn") {
		t.Fatalf("%d %s %s", code, out, errText)
	}
	if opts.ProfileDir != filepath.Join(h.home, "chrome-profile") || !opts.QuitOnClose {
		t.Errorf("aja's own profile, closed afterwards: %+v", *opts)
	}
	if st, err := os.Stat(opts.ProfileDir); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Errorf("the profile is created, private: %v %v", st, err)
	}
	if !br.closed {
		t.Error("Chrome must be closed after signing in")
	}

	br = &loginBrowser{page: &loginPage{signedInAfter: 1}}
	fakeChrome(t, br, nil)
	if code, out, _ := h.run("login"); code != 0 || !strings.Contains(out, "already signed in") || strings.Contains(out, "Sign in to LinkedIn") {
		t.Errorf("%d %s", code, out)
	}
}

func TestLoginGivesUpAfterTheTimeoutWithAHint(t *testing.T) {
	h := newHarness(t)
	fakeChrome(t, &loginBrowser{page: &loginPage{}}, nil)
	start := time.Now()
	code, _, errText := h.run("login", "--timeout", "50ms")
	if code == 0 || !strings.Contains(errText, "no LinkedIn sign-in within 50ms; run `aja login` again") {
		t.Errorf("%d %s", code, errText)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("a short timeout must not wait for a long poll")
	}
}

func TestLoginRefusesWhileTheProfileOrARunIsBusy(t *testing.T) {
	h := newHarness(t)
	fakeChrome(t, nil, browser.ErrProfileInUse)
	if code, _, errText := h.run("login"); code == 0 || !strings.Contains(errText, "aja's Chrome profile is in use by another window") {
		t.Errorf("%d %s", code, errText)
	}

	lock, err := run.AcquireLock(h.home, run.OriginSchedule, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	opts := fakeChrome(t, &loginBrowser{page: &loginPage{signedInAfter: 1}}, nil)
	if code, _, errText := h.run("login"); code == 0 || !strings.Contains(errText, "a run started from the schedule") || !strings.Contains(errText, "aja stop") {
		t.Errorf("%d %s", code, errText)
	}
	if opts.ProfileDir != "" {
		t.Error("Chrome must not open while a run uses it")
	}
}

func TestLoginOnTheUsersOwnProfileLeavesChromeOpen(t *testing.T) {
	h := newHarness(t)
	everyday := t.TempDir()
	h.env["AJA_CFG_SETTINGS_CHROME_PROFILE_DIR"] = everyday
	opts := fakeChrome(t, &loginBrowser{page: &loginPage{signedInAfter: 1}}, nil)
	if code, _, errText := h.run("login"); code != 0 {
		t.Fatalf("%d %s", code, errText)
	}
	if opts.ProfileDir != everyday || opts.QuitOnClose {
		t.Errorf("the user's profile: as set, and only detached from: %+v", *opts)
	}
}
