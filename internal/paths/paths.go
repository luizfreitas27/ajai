// Package paths resolves where aja keeps its files: the YAML config, the .env
// with secrets, and the data directory (database, logs, screenshots, resumes).
//
// Resolution order: the --config flag relocates the config file alone (the
// .env stays beside it); AJA_HOME puts everything under one directory; otherwise
// the OS conventions apply (XDG on Linux, Application Support on macOS,
// AppData on Windows).
package paths

import (
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// Paths is every location aja reads or writes.
type Paths struct {
	// Home is set only when AJA_HOME was used; informational.
	Home          string
	ConfigFile    string
	EnvFile       string
	DataDir       string
	DBFile        string
	LogDir        string
	ScreenshotDir string
	ResumeDir     string
	// ChromeProfileDir is the aja profile, used when settings.chrome_profile_dir is blank.
	ChromeProfileDir string
}

// Getenv is the environment lookup Resolve uses; os.Getenv in production.
type Getenv func(string) string

// Resolve computes the paths for this process. configFlag is the value of
// --config ("" when absent).
func Resolve(configFlag string, getenv Getenv) Paths {
	var p Paths
	if home := getenv("AJA_HOME"); home != "" {
		p.Home = home
		p.ConfigFile = filepath.Join(home, "config.yaml")
		p.DataDir = home
	} else {
		p.ConfigFile = filepath.Join(xdg.ConfigHome, "aja", "config.yaml")
		p.DataDir = filepath.Join(xdg.DataHome, "aja")
	}
	if configFlag != "" {
		p.ConfigFile = configFlag
	}
	p.EnvFile = filepath.Join(filepath.Dir(p.ConfigFile), ".env")
	p.DBFile = filepath.Join(p.DataDir, "aja.db")
	p.LogDir = filepath.Join(p.DataDir, "logs")
	p.ScreenshotDir = filepath.Join(p.DataDir, "screenshots")
	p.ResumeDir = filepath.Join(p.DataDir, "resumes")
	p.ChromeProfileDir = filepath.Join(p.DataDir, "chrome-profile")
	return p
}

// Profile is the Chrome profile runs use.
type Profile struct {
	Dir string
	// Own: the aja profile, which aja creates and quits Chrome on; any other
	// profile is the user's, and Chrome there is only ever detached from.
	Own bool
}

// ChromeProfile resolves settings.chrome_profile_dir: blank means the aja profile.
func (p Paths) ChromeProfile(setting string) Profile {
	if setting == "" {
		return Profile{Dir: p.ChromeProfileDir, Own: true}
	}
	return Profile{Dir: setting, Own: filepath.Clean(setting) == filepath.Clean(p.ChromeProfileDir)}
}

// EnsureDataDirs creates the data directories if they are missing.
func (p Paths) EnsureDataDirs() error {
	for _, dir := range []string{p.DataDir, p.LogDir, p.ScreenshotDir, p.ResumeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ResumePath makes a resume setting absolute: relative names live in the resumes directory.
func (p Paths) ResumePath(name string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(p.ResumeDir, name)
}

// All lists the paths with labels, in the order doctor prints them.
func (p Paths) All() [][2]string {
	return [][2]string{
		{"config", p.ConfigFile},
		{".env", p.EnvFile},
		{"data", p.DataDir},
		{"database", p.DBFile},
		{"logs", p.LogDir},
		{"screenshots", p.ScreenshotDir},
		{"resumes", p.ResumeDir},
	}
}
