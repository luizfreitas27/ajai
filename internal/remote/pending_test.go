package remote

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/notify"
)

// press queues a press of the button with data under message on, in chat c.
func (f *fakeTelegram) press(c int64, on outgoing, data string) {
	f.mu.Lock()
	f.nextID++
	f.queue = append(f.queue, notify.Update{UpdateID: f.nextID, CallbackQuery: &notify.CallbackQuery{
		ID: fmt.Sprint("cb", f.nextID), Data: data, Message: &notify.Message{MessageID: on.ID, Chat: notify.Chat{ID: c}, Text: on.Text}}})
	f.mu.Unlock()
	f.arrived <- struct{}{}
}

// replyTo queues a reply from chat c to message to.
func (f *fakeTelegram) replyTo(c, to int64, text string) {
	f.mu.Lock()
	f.nextID++
	f.queue = append(f.queue, notify.Update{UpdateID: f.nextID, Message: &notify.Message{MessageID: f.nextID, Date: time.Now().Unix(),
		Chat: notify.Chat{ID: c}, Text: text, ReplyTo: &notify.Message{MessageID: to, Chat: notify.Chat{ID: c}}}})
	f.mu.Unlock()
	f.arrived <- struct{}{}
}

type did struct {
	sent, edits []outgoing
	acks        []string
}

