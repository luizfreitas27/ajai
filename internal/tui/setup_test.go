package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/config"
)

// selectCheck moves the checklist's selection to the check named name.
func (h *harness) selectCheck(name string) {
	h.t.Helper()
	up := make([]string, 20)
	for i := range up {
		up[i] = "up"
	}
	h.selectRow(fmt.Sprintf("check %q", name), up, 20, func(row string) bool { return strings.Contains(row, " "+name+" ") })
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
	h.shows("ajai   Setup", "Setup checklist", selected+"✗ config", "no config file at", "Enter: create the config with the init wizard (`ajai init`)",
		"✓ chrome ", "i  import settings, answers and history from the Python bot", "r  check again",
		"The other tabs open once there is a config file")
	h.golden("setup-no-config")

	h.press("1")
	h.shows("Setup checklist") // nothing else works without a config
	h.press("esc")
	h.shows("Setup checklist")
	h.press(tabKey(tabConfig))
	h.shows("There is no config file yet") // the secrets can be set already
	h.press("1")
	h.shows("Setup checklist")

	h.press("enter")
	if !reflect.DeepEqual(h.ran, [][]string{{"init"}}) {
		t.Fatalf("ran %v", h.ran)
	}
	h.shows("`ajai init` ended; checked again.", "✓ config ")
	h.press("esc")
	h.shows("No run in progress.", "Setup", "every check passes")
}

func TestNoLinkedInSessionOpensTheChecklistWhichOffersTheSignInAndTheConfigTab(t *testing.T) {
	h := newHarness(t)
	h.session = false
	h.env[config.EnvTelegramBotToken] = "123:ABC" // the chat id is missing
	h.onRun = func(args []string) {
		if args[0] == "login" {
			h.session = true
		}
	}
	h.open(100, 30)
	h.shows("Setup checklist") // a run would stop at LinkedIn's sign-in
	h.selectCheck("chrome profile")
	h.shows("! chrome profile", "has no LinkedIn session", "Enter: sign in to LinkedIn once (`ajai login` opens Chrome)")
	h.press("enter")
	if !reflect.DeepEqual(h.ran, [][]string{{"login"}}) {
		t.Fatalf("ran %v", h.ran)
	}
	h.shows("`ajai login` ended; checked again.", "✓ chrome profile")

	h.selectCheck("telegram")
	h.shows("Enter: set the secrets (or the AI settings) in the Config tab")
	h.press("enter")
	h.shows(tabKey(tabConfig)+" Config", "AJAI_TELEGRAM_BOT_TOKEN", "set (your environment)")
	if strings.Contains(h.screen(), "Setup checklist") {
		t.Errorf("the Config tab is shown:\n%s", h.screen())
	}

	// Saving the chat id runs the checks again: the Dashboard sees it at once.
	h.moveTo("AJAI_TELEGRAM_CHAT_ID")
	h.press("enter", "42", "enter")
	h.press("1")
	h.shows("every check passes")
}

func TestOnlyWarningsLeaveTheDashboardAndTheChecksRunAgainEveryHalfMinute(t *testing.T) {
	h := newHarness(t)
	h.env[config.EnvTelegramBotToken] = "123:ABC"
	h.open(100, 30)
	h.shows("No run in progress.", "1 warning(s): press s for the setup checklist")
	h.env[config.EnvTelegramChatID] = "42" // set elsewhere
	for range doctorEvery - 2 {            // opening made the first refresh
		h.refresh()
	}
	h.shows("1 warning(s)")
	h.refresh()
	h.shows("every check passes")
}

func TestTheChecklistNeverOpensOverASecretBeingTyped(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press(tabKey(tabConfig))
	h.moveTo("AJAI_AI_API_KEY")
	h.press("enter", "sk-")
	h.session = false
	h.send(runDoctor(h.deps())()) // a report saying LinkedIn is not signed in, mid-typing
	h.press("i")                  // still the secret
	h.shows("Edit AJAI_AI_API_KEY (hidden as you type)", "•••")
	if strings.Contains(h.screen(), "Setup checklist") || strings.Contains(h.screen(), "sk-i") {
		t.Fatalf("the checklist took the keys, or the secret shows:\n%s", h.screen())
	}
}

func TestTheSelectionNeverGoesBelowZeroBeforeTheFirstReport(t *testing.T) {
	var v checklist
	v, _ = v.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}, Deps{})
	v, _ = v.key(tea.KeyMsg{Type: tea.KeyEnter}, Deps{})
	if v.cursor != 0 {
		t.Fatalf("cursor %d", v.cursor)
	}
	h := newHarness(t)
	v, _ = v.update(runDoctor(h.deps())(), Deps{})
	if v.cursor != 0 || v.view(measure, h.deps(), 100) == "" {
		t.Errorf("cursor %d", v.cursor)
	}
}

func TestABrokenDotenvIsFixedInAnEditor(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(h.p.EnvFile, []byte("AJAI_AI_API_KEY='unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.open(100, 30)
	h.shows("Setup checklist", "✗ config", "Enter: fix ~/.env in an editor (ajai never shows its content)")
	h.press("enter")
	if len(h.ran) != 0 || strings.Contains(h.screen(), "unterminated") {
		t.Errorf("ran %v:\n%s", h.ran, h.screen())
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
	h.press("i", "~/old-bot")
	h.golden("setup-import")
	h.press("enter")
	if !reflect.DeepEqual(h.ran, [][]string{{"import", "--", filepath.Join(h.p.DataDir, "old-bot")}}) {
		t.Fatalf("ran %v", h.ran)
	}
	h.shows("`ajai import` ended; checked again.")
}

func TestCheckAgainSeesWhatChanged(t *testing.T) {
	h := newHarness(t)
	h.session = false
	h.open(100, 30)
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
		if got := d.tilde(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}
