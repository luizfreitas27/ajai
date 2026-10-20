package store

import (
	"errors"
	"fmt"
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

func TestInsertMessageGivesTheNewRowsIDOnlyWhenItInserts(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	m := queued("1", "https://www.linkedin.com/in/ana", "Oi")
	m.State = MessageApproved
	id, err := s.InsertMessage(m)
	must(t, err)
	got, err := s.GetMessage(id)
	must(t, err)
	if got == nil || got.State != MessageApproved || got.Text != "Oi" {
		t.Fatalf("message %d = %+v, want the approved row", id, got)
	}
	again, err := s.InsertMessage(m)
	must(t, err)
	if again != 0 {
		t.Errorf("a second insert for the same job and recipient gave id %d, want 0", again)
	}
}

func TestHasMessagedSeesTheRecruiterOnAnyJob(t *testing.T) {
	const ana, bia = "https://www.linkedin.com/in/ana", "https://www.linkedin.com/in/bia"
	s := open(t)
	for _, j := range []string{"1", "2", "3", "4"} {
		seedJob(t, s, j)
	}
	has := func(url string, except int64) bool {
		t.Helper()
		ok, err := s.HasMessaged(url, except)
		must(t, err)
		return ok
	}
	q, err := s.InsertMessage(queued("1", ana, "Oi"))
	must(t, err)
	if has(ana, 0) {
		t.Error("a queued message counts as messaged")
	}
	must(t, s.ApproveMessage(q, messages.Digest("Oi")))
	if !has(ana, 0) {
		t.Error("an approved message does not count for a new message")
	}
	if has(ana, q) {
		t.Error("a message counts against itself")
	}
	newer, err := s.InsertMessage(Message{JobID: "2", RecipientURL: ana, Text: "Oi", State: MessageApproved})
	must(t, err)
	if !has(ana, newer) {
		t.Error("an older approved message does not count against a newer one")
	}
	if has(ana, q) {
		t.Error("a newer approved message counts against the older one, so neither would be sent")
	}
	if ok, err := s.MarkSending(q); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !has(ana, newer) {
		t.Error("a sending message does not count")
	}
	if ok, err := s.MarkSent(q); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !has(ana, newer) || has(bia, 0) || has("", 0) {
		t.Error("a sent message counts only for its own recruiter")
	}
	if ok, err := s.DropApproved(newer, "already messaged this recruiter"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got, _ := s.GetMessage(newer); got.State != MessageNoRecipient || got.Error != "already messaged this recruiter" {
		t.Errorf("dropped message %+v", got)
	}
	skipped, err := s.InsertMessage(Message{JobID: "3", RecipientURL: ana, State: MessageNoRecipient, Error: "already messaged this recruiter"})
	must(t, err)
	if got, _ := s.GetMessage(skipped); got.Error != "already messaged this recruiter" {
		t.Errorf("inserted %+v, want its reason kept", got)
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
	// A fixed zone, not the machine's: the test means the same under TZ=UTC.
	brt := time.FixedZone("BRT", -3*3600)
	prevLocal := time.Local
	time.Local = brt
	t.Cleanup(func() { time.Local = prevLocal })

	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")
	at := func(day, hour, min int) time.Time { return time.Date(2026, 10, day, hour, min, 0, 0, brt) }

	// 20:30 and 21:30 BRT on 20 Oct are 23:30 UTC on 20 Oct and 00:30 UTC on 21 Oct:
	// the two straddle midnight UTC, but both are 20 Oct in BRT.
	sentAt := []time.Time{at(20, 20, 30), at(20, 21, 30)}
	for i, job := range []string{"1", "2"} {
		s.now = func() time.Time { return sentAt[i] }
		_, _ = s.QueueMessage(queued(job, "https://www.linkedin.com/in/p"+job, "Oi"))
		m, _ := s.ListMessages("")
		must(t, s.ApproveMessage(m[i].ID, messages.Digest("Oi")))
		_, _ = s.MarkSending(m[i].ID)
		_, _ = s.MarkSent(m[i].ID)
	}
	s.now = func() time.Time { return at(20, 21, 45) }
	if n, _ := s.SentToday(); n != 2 {
		t.Fatalf("SentToday at 21:45 BRT on 20 Oct = %d, want 2", n)
	}
	s.now = func() time.Time { return at(21, 0, 1) }
	if n, _ := s.SentToday(); n != 0 {
		t.Errorf("SentToday after local midnight = %d, want 0", n)
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

func TestASendingMessageLeftByADeadProcessEndsFailedAndIsNeverOffered(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")
	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	_, _ = s.QueueMessage(queued("2", "https://www.linkedin.com/in/bruno", "Ola"))
	msgs, _ := s.ListMessages("")
	must(t, s.ApproveMessage(msgs[0].ID, messages.Digest("Oi")))
	must(t, s.ApproveMessage(msgs[1].ID, messages.Digest("Ola")))
	if ok, err := s.MarkSending(msgs[0].ID); err != nil || !ok {
		t.Fatalf("MarkSending: ok=%v err=%v", ok, err)
	}

	n, err := s.FailSendingMessages(InterruptedSend)
	must(t, err)
	if n != 1 {
		t.Fatalf("FailSendingMessages = %d, want 1", n)
	}
	m, _ := s.GetMessage(msgs[0].ID)
	if m.State != MessageFailed || m.Error != "interrupted while sending; it may have been sent" {
		t.Errorf("orphaned sending row: %+v, want failed with the interrupted reason", m)
	}
	if next, _ := s.NextApproved(10); len(next) != 1 || next[0].ID != msgs[1].ID {
		t.Errorf("NextApproved = %+v, want only the other approved message", next)
	}
	if n, _ := s.FailSendingMessages(InterruptedSend); n != 0 {
		t.Errorf("a second FailSendingMessages changed %d rows, want 0", n)
	}
}

func TestApprovingAMessageDeletedAfterTheListNeverSucceeds(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	id := mustID(t, s)
	digest := messages.Digest("Oi") // what list showed
	if ok, err := s.DeleteMessage(id); err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}

	if err := s.ApproveMessage(id, digest); !errors.Is(err, ErrNoMessage) {
		t.Errorf("approve after delete: err = %v, want ErrNoMessage", err)
	}
}

func TestASentMessageCannotBeDeletedSoItKeepsCountingTowardTheDailyCap(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	_, _ = s.QueueMessage(queued("1", "https://www.linkedin.com/in/ana", "Oi"))
	id := mustID(t, s)
	must(t, s.ApproveMessage(id, messages.Digest("Oi")))
	if ok, err := s.MarkSending(id); err != nil || !ok {
		t.Fatalf("MarkSending: ok=%v err=%v", ok, err)
	}
	if ok, err := s.MarkSent(id); err != nil || !ok {
		t.Fatalf("MarkSent: ok=%v err=%v", ok, err)
	}

	if ok, _ := s.DeleteMessage(id); ok {
		t.Fatal("a sent message was deleted")
	}
	if m, _ := s.GetMessage(id); m == nil || m.State != MessageSent {
		t.Errorf("after the refused delete: %+v, want the sent row kept", m)
	}
	if n, _ := s.SentToday(); n != 1 {
		t.Errorf("SentToday = %d, want 1", n)
	}
}

// codedErr stands in for modernc's *sqlite.Error, whose code cannot be set from outside its package.
type codedErr struct{ code int }

func (e *codedErr) Error() string { return "sqlite error" }
func (e *codedErr) Code() int     { return e.code }

func TestABusyDatabaseErrorIsRecognisedWhateverItsExtendedCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"SQLITE_BUSY", &codedErr{5}, true},
		{"SQLITE_BUSY_SNAPSHOT", &codedErr{517}, true},
		{"busy, wrapped", fmt.Errorf("update: %w", &codedErr{517}), true},
		{"SQLITE_LOCKED is not busy", &codedErr{6}, false},
		{"a constraint error", &codedErr{19}, false},
		{"not a sqlite error", errors.New("boom"), false},
		{"no error", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBusy(tc.err); got != tc.want {
				t.Errorf("isBusy(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
