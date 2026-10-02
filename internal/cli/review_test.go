package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/store"
)

// seed puts entries in the harness database.
func (h *harness) seed(entries ...store.Answer) {
	h.t.Helper()
	h.initValid()
	st, err := store.Open(filepath.Join(h.home, "aja.db"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for i, e := range entries {
		e.UpdatedAt = time.Date(2026, 10, 1, 10, 0, 59-i, 0, time.UTC).Format(time.RFC3339) // listed in this order
		if err := st.UpsertAnswer(e); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) memory() *memory.Memory {
	h.t.Helper()
	st, err := store.Open(filepath.Join(h.home, "aja.db"))
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = st.Close() })
	return memory.New(st, nil)
}

func pendingAnswer(id, label, kind, answer, source string) store.Answer {
	return store.Answer{ID: id, Label: label, Normalised: strings.ToLower(strings.TrimRight(label, "?")), Kind: kind,
		Answer: answer, Source: source, State: "pending"}
}

func TestReviewApprovesEditsDeletesAndSkipsEachPendingAnswerInTurn(t *testing.T) {
	h := newHarness(t)
	h.seed(
		pendingAnswer("a1", "Years with Go?", "text", "4", "ai"),
		pendingAnswer("a2", "Favourite colour?", "radio", "", "form"), // nothing could answer it
		pendingAnswer("a3", "Subscribe to job alerts", "checkbox", "checked", "form"),
		pendingAnswer("a4", "Preferred shift", "select", "Night", "ai"),
	)
	// a1: approve. a2: approve refused (no answer), an empty edit (unchanged), then edit
	// to Blue. a3: delete. a4: skip.
	h.stdin = "a\na\ne\n\ne\nBlue\nd\n\n"
	code, out, errText := h.run("answers", "review")
	if code != 0 {
		t.Fatal(errText)
	}
	for _, want := range []string{"4 pending answer(s)", "[1/4] Years with Go?", "answer: \"4\"", "(none yet)",
		"no answer to approve yet", "nothing typed; unchanged", "1 approved, 1 edited, 1 deleted, 1 skipped; 1 still pending"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	mem := h.memory()
	if e, _ := mem.Get("a1"); e.State != "approved" || e.Source != "ai" {
		t.Errorf("a1 %+v", e)
	}
	if e, _ := mem.Get("a2"); e.State != "approved" || e.Answer != "Blue" || e.Source != "user" {
		t.Errorf("a2 %+v", e)
	}
	if e, _ := mem.Get("a3"); e != nil {
		t.Errorf("a3 not deleted: %+v", e)
	}
	if e, _ := mem.Get("a4"); e.State != "pending" {
		t.Errorf("a4 %+v", e)
	}
}

func TestReviewStopsOnQuitAndOnEndOfInputAndSaysWhenThereIsNothing(t *testing.T) {
	h := newHarness(t)
	h.seed(pendingAnswer("a1", "Q1", "text", "x", "ai"), pendingAnswer("a2", "Q2", "text", "y", "ai"))
	h.stdin = "q\n"
	_, out, _ := h.run("answers", "review")
	if !strings.Contains(out, "0 approved, 0 edited, 0 deleted, 0 skipped; 2 still pending") || strings.Contains(out, "[2/2]") {
		t.Errorf("%s", out)
	}
	h.stdin = "a\n" // input ends after the first answer
	_, out, _ = h.run("answers", "review")
	if !strings.Contains(out, "1 approved") || !strings.Contains(out, "1 still pending") {
		t.Errorf("%s", out)
	}
	h2 := newHarness(t)
	h2.initValid()
	if _, out, _ := h2.run("answers", "review"); !strings.Contains(out, "Nothing to review.") {
		t.Errorf("%s", out)
	}
}

func TestReadLineHonoursTimeoutCancellationAndEndOfInput(t *testing.T) {
	a := &App{Stdin: strings.NewReader("first\r\nsecond")}
	ctx := context.Background()
	if got, err := a.readLine(ctx, 0); got != "first" || err != nil {
		t.Errorf("%q %v", got, err)
	}
	if got, err := a.readLine(ctx, 0); got != "second" || err != nil {
		t.Errorf("a last line without a newline still counts: %q %v", got, err)
	}
	if _, err := a.readLine(ctx, 0); !errors.Is(err, io.EOF) {
		t.Errorf("%v", err)
	}
	blocked := &App{Stdin: blockingReader{}}
	if _, err := blocked.readLine(ctx, 20*time.Millisecond); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("%v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := blocked.readLine(cctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

func TestAnEntryDeletedElsewhereIsTreatedAsGoneNotAsAFailure(t *testing.T) {
	h := newHarness(t)
	h.seed(pendingAnswer("a1", "Q1", "text", "x", "ai"), pendingAnswer("a2", "Q2", "text", "y", "ai"))
	h.stdin = "a\na\n"
	mem := h.memory()
	if err := mem.Delete("a1"); err != nil {
		t.Fatal(err)
	}
	// What review sees when another process deleted the entry it is showing.
	if _, err := mem.Approve("a1", ""); !gone(err) {
		t.Errorf("%v", err)
	}
	if err := mem.Delete("a1"); !gone(err) {
		t.Errorf("%v", err)
	}
	code, out, errText := h.run("answers", "review")
	if code != 0 || !strings.Contains(out, "1 approved") {
		t.Errorf("%d %s %s", code, out, errText)
	}
}
