package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/luizfreitas27/ajai/internal/schedule"
)

// logoTop is the screen line where the whole logo starts, its rows one under
// the other, or -1.
func (h *harness) logoTop() int {
	lines := strings.Split(h.screen(), "\n")
	for top := range lines {
		row := 0
		for row < len(logo) && top+row < len(lines) && strings.Contains(lines[top+row], strings.TrimRight(logo[row], " ")) {
			row++
		}
		if row == len(logo) {
			return top
		}
	}
	return -1
}

// column is where text starts in the screen line that has it, in cells, or -1.
func (h *harness) column(text string) int {
	for _, l := range strings.Split(h.screen(), "\n") {
		if i := strings.Index(l, text); i >= 0 {
			return len([]rune(l[:i]))
		}
	}
	return -1
}

func TestTheLogoStandsBesideTheSummaryOnAWideTerminal(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	lines := strings.Split(h.screen(), "\n")
	top := h.logoTop()
	if top < 0 {
		t.Fatalf("no logo:\n%s", h.screen())
	}
	if !strings.Contains(lines[top], "Run") || !strings.Contains(lines[top], "No run in progress.") {
		t.Errorf("the summary does not start beside the logo:\n%s", h.screen())
	}
	h.golden("dashboard-logo-beside")
}

func TestASummaryLineTooLongBesideTheLogoWrapsInItsColumn(t *testing.T) {
	h := newHarness(t)
	if _, err := h.st.AddWindow(schedule.Window{Days: []time.Weekday{time.Monday, time.Wednesday}, Start: 14 * 60, End: 16 * 60, Market: "brasil", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.open(100, 30)
	h.shows("not running: schedule windows start runs only", "while it runs") // cut nowhere
	if strings.Contains(h.screen(), "…") {
		t.Errorf("a summary line was cut:\n%s", h.screen())
	}
	if n := strings.Count(h.screen(), "Daemon"); n != 1 {
		t.Errorf("the label is repeated on the line that continues it (%d times):\n%s", n, h.screen())
	}
	if text, rest := h.column("not running: schedule"), h.column("while it runs"); rest != text {
		t.Errorf("the rest of the line starts at column %d, not under its text at %d:\n%s", rest, text, h.screen())
	}
}

func TestOnANarrowTallTerminalTheLogoGoesAboveTheSummary(t *testing.T) {
	h := newHarness(t)
	h.open(80, 40)
	top := h.logoTop()
	if top < 0 {
		t.Fatalf("no logo:\n%s", h.screen())
	}
	lines := strings.Split(h.screen(), "\n")
	if strings.Contains(lines[top], "Run") || !strings.HasPrefix(lines[top+len(logo)+1], "Run") {
		t.Errorf("the summary is not under the logo:\n%s", h.screen())
	}
	h.golden("dashboard-logo-above")
}

func TestOnTheSmallestTerminalTheLogoStepsAside(t *testing.T) {
	h := newHarness(t)
	h.open(80, 24)
	if h.logoTop() >= 0 {
		t.Errorf("the logo takes the room of the runs and the log:\n%s", h.screen())
	}
	h.shows("Run", "Last runs", "Today's log")
}

func TestTheLogoIsAFlameFromYellowAtTheBaseToEmberAtTheTips(t *testing.T) {
	h := newHarness(t)
	h.color = true
	h.open(100, 30)
	for row, hex := range map[int]string{0: "#B8101E", len(logo) - 1: "#FFE85C"} {
		start := string([]rune(logo[row])[:4])
		if !strings.Contains(h.screen(), escapeOf(hex)+start) {
			t.Errorf("logo row %d is not %s:\n%q", row, hex, h.screen())
		}
	}
	h.golden("colour-logo")
}

// escapeOf is the escape sequence that starts text in the colour hex, as the
// harness draws it with h.color.
func escapeOf(hex string) string {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	drawn := r.NewStyle().Foreground(lipgloss.Color(hex)).Render("x")
	return drawn[:strings.Index(drawn, "x")]
}
