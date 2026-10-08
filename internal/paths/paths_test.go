package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func TestAJAIHomePutsEverythingUnderOneDirectory(t *testing.T) {
	p := Resolve("", env(map[string]string{"AJAI_HOME": "/tmp/x"}))
	for _, pair := range p.All() {
		if !strings.HasPrefix(pair[1], "/tmp/x/") && pair[1] != "/tmp/x" {
			t.Errorf("%s = %s, want under /tmp/x", pair[0], pair[1])
		}
	}
	if p.EnvFile != "/tmp/x/.env" || p.DBFile != "/tmp/x/ajai.db" {
		t.Errorf("env %s db %s", p.EnvFile, p.DBFile)
	}
}

func TestTheOldAJAHomeIsNotRead(t *testing.T) {
	p := Resolve("", env(map[string]string{"AJA_HOME": "/tmp/old"})) // the tool was called aja before its first release
	for _, pair := range p.All() {
		if strings.HasPrefix(pair[1], "/tmp/old") {
			t.Errorf("%s = %s: AJA_HOME moved it", pair[0], pair[1])
		}
	}
}

func TestConfigFlagMovesOnlyTheConfigAndItsEnv(t *testing.T) {
	p := Resolve("/etc/ajai/prod.yaml", env(map[string]string{"AJAI_HOME": "/tmp/x"}))
	if p.ConfigFile != "/etc/ajai/prod.yaml" || p.EnvFile != "/etc/ajai/.env" {
		t.Errorf("config %s env %s", p.ConfigFile, p.EnvFile)
	}
	if p.DataDir != "/tmp/x" {
		t.Errorf("data dir moved to %s", p.DataDir)
	}
}

func TestDefaultsFollowTheOSConventions(t *testing.T) {
	p := Resolve("", env(nil))
	if filepath.Base(filepath.Dir(p.ConfigFile)) != "ajai" || filepath.Base(p.DataDir) != "ajai" {
		t.Errorf("config %s data %s", p.ConfigFile, p.DataDir)
	}
}

func TestTheChromeProfileIsTheAjaiProfileUnlessTheSettingNamesAnother(t *testing.T) {
	p := Resolve("", func(k string) string {
		if k == "AJAI_HOME" {
			return "/data/ajai"
		}
		return ""
	})
	cases := []struct {
		setting string
		dir     string
		own     bool
	}{
		{"", "/data/ajai/chrome-profile", true},
		{"/home/u/.config/google-chrome", "/home/u/.config/google-chrome", false},
		{"/data/ajai/chrome-profile/", "/data/ajai/chrome-profile/", true}, // the ajai profile, named explicitly
	}
	for _, tc := range cases {
		if got := p.ChromeProfile(tc.setting); got.Dir != tc.dir || got.Own != tc.own {
			t.Errorf("%q: %+v, want %q %v", tc.setting, got, tc.dir, tc.own)
		}
	}
}

// mode is a path's permission bits.
func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestAjaisDirectoriesAreTheUsersAlone(t *testing.T) {
	home := filepath.Join(t.TempDir(), "ajai")
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil { // an install from before: wide open
		t.Fatal(err)
	}
	_ = os.Chmod(home, 0o755)
	p := Resolve("", env(map[string]string{"AJAI_HOME": home}))
	if err := p.EnsureDataDirs(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.DataDir, p.LogDir, p.ScreenshotDir, p.ResumeDir} {
		if m := mode(t, dir); m != PrivateDirMode {
			t.Errorf("%s is %o, want %o", dir, m, PrivateDirMode)
		}
	}
}

func TestADirectoryConfigPointsIntoIsLeftAsItIs(t *testing.T) {
	mine := filepath.Join(t.TempDir(), "dotfiles")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Resolve(filepath.Join(mine, "ajai.yaml"), env(map[string]string{"AJAI_HOME": filepath.Join(t.TempDir(), "ajai")}))
	if err := p.EnsureDataDirs(); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, mine); m != 0o755 {
		t.Errorf("the --config directory was changed to %o", m)
	}
}

func TestEnsurePrivateDirCreatesItPrivateAndItsParentsAsUsual(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "share")
	dir := filepath.Join(parent, "ajai")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, dir); m != PrivateDirMode {
		t.Errorf("dir %o", m)
	}
	if m := mode(t, parent); m&0o055 == 0 {
		t.Errorf("a parent ajai does not own was made private: %o", m)
	}
}

func TestAjaisOwnConfigDirectoryIsNarrowedToo(t *testing.T) {
	base := t.TempDir()
	config := filepath.Join(base, "config", "ajai")
	if err := os.MkdirAll(config, 0o755); err != nil { // as an install from before left it
		t.Fatal(err)
	}
	data := filepath.Join(base, "share", "ajai")
	p := Paths{ConfigFile: filepath.Join(config, "config.yaml"), DataDir: data, LogDir: filepath.Join(data, "logs"),
		ScreenshotDir: filepath.Join(data, "screenshots"), ResumeDir: filepath.Join(data, "resumes"), ownConfigDir: true}
	if err := p.EnsureDataDirs(); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, config); m != PrivateDirMode {
		t.Errorf("ajai's config directory is %o", m)
	}
}
