package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/apply"
	"github.com/luizfreitas27/ajai/internal/notify"
)

// defaultHelpTimeout is how long the help pause waits for the person at the
// keyboard before giving the job up (and queueing its questions for review).
const defaultHelpTimeout = 15 * time.Minute

// terminalHelper is the help pause in a terminal: it shows what is stuck, sends
// a Telegram nudge, and waits for Enter.
type terminalHelper struct {
	app     *App
	notify  *notify.Telegram
	timeout time.Duration
}

// Help implements apply.Helper.
func (h *terminalHelper) Help(ctx context.Context, req apply.HelpRequest) error {
	h.app.drainLines()
	if h.app.stdinClosed {
		return io.EOF // nobody can press Enter any more: no point showing or nudging
	}
	ui := h.app.ui
	title := strings.TrimSpace(req.Job.Title + " | " + req.Job.Company)
	ui.Println()
	ui.Warn("help", "ajai cannot answer these questions on "+title+":")
	for _, q := range req.Questions {
		ui.Hint("- " + q)
	}
	if req.Job.Link != "" {
		ui.Hint("job: " + req.Job.Link)
	}
	if req.Screenshot != "" {
		ui.Hint("screenshot: " + req.Screenshot)
	}
	ui.Hint("Fill them in the Chrome window (do NOT click Next, Review or Submit), then press Enter here.")
	ui.Hint("What you type is remembered as an approved answer. Waiting up to " + h.timeout.String() + ".")
	h.notify.Send(ctx, "ajai needs your help with "+title+"\n"+req.Job.Link+"\n\nUnanswered:\n- "+
		strings.Join(req.Questions, "\n- ")+"\n\nFill them in the Chrome window, then press Enter in the terminal.")
	_, err := h.app.readLine(ctx, h.timeout)
	return err
}

// filterPause is search.pause_after_filters in a terminal: after a term's
// filters are applied, wait for Enter so the user can check or adjust them in
// Chrome. Typing "off", closing stdin or not answering in time stops the
// pauses for the rest of the session.
func (a *App) filterPause(timeout time.Duration) func(ctx context.Context, term string) bool {
	return func(ctx context.Context, term string) bool {
		a.drainLines()
		if a.stdinClosed {
			return false
		}
		a.ui.Warn("filters", fmt.Sprintf("filters applied for %q: check them in Chrome (you may change them now), then press Enter; type off to stop pausing", term))
		line, err := a.readLine(ctx, timeout)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				a.ui.Hint("no answer; not pausing after filters again this session")
			}
			return false
		case strings.EqualFold(strings.TrimSpace(line), "off"):
			a.ui.Hint("not pausing after filters again this session")
			return false
		}
		return true
	}
}
