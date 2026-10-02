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

	"github.com/luizfreitas27/aja/internal/apply"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/ui"
)

var ioEOF = io.EOF

func TestTheHelpPauseShowsTheQuestionsNudgesTelegramAndWaitsForEnter(t *testing.T) {
	var sentText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		sentText = string(data)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	saved := notify.APIBase
	notify.APIBase = srv.URL
	defer func() { notify.APIBase = saved }()

	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out, Stdin: strings.NewReader("\n")}
	a.ui = ui.New(&out, &out, true)
	h := &terminalHelper{app: a, notify: notify.New("1:tok", "42", nil), timeout: time.Minute}
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

func TestOnlyATerminalOnBothEndsIsAttended(t *testing.T) {
	cases := []struct{ flag, in, out, unattended bool }{
		{false, true, true, false},
		{true, true, true, true},
		{false, false, true, true}, // stdin from a pipe or the scheduler
		{false, true, false, true}, // stdout piped: nobody watching
	}
	for _, c := range cases {
		if got := run.ResolveUnattended(c.flag, c.in, c.out); got != c.unattended {
			t.Errorf("%+v -> %v", c, got)
		}
	}
}
