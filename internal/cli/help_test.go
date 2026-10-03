package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/apply"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/notify"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/ui"
)

func TestTheHelpPauseShowsTheQuestionsNudgesTelegramAndWaitsForEnter(t *testing.T) {
	var sentText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		sentText = string(data)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	var out bytes.Buffer
	// The Enter arrives after the pause started (an Enter typed before it is drained).
	in, typed := io.Pipe()
	a := &App{Stdout: &out, Stderr: &out, Stdin: in}
	a.ui = ui.New(&out, &out, true)
	h := &terminalHelper{app: a, notify: notify.New("1:tok", "42", nil).WithAPIBase(srv.URL), timeout: time.Minute}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(typed, "\n")
	}()
	req := apply.HelpRequest{Job: linkedin.Card{Title: "Go Dev", Company: "ACME", Link: "https://www.linkedin.com/jobs/view/1"},
		Questions: []string{"Favourite colour? [Red, Blue]"}, Screenshot: "/tmp/shot.png"}
	if err := h.Help(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Go Dev | ACME", "- Favourite colour? [Red, Blue]", "/tmp/shot.png", "press Enter"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("terminal lacks %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(sentText, "Favourite colour?") || !strings.Contains(sentText, "jobs/view/1") {
		t.Errorf("telegram got %s", sentText)
	}
	// Nobody at the keyboard: the wait ends and the job is given up.
	b := &App{Stdout: &out, Stderr: &out, Stdin: blockingReader{}}
	b.ui = a.ui
	h = &terminalHelper{app: b, notify: notify.New("", "", nil), timeout: 20 * time.Millisecond}
	if err := h.Help(context.Background(), req); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("%v", err)
	}
}

func TestAnEnterTypedBeforeThePauseDoesNotAnswerItAndAClosedStdinGivesUpQuietly(t *testing.T) {
	var out bytes.Buffer
	// Two stale Enters (say, pressed during the login wait), then nothing.
	a := &App{Stdout: &out, Stderr: &out, Stdin: io.MultiReader(strings.NewReader("\n\n"), blockingReader{})}
	a.ui = ui.New(&out, &out, true)
	h := &terminalHelper{app: a, notify: notify.New("", "", nil), timeout: 100 * time.Millisecond}
	if err := h.Help(context.Background(), apply.HelpRequest{Questions: []string{"Q"}}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("a stale Enter answered the pause: %v", err)
	}
	var shown bytes.Buffer
	closed := &App{Stdout: &shown, Stderr: &shown, Stdin: strings.NewReader("")}
	closed.ui = ui.New(&shown, &shown, true)
	h = &terminalHelper{app: closed, notify: notify.New("", "", nil), timeout: time.Minute}
	if err := h.Help(context.Background(), apply.HelpRequest{Questions: []string{"Q"}}); !errors.Is(err, io.EOF) {
		t.Errorf("%v", err)
	}
	if shown.Len() != 0 {
		t.Errorf("with stdin closed nothing is shown or sent: %q", shown.String())
	}
}

func TestOnlyATerminalOnBothEndsIsAttended(t *testing.T) {
	cases := []struct{ flag, in, out, unattended bool }{
		{false, true, true, false},
		{true, true, true, true},
		{false, false, true, true}, // stdin from a pipe or the scheduler
		{false, true, false, true}, // stdout piped: nobody watching
	}
	for _, c := range cases {
		if got := run.ResolveUnattended(c.flag, false, c.in, c.out); got != c.unattended {
			t.Errorf("%+v -> %v", c, got)
		}
	}
}
