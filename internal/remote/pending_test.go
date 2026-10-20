package remote

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/memory"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/notify"
	"github.com/luizfreitas27/ajai/internal/store"
)

// press queues a press of the button with data under message on, in chat c.
func (f *fakeTelegram) press(c int64, on outgoing, data string) {
	f.add(func(id int64) notify.Update {
		return notify.Update{UpdateID: id, CallbackQuery: &notify.CallbackQuery{ID: fmt.Sprint("cb", id), Data: data,
			Message: &notify.Message{MessageID: on.ID, Chat: notify.Chat{ID: c}, Text: on.Text}}}
	})
}

// replyTo queues a reply from chat c to message to.
func (f *fakeTelegram) replyTo(c, to int64, text string) {
	f.add(func(id int64) notify.Update {
		return notify.Update{UpdateID: id, Message: &notify.Message{MessageID: id, Date: time.Now().Unix(), Chat: notify.Chat{ID: c}, Text: text,
			ReplyTo: &notify.Message{MessageID: to, Chat: notify.Chat{ID: c}}}}
	})
}

// messageFor finds the message showing the question label.
func messageFor(t *testing.T, sent []outgoing, label string) outgoing {
	t.Helper()
	for _, o := range sent {
		if strings.Contains(o.Text, "): "+label+"\n") {
			return o
		}
	}
	t.Fatalf("no message for %q in %+v", label, sent)
	return outgoing{}
}

type seeded struct {
	mem                      *memory.Memory
	years, relocate, consent *memory.Remembered
}

