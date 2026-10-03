package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// inColour are the styles as the harness draws them with h.color.
func inColour() styles {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	return newStyles(r)
}

// stroked is the tab bar's text above the flame stroke in the divider under
// it, or "" when the divider has no stroke.
func (h *harness) stroked() string {
	h.t.Helper()
	lines := strings.Split(h.screen(), "\n")
	bar, rule := []rune(lines[0]), []rune(lines[1])
	first, last := -1, -1
	for i, r := range rule {
		if r == '━' {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return ""
	}
	if last >= len(bar) {
		h.t.Fatalf("the stroke runs past the tab bar:\n%s", h.screen())
	}
	return string(bar[first : last+1])
}

func TestTheActiveTabIsMarkedByAStrokeUnderItsLabel(t *testing.T) {
	h := newHarness(t)
	h.remembered() // three pending answers: the Answers label grows
	h.open(100, 30)
	for _, tc := range []struct{ key, label string }{
		{"1", "1 Dashboard"}, {"2", "2 Answers (3)"}, {"3", "3 Schedule"}, {"4", "4 Jobs"}, {"5", "5 Config"},
	} {
		h.press(tc.key)
		if got := h.stroked(); got != tc.label {
			t.Errorf("tab %s: the stroke is under %q, want %q:\n%s", tc.key, got, tc.label, h.screen())
		}
	}
	h.press("?")
	if got := h.stroked(); got != "" {
		t.Errorf("the help is no tab, but the stroke is under %q", got)
	}
}

func TestTheSetupChecklistTakesTheStroke(t *testing.T) {
	h := newHarness(t)
	h.removeConfig()
	h.open(80, 24)
	if got := h.stroked(); got != "Setup" {
		t.Errorf("the stroke is under %q, want Setup:\n%s", got, h.screen())
	}
}

func TestTheFlameThemeInColour(t *testing.T) {
	h := newHarness(t)
	h.color = true
	h.someJobs()
	h.remembered()
	h.open(100, 30)
	h.golden("colour-dashboard") // the brand, the stroke, titles; pending answers in amber, the passing checks in green
	st := inColour()
	if dash := h.screen(); !strings.Contains(dash, st.warn.Render("3 to review")) || !strings.Contains(dash, st.ok.Render("every check passes")) {
		t.Errorf("the states lost their colours:\n%q", dash)
	}
	h.jobsTab()
	h.golden("colour-jobs") // the selection's bar, the filter shown, the keys in the hints
	for what, drawn := range map[string]string{"the stroke": st.stroke.Render("━━━━━━"), "the bar": st.stroke.Render("▌"),
		"a key": st.key.Render("tab"), "the filter": st.filter.Render("all")} {
		if !strings.Contains(h.screen(), drawn) {
			t.Errorf("%s is not drawn in the accent:\n%q", what, h.screen())
		}
	}
}

func TestAFailedCheckStaysRedAmongTheFlame(t *testing.T) {
	h := newHarness(t)
	h.color = true
	h.removeConfig()
	h.open(100, 30)
	h.golden("colour-setup")
	if !strings.Contains(h.screen(), inColour().fail.Render("✗")) {
		t.Errorf("the failed check is not red:\n%q", h.screen())
	}
}
