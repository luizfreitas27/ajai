package cli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/run"
)

// jsonEvent is one line of `ajai run --json`: one object per event, and the run
// summary as the "run-end" event.
type jsonEvent struct {
	Time    string       `json:"time"`
	Event   string       `json:"event"`
	Label   string       `json:"label,omitempty"` // what a start or warn note is about: "market", "mode", "config"...
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

// report shows one run event: a terminal line, or a JSON line with --json.
func (a *App) report(e run.Event) {
	if !a.eventsAsJSON {
		a.printEvent(e)
		return
	}
	line := jsonEvent{Event: string(e.Kind), Text: e.Text, Summary: e.Summary}
	if e.Card != nil {
		line.Job = &jsonJob{ID: e.Card.ID, Title: e.Card.Title, Company: e.Card.Company, Location: e.Card.Location, Link: e.Card.Link}
	}
	a.printJSON(line)
}

// note reports something outside the run loop (a warning before the run, the
// run's settings): a JSON event in --json mode, a terminal line otherwise.
func (a *App) note(kind run.EventKind, label, text string) {
	if a.eventsAsJSON {
		a.printJSON(jsonEvent{Event: string(kind), Label: label, Text: text})
		return
	}
	if kind == run.EventWarn {
		a.ui.Warn(label, text)
		return
	}
	a.ui.KV(label, text)
}

// printJSON writes one event line. With --json, stdout carries nothing else.
func (a *App) printJSON(line jsonEvent) {
	line.Time = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(line)
	if err != nil {
		return
	}
	a.ui.Printf("%s\n", data)
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
		a.ui.Hint("Review them with `ajai answers review`.")
	}
}
