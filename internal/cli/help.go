package cli

import (
	"context"
	"strings"
	"time"

	"github.com/luizfreitas27/aja/internal/apply"
	"github.com/luizfreitas27/aja/internal/notify"
)

// helpTimeout is how long the help pause waits for the person at the keyboard
// before giving the job up (and queueing its questions for review).
var helpTimeout = 15 * time.Minute

// terminalHelper is the help pause in a terminal: it shows what is stuck, sends
// a Telegram nudge, and waits for Enter.
type terminalHelper struct {
	app     *App
	notify  *notify.Telegram
	timeout time.Duration
}

// Help implements apply.Helper.
func (h *terminalHelper) Help(ctx context.Context, req apply.HelpRequest) error {
	ui := h.app.ui
	title := strings.TrimSpace(req.Job.Title + " | " + req.Job.Company)
	ui.Println()
	ui.Warn("help", "aja cannot answer these questions on "+title+":")
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
	h.notify.Send(ctx, "aja needs your help with "+title+"\n"+req.Job.Link+"\n\nUnanswered:\n- "+
		strings.Join(req.Questions, "\n- ")+"\n\nFill them in the Chrome window, then press Enter in the terminal.")
	_, err := h.app.readLine(ctx, h.timeout)
	return err
}
