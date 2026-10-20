package store

import (
	"errors"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/messages"
)

// seedJob records one applied job, the row a recruiter message hangs off.
func seedJob(t *testing.T, s *Store, id string) {
	t.Helper()
	must(t, s.UpsertJob(Job{JobID: id, Outcome: Applied, Title: "Dev", Company: "Acme"}))
}

func queued(job, url, text string) Message {
	return Message{JobID: job, RecipientName: "Ana", RecipientURL: url, Text: text, State: MessageQueued}
}

func TestAMessageIsQueuedOncePerJobAndRecipient(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")

	ok, err := s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	must(t, err)
	if !ok {
		t.Fatal("first queue: not inserted")
	}
	again, err := s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi de novo"))
	must(t, err)
	if again {
		t.Error("second queue for the same job and recipient was inserted")
	}
	other, err := s.QueueMessage(queued("1", "https://www.linkedin.com/in/bruno", "Oi"))
	must(t, err)
	if !other {
		t.Error("another recipient of the same job was refused")
	}
	sameRecipientOtherJob, err := s.QueueMessage(queued("2", "https://www.linkedin.com/in/ana", "Oi"))
	must(t, err)
	if !sameRecipientOtherJob {
		t.Error("the same recipient on another job was refused")
	}

	all, err := s.ListMessages("")
	must(t, err)
	if len(all) != 3 {
		t.Fatalf("stored %d messages, want 3", len(all))
	}
	if all[0].Text != "Oi" {
		t.Errorf("the first queue kept text %q, want the first text", all[0].Text)
	}
}

func TestANoRecipientRowIsRecordedOncePerJob(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	none := Message{JobID: "1", State: MessageNoRecipient}
	ok, err := s.QueueMessage(none)
	must(t, err)
	if !ok {
		t.Fatal("no_recipient row not inserted")
	}
	again, err := s.QueueMessage(none)
	must(t, err)
	if again {
		t.Error("a second no_recipient row for the same job was inserted")
	}
	rows, _ := s.ListMessages(string(MessageNoRecipient))
	if len(rows) != 1 || rows[0].RecipientURL != "" {
		t.Errorf("rows = %+v, want one with a blank recipient URL", rows)
	}
}

func TestApproveAcceptsOnlyTheTextTheUserRead(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")
	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	msgs, _ := s.ListMessages("")
	id := msgs[0].ID

	if err := s.ApproveMessage(id, "00000000"); !errors.Is(err, ErrTextChanged) {
		t.Errorf("stale digest: err = %v, want ErrTextChanged", err)
	}
	if m, _ := s.GetMessage(id); m.State != MessageQueued {
		t.Errorf("a refused approval moved the message to %q", m.State)
	}
	if err := s.ApproveMessage(id, messages.Digest("Oi")); err != nil {
		t.Fatalf("approve with the shown digest: %v", err)
	}
	if err := s.ApproveMessage(id, messages.Digest("Oi")); !errors.Is(err, ErrNotQueued) {
		t.Errorf("approving twice: err = %v, want ErrNotQueued", err)
	}
	if err := s.ApproveMessage(999, "x"); !errors.Is(err, ErrNoMessage) {
		t.Errorf("unknown id: err = %v, want ErrNoMessage", err)
	}
}

func TestApproveAllNeedsTheListDigestOfTheQueueAsItWas(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")
	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	_, _ = s.QueueMessage(queued("2", "https://www.linkedin.com/in/bruno", "Ola"))
	shown := listDigestOf(t, s)

	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/carla", "Oi Carla"))
	if n, err := s.ApproveAll(shown); !errors.Is(err, ErrQueueChanged) || n != 0 {
		t.Fatalf("queue grew after the list: n=%d err=%v, want 0 and ErrQueueChanged", n, err)
	}
	if left, _ := s.ListMessages(MessageQueued); len(left) != 3 {
		t.Errorf("a refused approve-all still moved messages: %d queued, want 3", len(left))
	}

	n, err := s.ApproveAll(listDigestOf(t, s))
	if err != nil || n != 3 {
		t.Fatalf("approve-all with the current list: n=%d err=%v, want 3", n, err)
	}
	if approved, _ := s.ListMessages(MessageApproved); len(approved) != 3 {
		t.Errorf("%d approved, want 3", len(approved))
	}
}

