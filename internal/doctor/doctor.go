// Package doctor checks that a run can succeed before any browser work: Chrome,
// the profile, the config, the enabled secrets, the resume files and the data
// directory. It never changes anything except creating the data directories.
package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/ai"
	"github.com/luizfreitas27/ajai/internal/chrome"
	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/markets"
	"github.com/luizfreitas27/ajai/internal/paths"
)

// Status of one check.
type Status int

// Statuses, in order of severity.
const (
	OK Status = iota
	Warn
	Fail
)

// Check names, for code that acts on a check (the TUI's setup checklist).
const (
	CheckConfig        = "config"
	CheckChrome        = "chrome"
	CheckChromeProfile = "chrome profile"
	CheckAI            = "ai"
	CheckTelegram      = "telegram"
	CheckResume        = "resume" // and "resume brasil", "resume intl": IsResume
	CheckDataDir       = "data dir"
)

// IsResume reports whether name is one of the resume checks.
func IsResume(name string) bool {
	return name == CheckResume || strings.HasPrefix(name, CheckResume+" ")
}

// Check is one line of the report.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Report is every check plus the paths in use.
type Report struct {
	Checks []Check
	Paths  paths.Paths
}

// Failed is true when any check failed.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

// Probe lets tests replace the machine-dependent lookups.
type Probe struct {
	FindChrome    func(explicit string) (string, error)
	ChromeVersion func(binary string) string
	// EverydayProfileDir is where the user's own browser keeps its profile, for the hint.
	EverydayProfileDir func(binary string) string
	HasLinkedInSession func(profileDir string) (bool, error)
}

// RealProbe uses this machine.
func RealProbe() Probe {
	return Probe{FindChrome: chrome.Find, ChromeVersion: chrome.Version, EverydayProfileDir: chrome.EverydayProfileDir,
		HasLinkedInSession: func(dir string) (bool, error) { return chrome.HasLinkedInSession(dir, time.Now()) }}
}

// Run performs the checks. loaded may be nil when the config failed to load, in
// which case loadErr explains why and the config-dependent checks are skipped.
func Run(loaded *config.Loaded, loadErr error, p paths.Paths, probe Probe) Report {
	r := Report{Paths: p}
	add := func(name string, st Status, detail string) {
		r.Checks = append(r.Checks, Check{Name: name, Status: st, Detail: detail})
	}

	// Config
	switch {
	case loaded == nil:
		add(CheckConfig, Fail, loadErr.Error())
	case !loaded.FileExisted:
		add(CheckConfig, Fail, "no config file at "+p.ConfigFile+"; run `ajai init`")
	case loadErr != nil:
		add(CheckConfig, Fail, strings.ReplaceAll(loadErr.Error(), "\n", "\n                      "))
	default:
		add(CheckConfig, OK, p.ConfigFile)
	}
	if loaded != nil {
		for _, w := range loaded.Warnings {
			add(CheckConfig, Warn, w)
		}
	}

	var cfg config.Config
	if loaded != nil {
		cfg = loaded.Config
	} else {
		cfg = config.Defaults()
	}

	// Chrome
	binary, err := probe.FindChrome(cfg.Settings.ChromeBinary)
	window := ", window " + cfg.Settings.Window
	if err != nil {
		add(CheckChrome, Fail, err.Error())
	} else if v := probe.ChromeVersion(binary); v != "" {
		add(CheckChrome, OK, v+" ("+binary+")"+window)
	} else {
		add(CheckChrome, Warn, binary+" found but `--version` failed; it may still run"+window)
	}

	// Profile, and whether LinkedIn is signed in there
	profile := p.ChromeProfile(cfg.Settings.ChromeProfileDir)
	label, hint := profile.Dir, "run `ajai login` to sign in once"
	if profile.Own {
		label += " (the ajai profile)"
		hint += "; or set settings.chrome_profile_dir to your everyday Chrome profile (" + probe.EverydayProfileDir(binary) + ") to use that"
	}
	if st, err := os.Stat(profile.Dir); err != nil || !st.IsDir() {
		add(CheckChromeProfile, Warn, label+" does not exist yet; "+hint)
	} else if signedIn, err := probe.HasLinkedInSession(profile.Dir); err != nil {
		add(CheckChromeProfile, Warn, label+": could not check for a LinkedIn session ("+err.Error()+")")
	} else if !signedIn {
		add(CheckChromeProfile, Warn, label+" has no LinkedIn session; "+hint)
	} else {
		add(CheckChromeProfile, OK, label+", signed in to LinkedIn")
	}

	if loaded != nil && loaded.FileExisted {
		// Secrets the config relies on
		sec := loaded.Secrets
		switch _, err := ai.FromConfig(cfg, sec, markets.Resolve(cfg), nil); {
		case !cfg.AI.Enabled:
			add(CheckAI, OK, "off (ai.enabled: false)")
		case errors.Is(err, ai.ErrNoKey):
			add(CheckAI, Fail, "ai.enabled is true but "+config.EnvAIAPIKey+" is not set in the environment or "+p.EnvFile)
		case err != nil:
			add(CheckAI, Fail, err.Error())
		case sec.AIAPIKey == "":
			add(CheckAI, OK, fmt.Sprintf("%s %s at %s, no key sent", cfg.AI.Provider, cfg.AI.Model, cfg.AIBaseURL()))
		default:
			add(CheckAI, OK, fmt.Sprintf("%s %s, key present", cfg.AI.Provider, cfg.AI.Model))
		}
		switch {
		case sec.TelegramBotToken == "" && sec.TelegramChatID == "":
			add(CheckTelegram, OK, "off (set "+config.EnvTelegramBotToken+" and "+config.EnvTelegramChatID+" to turn on)")
		case sec.TelegramBotToken == "" || sec.TelegramChatID == "":
			add(CheckTelegram, Warn, "only one of "+config.EnvTelegramBotToken+" / "+config.EnvTelegramChatID+" is set; notifications stay off")
		default:
			add(CheckTelegram, OK, "on (token and chat id set)")
		}

		// Resumes
		checkResume := func(label, path string) {
			if path == "" {
				return
			}
			full := ResolveResume(p, path)
			if st, err := os.Stat(full); err != nil || st.IsDir() {
				add(label, Fail, full+" not found")
			} else {
				add(label, OK, full)
			}
		}
		checkResume(CheckResume, cfg.Questions.Resume)
		checkResume(CheckResume+" brasil", cfg.Markets.Brasil.Resume)
		checkResume(CheckResume+" intl", cfg.Markets.Internacional.Resume)
	}

	// Data directory
	if err := p.EnsureDataDirs(); err != nil {
		add(CheckDataDir, Fail, err.Error())
	} else if f, err := os.CreateTemp(p.DataDir, ".doctor-*"); err != nil {
		add(CheckDataDir, Fail, p.DataDir+" is not writable: "+err.Error())
	} else {
		_ = f.Close()
		_ = os.Remove(f.Name())
		add(CheckDataDir, OK, p.DataDir)
	}
	return r
}

// ResolveResume makes a resume path absolute (see paths.Paths.ResumePath).
func ResolveResume(p paths.Paths, path string) string { return p.ResumePath(path) }
