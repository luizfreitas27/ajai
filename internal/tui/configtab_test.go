package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/daemon"
)

// writeConfig writes a valid config file, as `aja init` would.
func (h *harness) writeConfig() {
	h.t.Helper()
	resume := h.p.ResumeDir + "/cv.pdf"
	if err := os.WriteFile(resume, []byte("%PDF"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	cfg := config.Defaults()
	err := cfg.ApplyEssentials(config.Essentials{FirstName: "Ana", LastName: "Silva", Terms: "Dev Python, QA", Location: "Brasil",
		Market: "brasil", DesiredSalary: 9000, Resume: resume})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := config.Write(h.p.ConfigFile, cfg); err != nil {
		h.t.Fatal(err)
	}
}

// fileValue reads key from the config file alone.
func (h *harness) fileValue(key string) string {
	h.t.Helper()
	l, err := config.Load(h.p, config.MapEnv(nil), nil)
	if l == nil {
		h.t.Fatal(err)
	}
	v, err := l.Config.Get(key)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

// moveTo walks the Config list down to the row named name.
func (h *harness) moveTo(name string) {
	h.t.Helper()
	h.press("g")
	for range 200 {
		if strings.Contains(h.screen(), "> "+name+" ") {
			return
		}
		h.press("down")
	}
	h.t.Fatalf("no row %q:\n%s", name, h.screen())
}

func TestTheConfigTabListsTheSecretsAndEverySetting(t *testing.T) {
	h := newHarness(t)
	h.env[config.EnvAIAPIKey] = "sk-from-the-environment"
	h.open(100, 30)
	h.press("2")
	h.shows("2 Config", "There is no config file yet: run `aja init`. The secrets can be set already.",
		"> AJA_AI_API_KEY", "set (your environment)", "AJA_TELEGRAM_BOT_TOKEN", "not set",
		"The AI provider's API key", "Your environment sets it, and wins over .env.")
	if strings.Contains(h.screen(), "sk-from-the-environment") {
		t.Fatal("a secret's value is on screen")
	}
	h.golden("config-missing")

	h.writeConfig()
	h.press("1", "2") // coming back reads the file again
	h.shows("personals", "first_name", "Ana")
	if strings.Contains(h.screen(), "problem") {
		t.Errorf("a valid file:\n%s", h.screen())
	}
	h.moveTo("disability")
	h.shows("Answer to disability questions.", "one of: Yes | No | Decline")
	h.golden("config")
}

func TestASettingIsSavedOnlyOnceValid(t *testing.T) {
	h := newHarness(t)
	h.writeConfig()
	h.open(100, 30)
	h.press("2")

	h.moveTo("terms")
	h.press("enter")
	h.shows("Edit search.terms (comma-separated; empty clears it)", "> Dev Python, QA", "enter saves · esc cancels")
	h.press("ctrl+u", "Go developer, Golang, q1", "enter") // q and 1 are typed, not keys
	h.shows("Saved search.terms = Go developer, Golang, q1.")
	if got := h.fileValue("search.terms"); got != "Go developer, Golang, q1" {
		t.Errorf("the file has %q", got)
	}
	if !strings.Contains(h.logs.String(), `"msg":"config set from the TUI","key":"search.terms"`) {
		t.Errorf("not logged: %s", h.logs)
	}

	h.moveTo("notice_period")
	h.press("enter", "ctrl+u", "-5", "enter")
	h.shows("Refused: questions.notice_period")
	before := h.fileValue("questions.notice_period")
	h.press("esc")
	h.shows("↑↓ move · enter edit")
	if got := h.fileValue("questions.notice_period"); got != before {
		t.Errorf("a refused value changed the file: %q", got)
	}

	h.moveTo("disability")
	h.press("enter")
	h.shows("Edit personals.disability", "Yes", "Decline")
	h.press("down", "down", "enter")
	h.shows("Saved personals.disability = Decline.")
	if got := h.fileValue("personals.disability"); got != "Decline" {
		t.Errorf("%q", got)
	}

	h.moveTo("randomize_order")
	was := h.fileValue("search.randomize_order")
	h.press("enter")
	h.shows("Edit search.randomize_order", "true", "false")
	if was == "false" {
		h.press("up")
	} else {
		h.press("down")
	}
	h.press("enter")
	if got := h.fileValue("search.randomize_order"); got == was {
		t.Errorf("the boolean did not change: %q", got)
	}
}

func TestAnEnvironmentOverrideIsMarkedAndNeverSaved(t *testing.T) {
	h := newHarness(t)
	h.writeConfig()
	h.env["AJA_CFG_SEARCH_TERMS"] = "From the environment"
	h.env["AJA_CFG_SEARCH_LOCATION"] = "Lisboa"
	location := h.fileValue("search.location")
	h.open(100, 30)
	h.press("2")
	h.moveTo("terms")
	h.shows("Dev Python, QA  ← AJA_CFG_SEARCH_TERMS overrides it",
		"AJA_CFG_SEARCH_TERMS in your environment wins over the file; an edit here changes the file.")
	h.press("enter", "ctrl+u", "Rust", "enter")
	if got := h.fileValue("search.terms"); got != "Rust" {
		t.Errorf("the file gets the edit: %q", got)
	}
	if got := h.fileValue("search.location"); got != location {
		t.Errorf("an override was saved into the file: location %q, was %q", got, location)
	}
}

func TestSecretsAreTypedHiddenSavedInDotenvAndNeverShownOrLogged(t *testing.T) {
	h := newHarness(t)
	h.writeConfig()
	d, err := daemon.AcquireLock(h.p.DataDir, h.now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Release()
	h.open(100, 30)
	h.press("2")

	h.moveTo("AJA_TELEGRAM_BOT_TOKEN")
	h.press("enter")
	h.shows("Edit AJA_TELEGRAM_BOT_TOKEN (hidden as you type)")
	h.press("123:SECRET")
	if strings.Contains(h.screen(), "123:SECRET") {
		t.Fatalf("the secret shows while typed:\n%s", h.screen())
	}
	h.shows("••••••••••")
	h.press("enter")
	h.shows("Saved AJA_TELEGRAM_BOT_TOKEN in .env. Restart the daemon to use it: it reads Telegram's settings", // wrapped
		"when it starts.", "set (.env)")
	data, err := os.ReadFile(h.p.EnvFile)
	if err != nil || !strings.Contains(string(data), "AJA_TELEGRAM_BOT_TOKEN='123:SECRET'") {
		t.Errorf(".env: %q %v", data, err)
	}
	if strings.Contains(h.screen(), "123:SECRET") || strings.Contains(h.logs.String(), "SECRET") {
		t.Fatalf("the secret reached the screen or the log:\n%s\n%s", h.screen(), h.logs)
	}

	h.env[config.EnvTelegramChatID] = "42"
	h.moveTo("AJA_TELEGRAM_CHAT_ID")
	h.press("enter", "-100", "enter")
	h.shows("Your environment sets it too, and wins.")

	h.moveTo("AJA_TELEGRAM_BOT_TOKEN")
	h.press("enter", "enter")
	h.shows("Removed AJA_TELEGRAM_BOT_TOKEN from .env.")
	if data, _ := os.ReadFile(h.p.EnvFile); strings.Contains(string(data), "BOT_TOKEN") {
		t.Errorf("not removed: %q", data)
	}

	h.moveTo("first_name")
	h.press("enter", "ctrl+u", "Ana Maria", "enter")
	h.shows("Saved personals.first_name = Ana Maria. The daemon's next run uses it.")
}