// seed remembers an AI answer, a radio question with no answer yet and a
// suggested checkbox (all pending), and an approved answer /pending leaves out.
func seed(t *testing.T, w *world) seeded {
	t.Helper()
	s := seeded{mem: memory.New(w.st, nil)}
	var err error
	remember := func(label, kind, answer, source, job string, options []string) *memory.Remembered {
		a, rerr := s.mem.Remember(label, kind, answer, source, job, options)
		if rerr != nil {
			err = rerr
		}
		return a
	}
	s.years = remember("Years of Go?", "text", "3", "ai", "https://www.linkedin.com/jobs/view/1/", nil)
	s.relocate = remember("Relocate?", "radio", "", "form", "https://www.linkedin.com/jobs/view/2/", []string{"Yes", "No, not now"})
	s.consent = remember("Send me updates", "checkbox", "checked", "form", "", nil)
	remember("City", "text", "Recife", "user", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func buttonsOf(o outgoing) string {
	var parts []string
	for _, b := range o.Buttons {
		parts = append(parts, b.Text+"="+b.Data)
	}
	return strings.Join(parts, " ")
}

func TestPendingSendsOneMessagePerPendingAnswerWithItsButtons(t *testing.T) {
	w := newWorld(t)
	w.listen()
	w.tg.say(chat, "/pending", w.now)
	if m := w.tg.waitFor(t, 1); m[0] != "Nothing pending: every remembered answer is approved." {
		t.Errorf("%q", m)
	}

	s := seed(t, w)
	w.tg.say(chat, "/pending", w.now)
	d := w.tg.until(t, "three messages", func(d did) bool { return len(d.sent) >= 4 })
	if len(d.sent) != 4 {
		t.Errorf("the approved answer is not shown: %+v", d.sent)
	}
	years := messageFor(t, d.sent, "Years of Go?")
	for _, want := range []string{"Question (text): Years of Go?", "Answer: 3 (from the AI)", "Job: https://www.linkedin.com/jobs/view/1/",
		"Id: " + s.years.ID, "Approve keeps this answer; reply to this message to give another."} {
		if !strings.Contains(years.Text, want) {
			t.Errorf("%q lacks %q", years.Text, want)
		}
	}
	if got := buttonsOf(years); got != "Approve=approve:"+s.years.ID+":"+messages.Digest("3")+" Delete=delete:"+s.years.ID {
		t.Errorf("buttons %q", got)
	}
	relocate := messageFor(t, d.sent, "Relocate?")
	for _, want := range []string{"Question (radio): Relocate?", "Options: Yes · No, not now", "Answer: none yet", "Reply to this message with the answer."} {
		if !strings.Contains(relocate.Text, want) {
			t.Errorf("%q lacks %q", relocate.Text, want)
		}
	}
	if got := buttonsOf(relocate); got != "Delete=delete:"+s.relocate.ID {
		t.Errorf("a question with no answer yet has only Delete: %q", got)
	}
	if consent := messageFor(t, d.sent, "Send me updates"); !strings.Contains(consent.Text, "Answer: tick it") || len(consent.Buttons) != 2 {
		t.Errorf("%+v", consent)
	}

	w.tg.say(chat, "/status", w.now)
	w.tg.say(chat, "/help", w.now)
	m := w.tg.waitFor(t, 6)
	if !strings.Contains(m[4], "Pending answers: 3\n/pending reviews them.") {
		t.Errorf("status: %q", m[4])
	}
	if !strings.Contains(m[5], "/pending: review the pending answers") {
		t.Errorf("help: %q", m[5])
	}
}

func TestButtonsApproveOrDeleteAndEditTheMessage(t *testing.T) {
	w := newWorld(t)
	s := seed(t, w)
	w.listen()
	w.tg.say(chat, "/pending", w.now)
	d := w.tg.until(t, "the messages", func(d did) bool { return len(d.sent) >= 3 })
	years, relocate, consent := messageFor(t, d.sent, "Years of Go?"), messageFor(t, d.sent, "Relocate?"), messageFor(t, d.sent, "Send me updates")

	w.tg.press(chat, years, years.Buttons[0].Data)
	d = w.tg.until(t, "approve", func(d did) bool { return len(d.acks) >= 1 })
	if d.acks[0] != "Approved." || len(d.edits) != 1 || d.edits[0].ID != years.ID ||
		!strings.Contains(d.edits[0].Text, "Approved: ajai uses this answer from now on.") || d.edits[0].Buttons == nil || len(d.edits[0].Buttons) != 0 {
		t.Errorf("acks %q edits %+v", d.acks, d.edits)
	}
	if a, _ := s.mem.Get(s.years.ID); a.State != "approved" || a.Answer != "3" || a.Source != "ai" {
		t.Errorf("approved as-is keeps who answered: %+v", a)
	}

	w.tg.press(chat, consent, "delete:"+s.consent.ID)
	d = w.tg.until(t, "delete", func(d did) bool { return len(d.acks) >= 2 })
	if d.acks[1] != "Deleted." || len(d.edits) != 2 || d.edits[1].ID != consent.ID || !strings.Contains(d.edits[1].Text, "Deleted: ajai forgets this answer.") {
		t.Errorf("acks %q edits %+v", d.acks, d.edits)
	}
	if a, _ := s.mem.Get(s.consent.ID); a != nil {
		t.Errorf("not deleted: %+v", a)
	}

	// Approve on a question with no answer (a forged button), a button on a
	// deleted answer, a press in another chat, and an unknown button.
	w.tg.press(chat, relocate, "approve:"+s.relocate.ID+":"+messages.Digest(""))
	w.tg.press(chat+1, years, "delete:"+s.years.ID)
	w.tg.press(chat, consent, "delete:"+s.consent.ID)
	w.tg.press(chat, years, "approve:"+s.years.ID)
	d = w.tg.until(t, "the last presses", func(d did) bool { return len(d.acks) >= 5 })
	if d.acks[2] != "Reply to the message with the answer first." || d.acks[3] != "Already gone." || d.acks[4] != "ajai does not know this button." {
		t.Errorf("acks %q", d.acks)
	}
	if len(d.edits) != 3 || d.edits[2].ID != consent.ID || !strings.HasSuffix(d.edits[2].Text, "No longer in the answer memory.") {
		t.Errorf("edits %+v", d.edits)
	}
	if a, _ := s.mem.Get(s.years.ID); a == nil {
		t.Error("a press in another chat deleted an answer")
	}
}

func TestApproveNeverApprovesAnAnswerTheMessageDidNotShow(t *testing.T) {
	w := newWorld(t)
	s := seed(t, w)
	w.listen()
	w.tg.say(chat, "/pending", w.now)
	d := w.tg.until(t, "the messages", func(d did) bool { return len(d.sent) >= 3 })
	years := messageFor(t, d.sent, "Years of Go?")

	// A run answers the question again while the message waits on the phone.
	if _, err := s.mem.Remember("Years of Go?", "text", "5", "ai", "", nil); err != nil {
		t.Fatal(err)
	}
	w.tg.press(chat, years, years.Buttons[0].Data)
	d = w.tg.until(t, "the refusal", func(d did) bool { return len(d.acks) >= 1 })
	if d.acks[0] != "The answer changed since this message; check it again." || len(d.edits) != 1 ||
		!strings.Contains(d.edits[0].Text, "Answer: 5 (from the AI)") || buttonsOf(d.edits[0]) != "Approve=approve:"+s.years.ID+":"+messages.Digest("5")+" Delete=delete:"+s.years.ID {
		t.Errorf("acks %q edits %+v", d.acks, d.edits)
	}
	if a, _ := s.mem.Get(s.years.ID); a.State != "pending" {
		t.Errorf("approved an answer the message did not show: %+v", a)
	}

	w.tg.press(chat, d.edits[0], d.edits[0].Buttons[0].Data)
	w.tg.until(t, "the approval", func(d did) bool { return len(d.acks) >= 2 })
	if a, _ := s.mem.Get(s.years.ID); a.State != "approved" || a.Answer != "5" {
		t.Errorf("%+v", a)
	}
}

func TestARepliedAnswerIsApprovedAndCheckedAgainstTheOptions(t *testing.T) {
	w := newWorld(t)
	s := seed(t, w)
	w.listen()
	w.tg.say(chat, "/pending", w.now)
	d := w.tg.until(t, "the messages", func(d did) bool { return len(d.sent) >= 3 })
	years, relocate, consent := messageFor(t, d.sent, "Years of Go?"), messageFor(t, d.sent, "Relocate?"), messageFor(t, d.sent, "Send me updates")

	w.tg.replyTo(chat, relocate.ID, "maybe")
	d = w.tg.until(t, "the refusal", func(d did) bool { return len(d.sent) >= 4 })
	if d.sent[3].Text != `"maybe" is not one of the options: Yes · No, not now. Reply again with one of them, or its beginning.` {
		t.Errorf("%q", d.sent[3].Text)
	}
	if a, _ := s.mem.Get(s.relocate.ID); a.Answer != "" || a.State != "pending" {
		t.Errorf("a refused reply changed the answer: %+v", a)
	}

	w.tg.replyTo(chat, relocate.ID, "no,  NOT")
	d = w.tg.until(t, "the answer", func(d did) bool { return len(d.edits) >= 1 })
	if d.edits[0].ID != relocate.ID || !strings.Contains(d.edits[0].Text, "Answer: No, not now (yours)") ||
		!strings.Contains(d.edits[0].Text, "Answered: ajai uses this answer from now on.") || len(d.edits[0].Buttons) != 0 {
		t.Errorf("%+v", d.edits)
	}
	if a, _ := s.mem.Get(s.relocate.ID); a.Answer != "No, not now" || a.State != "approved" || a.Source != "user" {
		t.Errorf("the option's own text is saved: %+v", a)
	}

	w.tg.replyTo(chat, years.ID, " 4 ")
	w.tg.until(t, "the second answer", func(d did) bool { return len(d.edits) >= 2 })
	if a, _ := s.mem.Get(s.years.ID); a.Answer != "4" || a.State != "approved" || a.Source != "user" {
		t.Errorf("%+v", a)
	}

	w.tg.replyTo(chat, consent.ID, "yes")
	w.tg.replyTo(chat, d.sent[3].ID, "hello") // the bot's refusal above
	w.tg.say(chat, "just chatting", w.now)
	w.tg.say(chat, "/status", w.now)
	m := w.tg.waitFor(t, 7)
	if m[4] != "A checkbox takes no text: Approve ticks it from now on, Delete forgets it." {
		t.Errorf("%q", m[4])
	}
	if m[5] != "That message is not a pending answer; /pending lists them." {
		t.Errorf("%q", m[5])
	}
	if !strings.HasPrefix(m[6], "No run in progress.") {
		t.Errorf("plain chatter gets no answer: %q", m[6:])
	}
}

func TestRepliesAndButtonsStillWorkAfterARestart(t *testing.T) {
	w := newWorld(t)
	s := seed(t, w)
	w.listen()
	w.tg.say(chat, "/pending", w.now)
	d := w.tg.until(t, "the messages", func(d did) bool { return len(d.sent) >= 3 })
	years, relocate := messageFor(t, d.sent, "Years of Go?"), messageFor(t, d.sent, "Relocate?")
	w.stop()

	// A new daemon: the database opened afresh.
	if err := w.st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(w.dir, "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w.st = st
	mem := memory.New(st, nil)
	w.listen()
	w.tg.replyTo(chat, relocate.ID, "Yes")
	w.tg.press(chat, years, years.Buttons[0].Data)
	w.tg.until(t, "both acted on", func(d did) bool { return len(d.edits) >= 2 && len(d.acks) >= 1 })
	if a, _ := mem.Get(s.relocate.ID); a.Answer != "Yes" || a.State != "approved" {
		t.Errorf("%+v", a)
	}
	if a, _ := mem.Get(s.years.ID); a.State != "approved" {
		t.Errorf("%+v", a)
	}
	if n := len(w.tg.messages()); n != 3 {
		t.Errorf("the restart replayed /pending: %d messages", n)
	}
}

func TestPendingShowsTheNewestTenAndWaitsWhenTelegramSaysTooMany(t *testing.T) {
	w := newWorld(t)
	mem := memory.New(w.st, nil)
	for i := 1; i <= 12; i++ {
		if _, err := mem.Remember(fmt.Sprintf("Question %d", i), "text", "", "form", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	w.tg.tooMany = 1
	w.listen()
	w.tg.say(chat, "/pending", w.now)
	m := w.tg.waitFor(t, 11)
	if m[10] != "Those are the 10 newest of 12 pending. Review them, then /pending shows the next." {
		t.Errorf("%q", m[10])
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(w.tg.messages()); n != 11 {
		t.Errorf("%d messages", n)
	}
	w.logMu.Lock()
	defer w.logMu.Unlock()
	if len(w.backoff) != 1 || w.backoff[0] != 3*time.Second {
		t.Errorf("waited %v, want once the 3s Telegram asked for", w.backoff)
	}
}
