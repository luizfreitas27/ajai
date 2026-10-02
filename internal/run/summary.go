package run

import (
	"fmt"
	"strings"
)

// Fields are the run summary as label/value pairs, in display order; the
// terminal summary and the Telegram message both read them.
func (s Summary) Fields() [][2]string {
	return s.fields(true)
}

func (s Summary) fields(withEnded bool) [][2]string {
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
	f = append(f, [2]string{"Pending answers", fmt.Sprintf("%d to review", s.PendingAnswers)})
	if withEnded {
		f = append(f, [2]string{"Ended", s.Reason})
	}
	return f
}

func (s Summary) lines(withEnded bool) string {
	var b strings.Builder
	for _, kv := range s.fields(withEnded) {
		b.WriteString(kv[0] + ": " + kv[1] + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