// until waits until cond holds for what the bot did, or fails.
func (f *fakeTelegram) until(t *testing.T, what string, cond func(d did) bool) did {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		d := did{append([]outgoing(nil), f.sent...), append([]outgoing(nil), f.edits...), append([]string(nil), f.acks...)}
		f.mu.Unlock()
		if cond(d) {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; sent %+v, edits %+v, acks %q", what, d.sent, d.edits, d.acks)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// cardFor finds the message showing the question label.
func cardFor(t *testing.T, sent []outgoing, label string) outgoing {
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
	mem                            *memory.Memory
	years, relocate, consent, city *memory.Remembered
}

// seed remembers an AI answer, a radio question with no answer yet, a
// suggested checkbox (all pending) and an approved answer.
func seed(t *testing.T, w *world) seeded {
	t.Helper()
	s := seeded{mem: memory.New(w.st, nil)}
	var err error
	remember := func(label, kind, answer, source, job string, options []string) *memory.Remembered {
		e, rerr := s.mem.Remember(label, kind, answer, source, job, options)
		if rerr != nil {
			err = rerr
		}
		return e
	}
	s.years = remember("Years of Go?", "text", "3", "ai", "https://www.linkedin.com/jobs/view/1/", nil)
	s.relocate = remember("Relocate?", "radio", "", "form", "https://www.linkedin.com/jobs/view/2/", []string{"Yes", "No, not now"})
	s.consent = remember("Send me updates", "checkbox", "checked", "form", "", nil)
	s.city = remember("City", "text", "Recife", "user", "", nil)
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
	d := w.tg.until(t, "three cards", func(d did) bool { return len(d.sent) >= 4 })
	if len(d.sent) != 4 {
		t.Errorf("the approved answer is not shown: %+v", d.sent)
	}
	years := cardFor(t, d.sent, "Years of Go?")
	for _, want := range []string{"Question (text): Years of Go?", "Answer: 3 (from the AI)", "Job: https://www.linkedin.com/jobs/view/1/",
		"Id: " + s.years.ID, "Approve keeps this answer; reply to this message to give another."} {
		if !strings.Contains(years.Text, want) {
			t.Errorf("%q lacks %q", years.Text, want)
		}
	}
	if got := buttonsOf(years); got != "Approve=approve:"+s.years.ID+" Delete=delete:"+s.years.ID {
		t.Errorf("buttons %q", got)
	}
	relocate := cardFor(t, d.sent, "Relocate?")
	for _, want := range []string{"Question (radio): Relocate?", "Options: Yes · No, not now", "Answer: none yet", "Reply to this message with the answer."} {
		if !strings.Contains(relocate.Text, want) {
			t.Errorf("%q lacks %q", relocate.Text, want)
		}
	}
	if got := buttonsOf(relocate); got != "Delete=delete:"+s.relocate.ID {
		t.Errorf("a question with no answer yet has only Delete: %q", got)
	}
	if consent := cardFor(t, d.sent, "Send me updates"); !strings.Contains(consent.Text, "Answer: tick it") || len(consent.Buttons) != 2 {
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
	d := w.tg.until(t, "the cards", func(d did) bool { return len(d.sent) >= 3 })
	years, relocate, consent := cardFor(t, d.sent, "Years of Go?"), cardFor(t, d.sent, "Relocate?"), cardFor(t, d.sent, "Send me updates")

	w.tg.press(chat, years, "approve:"+s.years.ID)
	d = w.tg.until(t, "approve", func(d did) bool { return len(d.acks) >= 1 })
	if d.acks[0] != "Approved." || len(d.edits) != 1 || d.edits[0].ID != years.ID ||
		!strings.Contains(d.edits[0].Text, "Approved: aja uses this answer from now on.") || d.edits[0].Buttons == nil || len(d.edits[0].Buttons) != 0 {
		t.Errorf("acks %q edits %+v", d.acks, d.edits)
	}
	if e, _ := s.mem.Get(s.years.ID); e.State != "approved" || e.Answer != "3" || e.Source != "ai" {
		t.Errorf("approved as-is keeps who answered: %+v", e)
	}

	w.tg.press(chat, consent, "delete:"+s.consent.ID)
	d = w.tg.until(t, "delete", func(d did) bool { return len(d.acks) >= 2 })
	if d.acks[1] != "Deleted." || len(d.edits) != 2 || d.edits[1].ID != consent.ID || !strings.Contains(d.edits[1].Text, "Deleted: aja forgets this answer.") {
		t.Errorf("acks %q edits %+v", d.acks, d.edits)
	}
	if e, _ := s.mem.Get(s.consent.ID); e != nil {
		t.Errorf("not deleted: %+v", e)
	}

	// Approve on a question with no answer (an old message's button), a button
	// on a deleted answer, and a press in another chat.
	w.tg.press(chat, relocate, "approve:"+s.relocate.ID)
	w.tg.press(chat+1, years, "delete:"+s.years.ID)
	w.tg.press(chat, consent, "delete:"+s.consent.ID)
	d = w.tg.until(t, "the last presses", func(d did) bool { return len(d.acks) >= 4 })
	if d.acks[2] != "Reply to the message with the answer first." || d.acks[3] != "Already gone." {
		t.Errorf("acks %q", d.acks)
	}
	if len(d.edits) != 3 || d.edits[2].ID != consent.ID || !strings.HasSuffix(d.edits[2].Text, "No longer in the answer memory.") {
		t.Errorf("edits %+v", d.edits)
	}
	if e, _ := s.mem.Get(s.years.ID); e == nil {
		t.Error("a press in another chat deleted an answer")
	}
}

func TestARepliedAnswerIsApprovedAndCheckedAgainstTheOptions(t *testing.T) {
	w := newWorld(t)
	s := seed(t, w)
	w.listen()
	w.tg.say(chat, "/pending", w.now)
	d := w.tg.until(t, "the cards", func(d did) bool { return len(d.sent) >= 3 })
	years, relocate, consent := cardFor(t, d.sent, "Years of Go?"), cardFor(t, d.sent, "Relocate?"), cardFor(t, d.sent, "Send me updates")

	w.tg.replyTo(chat, relocate.ID, "maybe")
	m := w.tg.waitFor(t, 4)
	if m[3] != `"maybe" is not one of the options: Yes · No, not now. Reply again with one of them.` {
		t.Errorf("%q", m[3])
	}
	if e, _ := s.mem.Get(s.relocate.ID); e.Answer != "" || e.State != "pending" {
		t.Errorf("a refused reply changed the answer: %+v", e)
	}

	w.tg.replyTo(chat, relocate.ID, "no, NOT now")
	d = w.tg.until(t, "the answer", func(d did) bool { return len(d.edits) >= 1 })
	if d.edits[0].ID != relocate.ID || !strings.Contains(d.edits[0].Text, "Answer: No, not now (yours)") ||
		!strings.Contains(d.edits[0].Text, "Answered: aja uses this answer from now on.") || len(d.edits[0].Buttons) != 0 {
		t.Errorf("%+v", d.edits)
	}
	if e, _ := s.mem.Get(s.relocate.ID); e.Answer != "No, not now" || e.State != "approved" || e.Source != "user" {
		t.Errorf("the option's own text is saved: %+v", e)
	}

	w.tg.replyTo(chat, years.ID, " 4 ")
	d = w.tg.until(t, "the second answer", func(d did) bool { return len(d.edits) >= 2 })
	if e, _ := s.mem.Get(s.years.ID); e.Answer != "4" || e.State != "approved" || e.Source != "user" {
		t.Errorf("%+v", e)
	}

	w.tg.replyTo(chat, consent.ID, "yes")
	w.tg.replyTo(chat, d.sent[3].ID, "hello") // the bot's refusal above: not a card
	w.tg.say(chat, "just chatting", w.now)
	w.tg.say(chat, "/status", w.now)
	m = w.tg.waitFor(t, 7)
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
	d := w.tg.until(t, "the cards", func(d did) bool { return len(d.sent) >= 3 })
	years, relocate := cardFor(t, d.sent, "Years of Go?"), cardFor(t, d.sent, "Relocate?")
	w.stop()

	w.listen() // a new daemon, the same store
	w.tg.replyTo(chat, relocate.ID, "Yes")
	w.tg.press(chat, years, "approve:"+s.years.ID)
	w.tg.until(t, "both acted on", func(d did) bool { return len(d.edits) >= 2 && len(d.acks) >= 1 })
	if e, _ := s.mem.Get(s.relocate.ID); e.Answer != "Yes" || e.State != "approved" {
		t.Errorf("%+v", e)
	}
	if e, _ := s.mem.Get(s.years.ID); e.State != "approved" {
		t.Errorf("%+v", e)
	}
	if n := len(w.tg.messages()); n != 3 {
		t.Errorf("the restart replayed /pending: %d messages", n)
	}
}

func TestPendingShowsTheNewestTenAndSaysHowManyMore(t *testing.T) {
	w := newWorld(t)
	mem := memory.New(w.st, nil)
	for i := 1; i <= 12; i++ {
		if _, err := mem.Remember(fmt.Sprintf("Question %d", i), "text", "", "form", "", nil); err != nil {
			t.Fatal(err)
		}
	}
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
}
