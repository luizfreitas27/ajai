package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func TestAJAHomePutsEverythingUnderOneDirectory(t *testing.T) {
	p := Resolve("", env(map[string]string{"AJA_HOME": "/tmp/x"}))
	for _, pair := range p.All() {
		if !strings.HasPrefix(pair[1], "/tmp/x/") && pair[1] != "/tmp/x" {
			t.Errorf("%s = %s, want under /tmp/x", pair[0], pair[1])
		}
	}
	if p.EnvFile != "/tmp/x/.env" || p.DBFile != "/tmp/x/aja.db" {
		t.Errorf("env %s db %s", p.EnvFile, p.DBFile)
	}
}

func TestConfigFlagMovesOnlyTheConfigAndItsEnv(t *testing.T) {
	p := Resolve("/etc/aja/prod.yaml", env(map[string]string{"AJA_HOME": "/tmp/x"}))
	if p.ConfigFile != "/etc/aja/prod.yaml" || p.EnvFile != "/etc/aja/.env" {
		t.Errorf("config %s env %s", p.ConfigFile, p.EnvFile)
	}
	if p.DataDir != "/tmp/x" {
		t.Errorf("data dir moved to %s", p.DataDir)
	}
}

func TestDefaultsFollowTheOSConventions(t *testing.T) {
	p := Resolve("", env(nil))
	if filepath.Base(filepath.Dir(p.ConfigFile)) != "aja" || filepath.Base(p.DataDir) != "aja" {
		t.Errorf("config %s data %s", p.ConfigFile, p.DataDir)
	}
}

func TestTheChromeProfileIsAjasOwnUnlessTheSettingNamesAnother(t *testing.T) {
	p := Resolve("", func(k string) string {
		if k == "AJA_HOME" {
			return "/data/aja"
		}
		return ""
	})
	cases := []struct {
		setting string
		dir     string
		own     bool
	}{
		{"", "/data/aja/chrome-profile", true},
		{"/home/u/.config/google-chrome", "/home/u/.config/google-chrome", false},
		{"/data/aja/chrome-profile/", "/data/aja/chrome-profile/", true}, // aja's own, named explicitly
	}
	for _, tc := range cases {
		if dir, own := p.ChromeProfile(tc.setting); dir != tc.dir || own != tc.own {
			t.Errorf("%q: %q %v, want %q %v", tc.setting, dir, own, tc.dir, tc.own)
		}
	}
}
