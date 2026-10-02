package run

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"
)

// Notifier receives the run's messages (Telegram in production). Send must
// never fail the run; it reports whether the message went out.
type Notifier interface {
	Send(ctx context.Context, text string) bool
}

// notifyTimeout bounds the end-of-run message: the run's own context may
// already be cancelled (Ctrl-C), and the message must still go out.
const notifyTimeout = 15 * time.Second

// SummaryMessage is the message for a run that ended (finished, stopped by the
// user, or stopped by a limit).
func SummaryMessage(s Summary) string {
	text := "aja: run finished.\n\n" + s.lines(true)
	if s.PendingAnswers > 0 {
		text += "\n\nReview the pending answers with: aja answers review"
	}
	return text
}

// FailureMessage is the message for a run that died with an error, with what
// it had done until then (the error is the ending, so it is said once).
func FailureMessage(s Summary, err error) string {
	return "aja stopped with an error.\n\n" + err.Error() + "\n\n" + s.lines(false)
}

// StartFailureMessage is the message for a run that could not start (bad
// config, Chrome missing or busy, AI settings).
func StartFailureMessage(err error) string {
	return "aja could not start a run.\n\n" + err.Error()
}

// notifyEnd sends exactly one message for a run: the summary, the error with
// the counts, or (when the run was never recorded) the start failure.
func (r *Runner) notifyEnd(sum Summary, err error) {
	switch {
	case err == nil:
		send(r.Notifier, SummaryMessage(sum))
	case sum.RunID == 0:
		send(r.Notifier, StartFailureMessage(err))
	default:
		send(r.Notifier, FailureMessage(sum, err))
	}
}

// StartFailedMsg is the log line of a run that could not start; the TUI reads
// it (ReadStartFailure) to say why a run it started did not.
const StartFailedMsg = "run could not start"

// LogStartFailure writes why a run could not start to its log: a run started
// from the TUI has no terminal to print it to.
func LogStartFailure(logger *slog.Logger, err error) {
	logger.Error(StartFailedMsg, "error", err.Error())
}

// ReadStartFailure is the error of the last LogStartFailure line in the log
// at path written since since, or "".
func ReadStartFailure(path string, since time.Time) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	reason := ""
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 64*1024), 1<<20)
	for lines.Scan() {
		var line struct {
			Time  time.Time `json:"time"`
			Msg   string    `json:"msg"`
			Error string    `json:"error"`
		}
		if json.Unmarshal(lines.Bytes(), &line) == nil && line.Msg == StartFailedMsg && !line.Time.Before(since.Add(-time.Second)) {
			reason = line.Error
		}
	}
	return reason
}

// ReportStartFailure tells the user a run could not start (configuration,
// Chrome, AI settings), unless the user cancelled it themselves.
func ReportStartFailure(ctx context.Context, n Notifier, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}
	send(n, StartFailureMessage(err))
}

// send delivers one message on a fresh context: the run's own context may
// already be cancelled (Ctrl-C), and the message must still go out.
func send(n Notifier, text string) {
	if n == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	n.Send(ctx, text)
}
