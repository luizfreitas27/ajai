package run

import (
	"context"
	"fmt"
	"strings"
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

// Fields are the run summary as label/value pairs, in display order; the
// terminal summary and the Telegram message both read them.
func (s Summary) Fields() [][2]string {
	mode := "live"
	if s.DryRun {
		mode = "dry run"
	}
	f := [][2]string{{"Run", fmt.Sprintf("#%d (%s, %s)", s.RunID, s.Market, mode)}}
	if s.DryRun {
		f = append(f, [2]string{"Would apply", fmt.Sprint(s.WouldApply)})
	} else {
		f = append(f, [2]string{"Applied", fmt.Sprint(s.Applied)}, [2]string{"External", fmt.Sprint(s.External)},
			[2]string{"Failed", fmt.Sprint(s.Failed)})
	}
	f = append(f, [2]string{"Skipped", fmt.Sprint(s.Skipped)})
	if s.Captured > 0 {
		f = append(f, [2]string{"Captured", fmt.Sprintf("%d answer(s) you typed, remembered", s.Captured)})
	}
	f = append(f, [2]string{"Pending answers", fmt.Sprintf("%d to review", s.PendingAnswers)},
		[2]string{"Ended", s.Reason})
	return f
}

func (s Summary) lines() string {
	var b strings.Builder
	for _, kv := range s.Fields() {
		b.WriteString(kv[0] + ": " + kv[1] + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// SummaryMessage is the message for a run that ended (finished, stopped by the
// user, or stopped by a limit).
func SummaryMessage(s Summary) string {
	text := "aja: run finished.\n\n" + s.lines()
	if s.PendingAnswers > 0 {
		text += "\n\nReview the pending answers with: aja answers review"
	}
	return text
}

// FailureMessage is the message for a run that died with an error, with what
// it had done until then.
func FailureMessage(s Summary, err error) string {
	return "aja stopped with an error.\n\n" + err.Error() + "\n\n" + s.lines()
}

// StartFailureMessage is the message for a run that could not start (bad
// config, Chrome missing or busy, AI settings).
func StartFailureMessage(err error) string {
	return "aja could not start a run.\n\n" + err.Error()
}

// notifyEnd sends exactly one message for a finished run.
func (r *Runner) notifyEnd(sum Summary, err error) {
	if r.Notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	text := SummaryMessage(sum)
	if err != nil {
		text = FailureMessage(sum, err)
	}
	r.Notifier.Send(ctx, text)
}
