package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
)

func TestSetDotenvWritesOneVariableAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf", ".env")
	read := func() (string, map[string]string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		m, err := godotenv.Read(path)
		if err != nil {
			t.Fatalf("godotenv cannot read it back: %v\n%s", err, data)
		}
		return string(data), m
	}
	perm := func() os.FileMode {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}

	if err := SetDotenv(path, EnvTelegramChatID, "-100123"); err != nil {
		t.Fatal(err)
	}
	if text, m := read(); text != "AJA_TELEGRAM_CHAT_ID='-100123'\n" || m[EnvTelegramChatID] != "-100123" || perm() != 0o600 {
		t.Errorf("a new file: %q %v %v", text, m, perm())
	}

	existing := "# my secrets\nOTHER=1\nexport AJA_AI_API_KEY=old\nAJA_TELEGRAM_CHAT_ID='-100123'\nAJA_AI_API_KEY=older\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDotenv(path, EnvAIAPIKey, "sk-$HOME#x y"); err != nil {
		t.Fatal(err)
	}
	text, m := read()
	if text != "# my secrets\nOTHER=1\nAJA_AI_API_KEY='sk-$HOME#x y'\nAJA_TELEGRAM_CHAT_ID='-100123'\n" {
		t.Errorf("replaced in place, the duplicate dropped, the rest kept:\n%s", text)
	}
	if m[EnvAIAPIKey] != "sk-$HOME#x y" || m["OTHER"] != "1" {
		t.Errorf("read back literally: %v", m)
	}
	if perm() != 0o600 {
		t.Errorf("readable only by the owner: %v", perm())
	}

	if err := SetDotenv(path, EnvAIAPIKey, ""); err != nil {
		t.Fatal(err)
	}
	if text, _ := read(); text != "# my secrets\nOTHER=1\nAJA_TELEGRAM_CHAT_ID='-100123'\n" {
		t.Errorf("an empty value removes the variable:\n%s", text)
	}

	if err := SetDotenv(path, EnvAIAPIKey, "sk-new"); err != nil {
		t.Fatal(err)
	}
	if text, m := read(); text != "# my secrets\nOTHER=1\nAJA_TELEGRAM_CHAT_ID='-100123'\nAJA_AI_API_KEY='sk-new'\n" || m[EnvAIAPIKey] != "sk-new" {
		t.Errorf("a new variable is appended, the rest kept:\n%s", text)
	}

	// The forms godotenv also reads are recognised, so a removal really removes.
	if err := os.WriteFile(path, []byte("AJA_AI_API_KEY: colon\nexport\tAJA_TELEGRAM_CHAT_ID=tab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{EnvAIAPIKey, EnvTelegramChatID} {
		if err := SetDotenv(path, name, ""); err != nil {
			t.Fatal(err)
		}
	}
	if text, m := read(); text != "" || len(m) != 0 {
		t.Errorf("both forms removed: %q %v", text, m)
	}

	for _, bad := range []string{"it's", "two\nlines", `ends\`} {
		if err := SetDotenv(path, EnvAIAPIKey, bad); err == nil {
			t.Errorf("%q must be refused: godotenv would misread it", bad)
		}
	}
	if err := SetDotenv(path, "NOT A NAME", "x"); err == nil {
		t.Error("a bad name must be refused")
	}
}

func TestEnvOverrideNameIsTheVariableThatOverridesAKey(t *testing.T) {
	for key, want := range map[string]string{
		"search.terms":            "AJA_CFG_SEARCH_TERMS",
		"settings.run_non_stop":   "AJA_CFG_SETTINGS_RUN_NON_STOP",
		"markets.brasil.currency": "AJA_CFG_MARKETS_BRASIL_CURRENCY",
	} {
		got := EnvOverrideName(key)
		if got != want || envKeyToDotted(got) != key {
			t.Errorf("EnvOverrideName(%q) = %q (back: %q), want %q", key, got, envKeyToDotted(got), want)
		}
	}
}
