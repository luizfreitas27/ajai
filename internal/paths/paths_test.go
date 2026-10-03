package paths

import (
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
