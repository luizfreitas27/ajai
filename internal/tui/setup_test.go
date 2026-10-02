package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/config"
)

// selectCheck moves the checklist's selection to the check named name.
func (h *harness) selectCheck(name string) {
	h.t.Helper()
	for range 20 {
		h.press("up")
	}
	for range 20 {
		for _, l := range strings.Split(h.screen(), "\n") {
			if strings.HasPrefix(l, "> ") && strings.Contains(l, " "+name+" ") {
				return
			}
		}
		h.press("down")
	}
	h.t.Fatalf("no check %q:\n%s", name, h.screen())
}

func TestWithoutAConfigTheTUIOpensOnTheChecklistAndTheInitWizardFixesIt(t *testing.T) {
	h := newHarness(t)
	h.removeConfig()
	h.onRun = func(args []string) {
		if args[0] == "init" {
			h.writeConfig() // what the wizard leaves behind
		}
	}
	h.open(100, 30)
	h.shows("aja   Setup", "Setup checklist", "> ✗ config", "no config file at", "Enter: create the config with the init wizard (`aja init`)",
		"✓ chrome ", "i  import settings, answers and history from the Python bot", "r  check again",
		"The other tabs open once there is a config file")
	h.golden("setup-no-config")

	h.press("1")
	h.shows("Setup checklist") // nothing else works without a config
	h.press("esc")
	h.shows("Setup checklist")
	h.press("2")
	h.shows("There is no config file yet") // the secrets can be set already
	h.press("1")
	h.shows("Setup checklist")

	h.press("enter")
	if !reflect.DeepEqual(h.ran, [][]string{{"init"}}) {
		t.Fatalf("ran %v", h.ran)
	}
	h.shows("`aja init` ended; checked again.", "✓ config ")
	h.press("esc")
	h.shows("No run in progress.", "Setup", "every check passes")
}

func TestTheChecklistOffersTheSignInAndTheConfigTab(t *testing.T) {
	h := newHarness(t)
	h.session = false
	h.env[config.EnvTelegramBotToken] = "123:ABC" // the chat id is missing
	h.onRun = func(args []string) {
		if args[0] == "login" {
			h.session = true
		}
	}
	h.open(100, 30)
	h.shows("No run in progress.", "2 warning(s): press s for the setup checklist") // warnings do not open it
	h.press("s")
	h.selectCheck("chrome profile")
	h.shows("! chrome profile", "has no LinkedIn session", "Enter: sign in to LinkedIn once (`aja login` opens Chrome)")
	h.press("enter")
	if !reflect.DeepEqual(h.ran, [][]string{{"login"}}) {
		t.Fatalf("ran %v", h.ran)
	}
	h.shows("`aja login` ended; checked again.", "✓ chrome profile")

	h.selectCheck("telegram")
	h.shows("Enter: set the secrets (or the AI settings) in the Config tab")
	h.press("enter")
	h.shows("2 Config", "AJA_TELEGRAM_BOT_TOKEN", "set (your environment)")
	if strings.Contains(h.screen(), "Setup checklist") {
		t.Errorf("the Config tab is shown:\n%s", h.screen())
	}
}

func TestImportAsksForTheCheckoutAndRunsInTheTerminal(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("s", "i")
	h.shows("Import from the Python bot", "enter imports · esc cancels")
	h.press("q1") // typed, not keys
	h.press("esc")
	if len(h.ran) != 0 || h.quit {
		t.Fatalf("cancelled: ran %v quit %v", h.ran, h.quit)
	}
	h.press("i", "~/old-bot", "enter")
	home, _ := os.UserHomeDir()
	if !reflect.DeepEqual(h.ran, [][]string{{"import", filepath.Join(home, "old-bot")}}) {
		t.Fatalf("ran %v", h.ran)
	}
	h.shows("`aja import` ended; checked again.")
}

func TestCheckAgainSeesWhatChanged(t *testing.T) {
	h := newHarness(t)
	h.session = false
	h.open(100, 30)
	h.press("s")
	h.shows("! chrome profile")
	h.session = true
	h.press("r")
	h.shows("Checked again.", "✓ chrome profile")
	h.press("esc")
	h.shows("every check passes")
}

func TestPathsUnderHomeAreShortened(t *testing.T) {
	d := Deps{Home: "/home/ana"}
	for in, want := range map[string]string{
		"/home/ana":                    "~",
		"no config at /home/ana/x.yml": "no config at ~/x.yml",
		"/home/ana2/x and /home/ana":   "/home/ana2/x and ~",
		"(/home/ana) here":             "(~) here",
	} {
		if got := d.short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}
