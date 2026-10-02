package cli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/luizfreitas27/aja/internal/run"
)

// jsonEvent is one line of `aja run --json`: one object per event, and the run
// summary as the "run-end" event.
type jsonEvent struct {
	Time    string       `json:"time"`
	Event   string       `json:"event"`
	Text    string       `json:"text,omitempty"`
	Job     *jsonJob     `json:"job,omitempty"`
	Summary *run.Summary `json:"summary,omitempty"`
}

type jsonJob struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Company  string `json:"company"`
	Location string `json:"location,omitempty"`
	Link     string `json:"link"`
}

// eventsAsJSON is set by `aja run --json`: events go to stdout as JSON lines and
// nothing else is printed there.
func (a *App) report(e run.Event) {
	if !a.eventsAsJSON {
		a.printEvent(e)
		return
	}
	line := jsonEvent{Time: time.Now().Format(time.RFC3339), Event: string(e.Kind), Text: e.Text, Summary: e.Summary}
	if e.Card != nil {
		line.Job = &jsonJob{ID: e.Card.ID, Title: e.Card.Title, Company: e.Card.Company, Location: e.Card.Location, Link: e.Card.Link}
	}
	data, err := json.Marshal(line)
	if err != nil {
		return
	}
	a.ui.Printf("%s\n", data)
}

// note reports something outside the run loop (a warning before the run, the
// run's settings): a JSON event in --json mode, a terminal line otherwise.
func (a *App) note(kind run.EventKind, label, text string) {
	if a.eventsAsJSON {
		a.report(run.Event{Kind: kind, Text: text})
		return
	}
	if kind == run.EventWarn {
		a.ui.Warn(label, text)
		return
	}
	a.ui.KV(label, text)
}

func (a *App) printEvent(e run.Event) {
	title := ""
	if e.Card != nil {
		title = strings.TrimSpace(e.Card.Title + " | " + e.Card.Company)
	}
	switch e.Kind {
	case run.EventLogin:
		a.ui.Warn("login", e.Text)
	case run.EventTerm:
		a.ui.Println()
		a.ui.Title("Searching: " + e.Text)
	case run.EventWouldApply:
		a.ui.OK("would apply", title)
	case run.EventApplied:
		a.ui.OK("applied", title)
	case run.EventFailed:
		a.ui.Fail("failed", title+"  ("+e.Text+")")
	case run.EventExternal:
		a.ui.OK("external", title)
	case run.EventSkip:
		a.ui.Hint("skip  " + title + "  (" + e.Text + ")")
	case run.EventLimit:
		a.ui.Warn("stop", e.Text)
	case run.EventWarn:
		a.ui.Warn("warn", e.Text)
	case run.EventWait:
		a.ui.Println()
		a.ui.Warn("wait", e.Text)
	case run.EventRunEnd:
		if e.Summary != nil {
			a.printSummary(*e.Summary)
		}
	default:
		a.ui.Println(e.Text)
	}
}

func (a *App) printSummary(s run.Summary) {
	a.ui.Println()
	a.ui.Title("Run summary")
	for _, kv := range s.Fields() {
		a.ui.KV(strings.ToLower(kv[0]), kv[1])
	}
	if s.PendingAnswers > 0 {
		a.ui.Hint("Review them with `aja answers review`.")
	}
}
