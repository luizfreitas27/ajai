// Package paths resolves where ajai keeps its files: the YAML config, the .env
// with secrets, and the data directory (database, logs, screenshots, resumes).
//
// Resolution order: the --config flag relocates the config file alone (the
// .env stays beside it); AJAI_HOME puts everything under one directory; otherwise
// the OS conventions apply (XDG on Linux, Application Support on macOS,
// AppData on Windows).
//
// Every directory ajai owns is the user's alone (PrivateDirMode), and so are
// the files it keeps there (PrivateFileMode): an install from before is
// narrowed on its next start. A directory the user chose (AJAI_HOME itself,
// the one --config points into) is created private when missing but never
// narrowed.
package paths

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/adrg/xdg"
)

// Paths is every location ajai reads or writes.
type Paths struct {
	// Home is set only when AJAI_HOME was used; informational.
	Home          string
	ConfigFile    string
	EnvFile       string
	DataDir       string
	DBFile        string
	LogDir        string
	ScreenshotDir string
	ResumeDir     string
	// ChromeProfileDir is the ajai profile, used when settings.chrome_profile_dir is blank.
	ChromeProfileDir string
	// ownDataDir and ownConfigDir: the data and the config file's directory are
	// ajai's own (the OS conventions' ajai directories, not AJAI_HOME nor one
	// --config points into), so ajai narrows them to PrivateDirMode.
	ownDataDir, ownConfigDir bool
}

// HomeEnv is the variable that puts every file under one directory.
const HomeEnv = "AJAI_HOME"

// name is the tool's name as its folders and its database carry it.
const name = "ajai"

// Getenv is the environment lookup Resolve uses; os.Getenv in production.
type Getenv func(string) string

// Resolve computes the paths for this process. configFlag is the value of
// --config ("" when absent).
func Resolve(configFlag string, getenv Getenv) Paths {
	var p Paths
	if home := getenv(HomeEnv); home != "" {
		p.Home = home
		p.ConfigFile = filepath.Join(home, "config.yaml")
		p.DataDir = home
	} else {
		p.ConfigFile = filepath.Join(xdg.ConfigHome, name, "config.yaml")
		p.DataDir = filepath.Join(xdg.DataHome, name)
	}
	p.ownDataDir = p.Home == ""
	if configFlag != "" {
		p.ConfigFile = configFlag
	}
	p.ownConfigDir = p.Home == "" && configFlag == ""
	p.EnvFile = filepath.Join(filepath.Dir(p.ConfigFile), ".env")
	p.DBFile = filepath.Join(p.DataDir, name+".db")
	p.LogDir = filepath.Join(p.DataDir, "logs")
	p.ScreenshotDir = filepath.Join(p.DataDir, "screenshots")
	p.ResumeDir = filepath.Join(p.DataDir, "resumes")
	p.ChromeProfileDir = filepath.Join(p.DataDir, "chrome-profile")
	return p
}

// Profile is the Chrome profile runs use.
type Profile struct {
	Dir string
	// Own: the ajai profile, which ajai creates and quits Chrome on; any other
	// profile is the user's, and Chrome there is only ever detached from.
	Own bool
}

// ChromeProfile resolves settings.chrome_profile_dir: blank means the ajai profile.
func (p Paths) ChromeProfile(setting string) Profile {
	if setting == "" {
		return Profile{Dir: p.ChromeProfileDir, Own: true}
	}
	return Profile{Dir: setting, Own: filepath.Clean(setting) == filepath.Clean(p.ChromeProfileDir)}
}

// PrivateDirMode and PrivateFileMode are the modes of what ajai owns: the
// history, answers, resumes, logs and config are the user's alone.
const (
	PrivateDirMode  os.FileMode = 0o700
	PrivateFileMode os.FileMode = 0o600
)

// EnsurePrivateDir creates dir when missing (PrivateDirMode; its parents as
// usual, 0755) and narrows an existing one that grants anyone else anything.
func EnsurePrivateDir(dir string) error { return ensureDir(dir, true) }

// CreateDir creates dir when missing, as EnsurePrivateDir does, and leaves an
// existing one as it is: a directory the user chose.
func CreateDir(dir string) error { return ensureDir(dir, false) }

func ensureDir(dir string, narrowExisting bool) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	err := os.Mkdir(dir, PrivateDirMode)
	if err == nil || !os.IsExist(err) {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "mkdir", Path: dir, Err: syscall.ENOTDIR}
	}
	if narrowExisting && info.Mode().Perm()&^PrivateDirMode != 0 {
		// Best effort: a filesystem without modes (vfat, some network mounts) keeps its own.
		_ = os.Chmod(dir, PrivateDirMode)
	}
	return nil
}

// EnsureDataDirs creates the data directories if they are missing and narrows
// the ones ajai owns, its config directory included when it exists: an install
// from before is tightened on its next start.
func (p Paths) EnsureDataDirs() error {
	if err := ensureDir(p.DataDir, p.ownDataDir); err != nil {
		return err
	}
	for _, dir := range []string{p.LogDir, p.ScreenshotDir, p.ResumeDir} {
		if err := EnsurePrivateDir(dir); err != nil {
			return err
		}
	}
	if dir := filepath.Dir(p.ConfigFile); p.ownConfigDir {
		if _, err := os.Stat(dir); err == nil {
			return EnsurePrivateDir(dir)
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
