package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/paths"
	"github.com/luizfreitas27/ajai/internal/store"
)

// withStore opens the harness's database the way the commands do and runs f.
func (h *harness) withStore(t *testing.T, f func(*store.Store)) {
	t.Helper()
	st, err := store.Open(paths.Resolve("", config.MapEnv(map[string]string{"AJAI_HOME": h.home}).Getenv).DBFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	f(st)
}

// seedMessage queues one message for a job the store knows, approves it when
// asked, and returns its id.
func (h *harness) seedMessage(t *testing.T, job, url, text string, approve bool) int64 {
	t.Helper()
	var id int64
	h.withStore(t, func(st *store.Store) {
		if err := st.UpsertJob(store.Job{JobID: job, Outcome: store.Applied, Title: "Dev Python", Company: "Acme"}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.QueueMessage(store.Message{JobID: job, RecipientName: "Ana Silva", RecipientURL: url, Text: text}); err != nil {
			t.Fatal(err)
		}
		all, err := st.ListMessages("")
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range all {
			if m.JobID == job && m.RecipientURL == url {
				id = m.ID
			}
		}
		if approve {
			if err := st.ApproveMessage(id, messages.Digest(text)); err != nil {
				t.Fatal(err)
			}
		}
	})
	return id
}

// approveAllDigest reads the approve-all line from a list output.
func approveAllDigest(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "approve --all ") {
			return strings.Fields(line)[len(strings.Fields(line))-1]
		}
	}
	t.Fatalf("no approve-all line in:\n%s", out)
	return ""
}

const longMessage = "Olá Ana, acabei de me candidatar à vaga de Dev Python na Acme. Fico à disposição para conversar e no aguardo de um contato. Obrigado!"

