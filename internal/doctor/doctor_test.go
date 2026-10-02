package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/paths"
)

func setup(t *testing.T) (paths.Paths, *config.Loaded) {
	t.Helper()
	home := t.TempDir()
	p := paths.Resolve("", func(k string) string {
		if k == "AJA_HOME" {
			return home
		}
		return ""
	})
	cfg := config.Defaults()
	cfg.Personals.FirstName, cfg.Personals.LastName = "A", "B"
	cfg.Search.Terms = []string{"x"}
	cfg.Settings.ChromeProfileDir = filepath.Join(home, "profile")
	return p, &config.Loaded{Config: cfg, Paths: p, FileExisted: true}
}

func probe(found bool) Probe {
	return Probe{
		FindChrome: func(explicit string) (string, error) {
			if explicit != "" {
				return explicit, nil
			}
			if found {
				return "/usr/bin/google-chrome", nil
			}
			return "", errors.New("Chrome was not found")
		},
		ChromeVersion:      func(string) string { return "Google Chrome 142.0" },
		EverydayProfileDir: func(string) string { return "/home/u/.config/google-chrome" },
		HasLinkedInSession: func(string) (bool, error) { return true, nil },
	}
}

func TestTheProfileCheckSaysWhichProfileAndWhetherLinkedInIsSignedIn(t *testing.T) {
	p, l := setup(t)
	l.Config.Settings.ChromeProfileDir = "" // the aja profile
	cases := []struct {
		name     string
		create   bool
		signedIn bool
		err      error
		status   Status
		want     []string
	}{
		{"not created", false, false, nil, Warn, []string{"(the aja profile) does not exist yet", "aja login", "/home/u/.config/google-chrome"}},
		{"no session", true, false, nil, Warn, []string{"has no LinkedIn session", "aja login"}},
		{"unreadable", true, false, errors.New("locked"), Warn, []string{"could not check", "locked"}},
		{"signed in", true, true, nil, OK, []string{"(the aja profile), signed in to LinkedIn"}},
	}
	for _, tc := range cases {
		_ = os.RemoveAll(p.ChromeProfileDir)
		if tc.create {
			if err := os.MkdirAll(p.ChromeProfileDir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		pr := probe(true)
		pr.HasLinkedInSession = func(dir string) (bool, error) {
			if dir != p.ChromeProfileDir {
				t.Errorf("checked %s", dir)
			}
			return tc.signedIn, tc.err
		}
		c := byName(Run(l, nil, p, pr), "chrome profile")
		if c.Status != tc.status || !strings.HasPrefix(c.Detail, p.ChromeProfileDir) {
			t.Errorf("%s: %+v", tc.name, c)
		}
		for _, w := range tc.want {
			if !strings.Contains(c.Detail, w) {
				t.Errorf("%s: %q lacks %q", tc.name, c.Detail, w)
			}
		}
	}
	// The user's own profile: no "the aja profile", no hint to switch.
	l.Config.Settings.ChromeProfileDir = t.TempDir()
	pr := probe(true)
	pr.HasLinkedInSession = func(string) (bool, error) { return false, nil }
	if c := byName(Run(l, nil, p, pr), "chrome profile"); strings.Contains(c.Detail, "the aja profile") || strings.Contains(c.Detail, "everyday") || !strings.Contains(c.Detail, "aja login") {
		t.Errorf("%+v", c)
	}
}

func byName(r Report, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{Name: name, Status: -1}
}

func TestMissingChromeAndMissingResumeFail(t *testing.T) {
	p, l := setup(t)
	r := Run(l, nil, p, probe(false))
	if byName(r, "chrome").Status != Fail || byName(r, "resume").Status != Fail || !r.Failed() {
		t.Errorf("%+v", r.Checks)
	}
	if byName(r, "chrome profile").Status != Warn {
		t.Errorf("a missing profile is a warning, got %+v", byName(r, "chrome profile"))
	}
}

func TestEverythingPresentPasses(t *testing.T) {
	p, l := setup(t)
	for _, dir := range []string{l.Config.Settings.ChromeProfileDir, p.ResumeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p.ResumeDir, "resume.pdf"), []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Run(l, nil, p, probe(true))
	if r.Failed() {
		t.Errorf("%+v", r.Checks)
	}
	if c := byName(r, "chrome"); !strings.Contains(c.Detail, "142.0") {
		t.Errorf("version not reported: %+v", c)
	}
	if byName(r, "ai").Status != OK || byName(r, "telegram").Status != OK {
		t.Errorf("off features should be OK: %+v", r.Checks)
	}
}

func TestEnabledFeaturesNeedTheirSecrets(t *testing.T) {
	p, l := setup(t)
	l.Config.AI.Enabled = true
	l.Secrets.TelegramBotToken = "tok"
	r := Run(l, nil, p, probe(true))
	if c := byName(r, "ai"); c.Status != Fail || !strings.Contains(c.Detail, config.EnvAIAPIKey) {
		t.Errorf("ai: %+v", c)
	}
	if c := byName(r, "telegram"); c.Status != Warn {
		t.Errorf("half telegram: %+v", c)
	}
	l.Config.AI.Provider = "ollama"
	l.Secrets.TelegramChatID = "1"
	r = Run(l, nil, p, probe(true))
	if byName(r, "ai").Status != OK || byName(r, "telegram").Status != OK {
		t.Errorf("%+v", r.Checks)
	}
}

func TestInvalidConfigIsReportedAndOtherChecksStillRun(t *testing.T) {
	p, l := setup(t)
	verr := &config.ValidationError{Errors: []*config.FieldError{{Key: "search.sort_by", Problem: "bad"}}}
	r := Run(l, verr, p, probe(true))
	if c := byName(r, "config"); c.Status != Fail || !strings.Contains(c.Detail, "search.sort_by") {
		t.Errorf("%+v", c)
	}
	if byName(r, "chrome").Status != OK {
		t.Error("chrome check should still run")
	}
}