func TestASendingMessageIsNeverOfferedAgain(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	id := mustID(t, s)
	if ok, _ := s.MarkSending(id); ok {
		t.Fatal("a queued message went to sending without approval")
	}
	must(t, s.ApproveMessage(id, messages.Digest("Oi")))

	if next, _ := s.NextApproved(10); len(next) != 1 {
		t.Fatalf("approved messages offered: %d, want 1", len(next))
	}
	if ok, err := s.MarkSending(id); err != nil || !ok {
		t.Fatalf("MarkSending from approved: ok=%v err=%v", ok, err)
	}
	if next, _ := s.NextApproved(10); len(next) != 0 {
		t.Errorf("a sending message offered again: %+v", next)
	}
	if ok, _ := s.MarkSending(id); ok {
		t.Error("MarkSending succeeded twice")
	}
	if ok, _ := s.MarkFailed(id, "boom"); !ok {
		t.Fatal("MarkFailed from sending refused")
	}
	if m, _ := s.GetMessage(id); m.State != MessageFailed || m.Error != "boom" {
		t.Errorf("after failure: %+v", m)
	}
}

func TestSentTodayCountsByLocalDateAcrossMidnight(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")
	seedJob(t, s, "3")
	clock := time.Date(2026, 10, 20, 23, 59, 0, 0, time.Local)
	s.now = func() time.Time { return clock }

	for i, job := range []string{"1", "2"} {
		_, _ = s.QueueMessage(queued(job, "https://www.linkedin.com/in/p"+job, "Oi"))
		m, _ := s.ListMessages("")
		must(t, s.ApproveMessage(m[i].ID, messages.Digest("Oi")))
		_, _ = s.MarkSending(m[i].ID)
		_, _ = s.MarkSent(m[i].ID)
	}
	if n, _ := s.SentToday(); n != 2 {
		t.Fatalf("SentToday at 23:59 = %d, want 2", n)
	}
	clock = time.Date(2026, 10, 21, 0, 1, 0, 0, time.Local)
	if n, _ := s.SentToday(); n != 0 {
		t.Errorf("SentToday after midnight = %d, want 0", n)
	}
}

func listDigestOf(t *testing.T, s *Store) string {
	t.Helper()
	queue, err := s.ListMessages(MessageQueued)
	must(t, err)
	return messages.ListDigest(messagesOf(queue))
}

func messagesOf(queue []Message) []messages.Item {
	items := make([]messages.Item, len(queue))
	for i, m := range queue {
		items[i] = messages.Item{ID: m.ID, Digest: messages.Digest(m.Text)}
	}
	return items
}

func mustID(t *testing.T, s *Store) int64 {
	t.Helper()
	m, err := s.ListMessages("")
	must(t, err)
	if len(m) == 0 {
		t.Fatal("no message")
	}
	return m[0].ID
}

func TestAMessageWhoseHirerChangedEndsNoRecipientAndIsNeverOffered(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	id := mustID(t, s)
	must(t, s.ApproveMessage(id, messages.Digest("Oi")))
	if ok, _ := s.MarkNoRecipient(id, "not sent"); ok {
		t.Fatal("MarkNoRecipient from approved (never sending) was accepted")
	}
	if ok, err := s.MarkSending(id); err != nil || !ok {
		t.Fatalf("MarkSending: ok=%v err=%v", ok, err)
	}
	if ok, err := s.MarkNoRecipient(id, "the hirer changed"); err != nil || !ok {
		t.Fatalf("MarkNoRecipient from sending: ok=%v err=%v", ok, err)
	}
	m, _ := s.GetMessage(id)
	if m.State != MessageNoRecipient || m.Error != "the hirer changed" || m.SentAt != "" {
		t.Errorf("after no recipient: %+v", m)
	}
	if next, _ := s.NextApproved(10); len(next) != 0 {
		t.Errorf("a no_recipient message offered: %+v", next)
	}
}