func TestMessagesListShowsTheFullTextAndDigestAndTheApproveAllLineOnlyWhenQueued(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.seedMessage(t, "1", "https://www.linkedin.com/in/ana", longMessage, false)
	h.seedMessage(t, "2", "https://www.linkedin.com/in/bia", "Oi Bia", true)

	code, out, errText := h.run("messages", "list")
	if code != 0 {
		t.Fatalf("%d %s", code, errText)
	}
	for _, want := range []string{
		"queued", "approved", "Dev Python | Acme", "Ana Silva", "https://www.linkedin.com/in/ana",
		longMessage, messages.Digest(longMessage),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	queued := messages.ListDigest([]messages.Item{{ID: 1, Digest: messages.Digest(longMessage)}})
	if !strings.Contains(out, "ajai messages approve --all "+queued) {
		t.Errorf("approve-all line missing:\n%s", out)
	}
	if !strings.Contains(out, "ajai messages approve 1 "+messages.Digest(longMessage)) {
		t.Errorf("per-message approve line missing:\n%s", out)
	}
	if strings.Index(out, "Oi Bia") > strings.Index(out, longMessage) {
		t.Errorf("newest first expected:\n%s", out)
	}

	_, out, _ = h.run("messages", "list", "--state", "approved")
	if strings.Contains(out, "approve --all") || strings.Contains(out, "approve 2 ") {
		t.Errorf("nothing queued is shown, so no approve line:\n%s", out)
	}
}

func TestMessagesApproveWithTheShownDigestApprovesThatText(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	id := h.seedMessage(t, "1", "https://www.linkedin.com/in/ana", longMessage, false)

	if code, out, errText := h.run("messages", "approve", fmt.Sprint(id), messages.Digest(longMessage)); code != 0 || !strings.Contains(out, "approved") {
		t.Fatalf("%d %s %s", code, out, errText)
	}
	_, out, _ := h.run("messages", "list", "--state", "approved")
	if !strings.Contains(out, longMessage) {
		t.Errorf("the message is not approved:\n%s", out)
	}
}

func TestMessagesApproveWithAStaleDigestIsRefusedAndTheRowStaysQueued(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.seedMessage(t, "1", "https://www.linkedin.com/in/ana", longMessage, false)

	code, _, errText := h.run("messages", "approve", "1", messages.Digest("an older text"))
	if code == 0 || !strings.Contains(errText, "changed since it was shown") || !strings.Contains(errText, "messages list") {
		t.Errorf("stale digest: %d %s", code, errText)
	}
	if _, out, _ := h.run("messages", "list", "--state", "queued"); !strings.Contains(out, longMessage) {
		t.Errorf("the refused message left the queue:\n%s", out)
	}
	if code, _, errText := h.run("messages", "approve", "9", messages.Digest(longMessage)); code == 0 || !strings.Contains(errText, "no message 9") {
		t.Errorf("unknown id: %d %s", code, errText)
	}
	h.run("messages", "approve", "1", messages.Digest(longMessage))
	if code, _, errText := h.run("messages", "approve", "1", messages.Digest(longMessage)); code == 0 || !strings.Contains(errText, "not queued") {
		t.Errorf("approving twice: %d %s", code, errText)
	}
}

func TestMessagesApproveAllApprovesTheQueueTheListShowed(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.seedMessage(t, "1", "https://www.linkedin.com/in/ana", longMessage, false)
	h.seedMessage(t, "2", "https://www.linkedin.com/in/bia", "Oi Bia", false)

	_, out, _ := h.run("messages", "list")
	digest := approveAllDigest(t, out)
	if code, out, errText := h.run("messages", "approve", "--all", digest); code != 0 || !strings.Contains(out, "2 message(s)") {
		t.Fatalf("%d %s %s", code, out, errText)
	}
	if _, out, _ := h.run("messages", "list", "--state", "approved"); !strings.Contains(out, longMessage) || !strings.Contains(out, "Oi Bia") {
		t.Errorf("not all approved:\n%s", out)
	}
}

func TestMessagesApproveAllAfterTheQueueChangedApprovesNothing(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.seedMessage(t, "1", "https://www.linkedin.com/in/ana", longMessage, false)
	_, out, _ := h.run("messages", "list")
	digest := approveAllDigest(t, out)

	h.seedMessage(t, "2", "https://www.linkedin.com/in/bia", "Oi Bia", false)
	code, _, errText := h.run("messages", "approve", "--all", digest)
	if code == 0 || !strings.Contains(errText, "run ajai messages list again") {
		t.Errorf("stale list: %d %s", code, errText)
	}
	if _, out, _ := h.run("messages", "list", "--state", "approved"); !strings.Contains(out, "No recruiter messages in state approved.") {
		t.Errorf("something was approved:\n%s", out)
	}
}

func TestMessagesDeleteRemovesAQueuedRowAndRefusesASendingOne(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	h.seedMessage(t, "1", "https://www.linkedin.com/in/ana", longMessage, false)
	sending := h.seedMessage(t, "2", "https://www.linkedin.com/in/bia", "Oi Bia", true)
	h.withStore(t, func(st *store.Store) {
		if ok, err := st.MarkSending(sending); err != nil || !ok {
			t.Fatalf("mark sending: %v %v", ok, err)
		}
	})

	if code, out, _ := h.run("messages", "delete", "1"); code != 0 || !strings.Contains(out, "deleted") {
		t.Errorf("delete queued: %d %s", code, out)
	}
	if _, out, _ := h.run("messages", "list", "--state", "queued"); strings.Contains(out, longMessage) {
		t.Errorf("the deleted row is still listed:\n%s", out)
	}
	code, _, errText := h.run("messages", "delete", "2")
	if code == 0 || !strings.Contains(errText, "being sent") {
		t.Errorf("delete sending: %d %s", code, errText)
	}
	if _, out, _ := h.run("messages", "list", "--state", "sending"); !strings.Contains(out, "Oi Bia") {
		t.Errorf("the sending row is gone:\n%s", out)
	}
}

func TestMessagesHelpListsItsSubcommands(t *testing.T) {
	h := newHarness(t)
	_, out, _ := h.run("messages", "--help")
	for _, want := range []string{"list", "approve", "delete"} {
		if !strings.Contains(out, want) {
			t.Errorf("messages --help lacks %q:\n%s", want, out)
		}
	}
	if _, out, _ := h.run("help"); !strings.Contains(out, "messages") {
		t.Errorf("ajai help does not list messages:\n%s", out)
	}
}

func TestMessagesListNamesEachStateOnceWhenAStateIsUnknown(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	code, _, errText := h.run("messages", "list", "--state", "bogus")
	want := "--state takes queued, approved, sending, sent, failed, no_recipient"
	if got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(errText), "error: ")); code == 0 || got != want {
		t.Errorf("unknown state: code %d, error %q, want exactly %q", code, got, want)
	}
}

func TestMessagesDeleteSaysASentMessageStaysInTheHistory(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	id := h.seedMessage(t, "1", "https://www.linkedin.com/in/ana", "Oi Ana", true)
	h.withStore(t, func(st *store.Store) {
		for _, step := range []func(int64) (bool, error){st.MarkSending, st.MarkSent} {
			if ok, err := step(id); err != nil || !ok {
				t.Fatalf("send: ok=%v err=%v", ok, err)
			}
		}
	})
	code, _, errText := h.run("messages", "delete", fmt.Sprint(id))
	want := fmt.Sprintf("message %d was sent; it stays in the history", id)
	if got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(errText), "error: ")); code == 0 || got != want {
		t.Errorf("delete sent: code %d, error %q, want %q", code, got, want)
	}
	if _, out, _ := h.run("messages", "list", "--state", "sent"); !strings.Contains(out, "Oi Ana") {
		t.Errorf("the sent row is gone:\n%s", out)
	}
}
