// Package doctor checks that a run can succeed before any browser work: Chrome,
// the profile, the config, the enabled secrets, the resume files and the data
// directory. It never changes anything except creating the data directories.
package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/luizfreitas27/aja/internal/ai"
	"github.com/luizfreitas27/aja/internal/chrome"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/paths"
)

// Status of one check.
type Status int

// Statuses, in order of severity.
const (
	OK Status = iota
	Warn
	Fail
)

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
	FindChrome        func(explicit string) (string, error)
	ChromeVersion     func(binary string) string
	DefaultProfileDir func() string
}

// RealProbe uses this machine.
func RealProbe() Probe {
	return Probe{FindChrome: chrome.Find, ChromeVersion: chrome.Version, DefaultProfileDir: chrome.DefaultProfileDir}
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
		add("config", Fail, loadErr.Error())
	case !loaded.FileExisted:
		add("config", Fail, "no config file at "+p.ConfigFile+"; run `aja init`")
	case loadErr != nil:
		add("config", Fail, strings.ReplaceAll(loadErr.Error(), "\n", "\n                      "))
	default:
		add("config", OK, p.ConfigFile)
	}
	if loaded != nil {
		for _, w := range loaded.Warnings {
			add("config", Warn, w)
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
	if err != nil {
		add("chrome", Fail, err.Error())
	} else if v := probe.ChromeVersion(binary); v != "" {
		add("chrome", OK, v+" ("+binary+")")
	} else {
		add("chrome", Warn, binary+" found but `--version` failed; it may still run")
	}

	// Profile
	profile := cfg.Settings.ChromeProfileDir
	if profile == "" {
		profile = probe.DefaultProfileDir()
	}
	if st, err := os.Stat(profile); err == nil && st.IsDir() {
		add("chrome profile", OK, profile)
	} else {
		add("chrome profile", Warn, profile+" does not exist yet; Chrome will create an empty profile and you will log in to LinkedIn once")
	}

	if loaded != nil && loaded.FileExisted {
		// Secrets the config relies on
		sec := loaded.Secrets
		switch _, err := ai.FromConfig(cfg, sec, markets.Resolve(cfg), nil); {
		case !cfg.AI.Enabled:
			add("ai", OK, "off (ai.enabled: false)")
		case errors.Is(err, ai.ErrNoKey):
			add("ai", Fail, "ai.enabled is true but "+config.EnvAIAPIKey+" is not set in the environment or "+p.EnvFile)
		case err != nil:
			add("ai", Fail, err.Error())
		case sec.AIAPIKey == "":
			add("ai", OK, fmt.Sprintf("%s %s at %s, no key sent", cfg.AI.Provider, cfg.AI.Model, cfg.AIBaseURL()))
		default:
			add("ai", OK, fmt.Sprintf("%s %s, key present", cfg.AI.Provider, cfg.AI.Model))
		}
		switch {
		case sec.TelegramBotToken == "" && sec.TelegramChatID == "":
			add("telegram", OK, "off (set "+config.EnvTelegramBotToken+" and "+config.EnvTelegramChatID+" to turn on)")
		case sec.TelegramBotToken == "" || sec.TelegramChatID == "":
			add("telegram", Warn, "only one of "+config.EnvTelegramBotToken+" / "+config.EnvTelegramChatID+" is set; notifications stay off")
		default:
			add("telegram", OK, "on (token and chat id set)")
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
		checkResume("resume", cfg.Questions.Resume)
		checkResume("resume brasil", cfg.Markets.Brasil.Resume)
		checkResume("resume intl", cfg.Markets.Internacional.Resume)
	}

	// Data directory
	if err := p.EnsureDataDirs(); err != nil {
		add("data dir", Fail, err.Error())
	} else if f, err := os.CreateTemp(p.DataDir, ".doctor-*"); err != nil {
		add("data dir", Fail, p.DataDir+" is not writable: "+err.Error())
	} else {
		_ = f.Close()
		_ = os.Remove(f.Name())
		add("data dir", OK, p.DataDir)
	}
	return r
}

// ResolveResume makes a resume path absolute (see paths.Paths.ResumePath).
func ResolveResume(p paths.Paths, path string) string { return p.ResumePath(path) }
