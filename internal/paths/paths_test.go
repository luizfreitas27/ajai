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

// wide makes dirs as an install from before left them: 0755.
func wide(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(d, 0o755)
	}
}

func TestEnsureDataDirsNarrowsWhatAjaiOwnsAndLeavesWhatTheUserChose(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(base string) (Paths, map[string]os.FileMode) // the paths, and the modes wanted after
	}{
		{"the OS conventions' directories, from before", func(base string) (Paths, map[string]os.FileMode) {
			config, data := filepath.Join(base, "config", "ajai"), filepath.Join(base, "share", "ajai")
			wide(t, config, data, filepath.Join(data, "logs"))
			p := Paths{ConfigFile: filepath.Join(config, "config.yaml"), DataDir: data, LogDir: filepath.Join(data, "logs"),
				ScreenshotDir: filepath.Join(data, "screenshots"), ResumeDir: filepath.Join(data, "resumes"), ownDataDir: true, ownConfigDir: true}
			return p, map[string]os.FileMode{config: 0o700, data: 0o700, p.LogDir: 0o700, p.ScreenshotDir: 0o700, p.ResumeDir: 0o700}
		}},
		{"AJAI_HOME, a directory of the user's", func(base string) (Paths, map[string]os.FileMode) {
			home := filepath.Join(base, "home")
			wide(t, home, filepath.Join(home, "logs"))
			p := Resolve("", env(map[string]string{"AJAI_HOME": home}))
			return p, map[string]os.FileMode{home: 0o755, p.LogDir: 0o700, p.ScreenshotDir: 0o700, p.ResumeDir: 0o700}
		}},
		{"AJAI_HOME not there yet", func(base string) (Paths, map[string]os.FileMode) {
			p := Resolve("", env(map[string]string{"AJAI_HOME": filepath.Join(base, "new")}))
			return p, map[string]os.FileMode{p.DataDir: 0o700}
		}},
		{"a directory --config points into", func(base string) (Paths, map[string]os.FileMode) {
			mine := filepath.Join(base, "dotfiles")
			wide(t, mine)
			p := Resolve(filepath.Join(mine, "ajai.yaml"), env(map[string]string{"AJAI_HOME": filepath.Join(base, "ajai")}))
			return p, map[string]os.FileMode{mine: 0o755}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, want := tc.setup(t.TempDir())
			if err := p.EnsureDataDirs(); err != nil {
				t.Fatal(err)
			}
			for dir, m := range want {
				if got := mode(t, dir); got != m {
					t.Errorf("%s is %o, want %o", dir, got, m)
				}
			}
		})
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

func TestAFileWhereADirectoryShouldBeIsAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "logs")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(file); err == nil {
		t.Error("a file passed for a directory")
	}
	if m := mode(t, file); m != 0o644 {
		t.Errorf("the file's mode was changed to %o", m)
	}
}
