package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/browser"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/run"
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

func TestLoginWaitsForTheFeedThenClosesChromeOnlyOnTheAjaiProfile(t *testing.T) {
	everyday := t.TempDir()
	cases := []struct {
		name          string
		profile       string // settings.chrome_profile_dir; "" = the ajai profile
		signedInAfter int    // URL reads until the feed shows
		want          string
		prompted      bool // asked to sign in
	}{
		{"signs in on the ajai profile", "", 2, "LinkedIn session saved in ", true},
		{"already signed in", "", 1, "already signed in to LinkedIn in ", false},
		{"the user's own profile", everyday, 1, "already signed in to LinkedIn in " + everyday, false},
	}
	for _, tc := range cases {
		h := newHarness(t) // no config yet: login comes before init just as well
		if tc.profile != "" {
			h.env["AJAI_CFG_SETTINGS_CHROME_PROFILE_DIR"] = tc.profile
		}
		br := &loginBrowser{page: &loginPage{signedInAfter: tc.signedInAfter}}
		opts := fakeChrome(t, br, nil)
		code, out, errText := h.run("login", "--timeout", "200ms")
		if code != 0 || !strings.Contains(out, tc.want) || strings.Contains(out, "Sign in to LinkedIn") != tc.prompted {
			t.Errorf("%s: %d %s %s", tc.name, code, out, errText)
			continue
		}
		ajaiProfile := filepath.Join(h.home, "chrome-profile")
		if tc.profile == "" {
			if opts.ProfileDir != ajaiProfile || !opts.QuitOnClose {
				t.Errorf("%s: the ajai profile, quit afterwards: %+v", tc.name, *opts)
			}
			if st, err := os.Stat(ajaiProfile); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
				t.Errorf("%s: the ajai profile is created, private: %v %v", tc.name, st, err)
			}
		} else if opts.ProfileDir != tc.profile || opts.QuitOnClose {
			t.Errorf("%s: the user's profile, as set, only detached from: %+v", tc.name, *opts)
		}
		if !br.closed {
			t.Errorf("%s: Close must always be called (it quits or detaches)", tc.name)
		}
	}
}

func TestLoginGivesUpAfterTheTimeoutWithAHint(t *testing.T) {
	h := newHarness(t)
	fakeChrome(t, &loginBrowser{page: &loginPage{}}, nil)
	start := time.Now()
	code, _, errText := h.run("login", "--timeout", "50ms")
	if code == 0 || !strings.Contains(errText, "no LinkedIn sign-in within 50ms; run `ajai login` again") {
		t.Errorf("%d %s", code, errText)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("a short timeout must not wait for a long poll")
	}
}

func TestLoginRefusesWhileTheProfileOrARunIsBusy(t *testing.T) {
	h := newHarness(t)
	fakeChrome(t, nil, browser.ErrProfileInUse)
	if code, _, errText := h.run("login"); code == 0 || !strings.Contains(errText, "the ajai profile is in use by another Chrome window") {
		t.Errorf("%d %s", code, errText)
	}

	lock, err := run.AcquireLock(h.home, run.Holder{Origin: run.OriginSchedule, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	opts := fakeChrome(t, &loginBrowser{page: &loginPage{signedInAfter: 1}}, nil)
	if code, _, errText := h.run("login"); code == 0 || !strings.Contains(errText, "a run started from the schedule") || !strings.Contains(errText, "ajai stop") {
		t.Errorf("%d %s", code, errText)
	}
	if opts.ProfileDir != "" {
		t.Error("Chrome must not open while a run uses it")
	}
}
