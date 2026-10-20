package run

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/pacing"
	"github.com/luizfreitas27/ajai/internal/store"
)

var ana = messages.Recipient{Name: "Ana Silva", URL: "https://www.linkedin.com/in/ana"}

// messagesCfg turns recruiter messages on for the internacional market, with a
// template that names every placeholder and no pauses.
func messagesCfg() config.Config {
	c := cfg()
	c.Messages = config.Messages{Enabled: true, MaxPerDay: 3}
	c.Markets.Active = "internacional"
	c.Markets.Internacional.MessageTemplate = "Hi {first_name}, I applied for {title} at {company}."
	return c
}

func allMessages(t *testing.T, st *store.Store) []store.Message {
	t.Helper()
	msgs, err := st.ListMessages("")
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestAnAppliedJobWithAHirerQueuesTheRenderedMessage(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
	r, st, _ := runner(t, site, messagesCfg())
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 1 {
		t.Fatalf("applied %d, err %v", sum.Applied, err)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 1 {
		t.Fatalf("%d messages queued, want 1", len(msgs))
	}
	m := msgs[0]
	if m.JobID != "1" || m.State != store.MessageQueued || m.RecipientURL != ana.URL || m.RecipientName != "Ana Silva" {
		t.Errorf("queued %+v", m)
	}
	if want := "Hi Ana, I applied for Go Developer at Acme."; m.Text != want {
		t.Errorf("text %q, want %q", m.Text, want)
	}
}

func TestAnAppliedJobWithNoHirerRecordsNoRecipientAndNeverQueuesAMessage(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {ok: false}}}
	r, st, _ := runner(t, site, messagesCfg())
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 1 || msgs[0].State != store.MessageNoRecipient || msgs[0].RecipientURL != "" || msgs[0].Text != "" {
		t.Fatalf("messages %+v, want one blank no_recipient row", msgs)
	}
}

func TestAJobAlreadyQueuedIsNotQueuedTwice(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
	r, st, _ := runner(t, site, messagesCfg())
	if err := st.UpsertJob(store.Job{JobID: "1", Outcome: store.Skipped, Title: "Go Developer", Company: "Acme"}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.QueueMessage(store.Message{JobID: "1", RecipientName: "Ana Silva", RecipientURL: ana.URL, Text: "Earlier text"})
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 1 || msgs[0].Text != "Earlier text" {
		t.Errorf("messages %+v, want the one earlier message untouched", msgs)
	}
}

func TestTheSamePostingListedTwiceQueuesOneMessage(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "Go Developer", "Acme", "desc"),
		"2": job("2", "Go Developer", "Acme", "desc"),
	}, pages: [][]string{{"1", "2"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}, "2": {to: ana, ok: true}}}
	r, st, _ := runner(t, site, messagesCfg())
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 1 || sum.Skipped != 1 {
		t.Fatalf("applied %d skipped %d err %v", sum.Applied, sum.Skipped, err)
	}
	if msgs := allMessages(t, st); len(msgs) != 1 {
		t.Errorf("%d messages, want 1", len(msgs))
	}
}

func TestAFailedOrSkippedApplicationQueuesNothing(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "Go Developer", "Acme", "desc"),
		"2": {card: job("2", "Go Developer", "Acme", "desc").card, apply: linkedin.EasyApply, appliedOnPage: true}, // skipped: already applied
	}, pages: [][]string{{"1", "2"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}, "2": {to: ana, ok: true}}}
	r, st, _ := runner(t, site, messagesCfg())
	r.Applier = &fakeApplier{errs: map[string]error{"1": errors.New("boom")}}
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Failed != 1 || sum.Skipped != 1 {
		t.Fatalf("failed %d skipped %d err %v", sum.Failed, sum.Skipped, err)
	}
	if msgs := allMessages(t, st); len(msgs) != 0 {
		t.Errorf("messages queued for a failed or skipped job: %+v", msgs)
	}
}

func TestADryRunOrMessagesOffQueuesNothing(t *testing.T) {
	jobs := func() *fakeSite {
		return &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
			pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
	}
	t.Run("dry run", func(t *testing.T) {
		r, st, _ := runner(t, jobs(), messagesCfg())
		if _, err := r.Run(context.Background(), Options{DryRun: true}); err != nil {
			t.Fatal(err)
		}
		if msgs := allMessages(t, st); len(msgs) != 0 {
			t.Errorf("a dry run queued %+v", msgs)
		}
	})
	t.Run("off", func(t *testing.T) {
		c := messagesCfg()
		c.Messages.Enabled = false
		r, st, _ := runner(t, jobs(), c)
		if _, err := r.Run(context.Background(), Options{}); err != nil {
			t.Fatal(err)
		}
		if msgs := allMessages(t, st); len(msgs) != 0 {
			t.Errorf("messages off queued %+v", msgs)
		}
	})
}

func TestAHiringTeamReadErrorLeavesTheApplicationStanding(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirerErr: errors.New("the page moved")}
	r, st, _ := runner(t, site, messagesCfg())
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 1 {
		t.Fatalf("applied %d, err %v: a hiring-team error must not fail the application", sum.Applied, err)
	}
	if msgs := allMessages(t, st); len(msgs) != 0 {
		t.Errorf("queued %+v after a read error", msgs)
	}
}

func TestABlankTemplateQueuesNothing(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
	c := messagesCfg()
	c.Markets.Internacional.MessageTemplate = "  "
	r, st, _ := runner(t, site, c)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if msgs := allMessages(t, st); len(msgs) != 0 {
		t.Errorf("a blank template queued %+v", msgs)
	}
}

// pausesRecorded replaces the runner's pacer with one that records every pause
// in the site's log, so tests see pauses and sends in order.
func pausesRecorded(r *Runner, site *fakeSite) *[]time.Duration {
	var pauses []time.Duration
	r.Pacer = pacing.New(r.Cfg.Pacing, true).WithSleep(func(ctx context.Context, d time.Duration) error {
		pauses = append(pauses, d)
		site.log = append(site.log, "pause")
		return ctx.Err()
	})
	return &pauses
}

// approvedMessage queues a message for job jobID and approves it, as
// ajai messages approve does, and returns the row.
func approvedMessage(t *testing.T, st *store.Store, jobID string, to messages.Recipient, text string) store.Message {
	t.Helper()
	if err := st.UpsertJob(store.Job{JobID: jobID, Outcome: store.Applied, Title: "Go Developer", Company: "Acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueMessage(store.Message{JobID: jobID, RecipientName: to.Name, RecipientURL: to.URL, Text: text}); err != nil {
		t.Fatal(err)
	}
	msgs := allMessages(t, st)
	m := msgs[len(msgs)-1]
	if err := st.ApproveMessage(m.ID, messages.Digest(text)); err != nil {
		t.Fatal(err)
	}
	return m
}

// recruiter is a hirer of its own, one per name: each recruiter gets one message, ever.
func recruiter(name string) messages.Recipient {
	return messages.Recipient{Name: name, URL: "https://www.linkedin.com/in/" + name}
}

// sentEarlierToday records a message to a recruiter of its own as sent today,
// through the same transitions a send takes.
func sentEarlierToday(t *testing.T, st *store.Store, jobID string) {
	t.Helper()
	sentEarlierTodayTo(t, st, jobID, recruiter("earlier-"+jobID))
}

func sentEarlierTodayTo(t *testing.T, st *store.Store, jobID string, to messages.Recipient) {
	t.Helper()
	m := approvedMessage(t, st, jobID, to, "Earlier")
	for _, step := range []func(int64) (bool, error){st.MarkSending, st.MarkSent} {
		if ok, err := step(m.ID); err != nil || !ok {
			t.Fatalf("sent earlier today: ok=%v err=%v", ok, err)
		}
	}
}

// liveSite is a site with no job cards: a run walks nothing and goes straight to sending.
func liveSite() *fakeSite { return &fakeSite{jobs: map[string]fakeJob{}, pages: [][]string{{}}} }

func stateOf(t *testing.T, st *store.Store, id int64) store.Message {
	t.Helper()
	m, err := st.GetMessage(id)
	if err != nil || m == nil {
		t.Fatalf("message %d: %v", id, err)
	}
	return *m
}

func TestApprovedMessagesAreSentAtTheEndOfALiveRun(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, messagesCfg())
	m := approvedMessage(t, st, "1", ana, "Oi Ana")
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 1 || site.sends[0].jobID != "1" || site.sends[0].to != ana || site.sends[0].text != "Oi Ana" {
		t.Fatalf("sends %+v", site.sends)
	}
	if got := stateOf(t, st, m.ID); got.State != store.MessageSent || got.SentAt == "" {
		t.Errorf("after the send: %+v", got)
	}
}

func TestADryRunNeverSends(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, messagesCfg())
	m := approvedMessage(t, st, "1", ana, "Oi Ana")
	if _, err := r.Run(context.Background(), Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 0 || stateOf(t, st, m.ID).State != store.MessageApproved {
		t.Errorf("a dry run sent %+v", site.sends)
	}
}

func TestSendingStopsAtMaxPerDayCountingWhatWasSentToday(t *testing.T) {
	site := liveSite()
	c := messagesCfg()
	c.Messages.MaxPerDay = 3
	r, st, _ := runner(t, site, c)
	sentEarlierToday(t, st, "1")
	sentEarlierToday(t, st, "2")
	approvedMessage(t, st, "3", recruiter("c"), "one")
	approvedMessage(t, st, "4", recruiter("d"), "two")
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 1 || site.sends[0].text != "one" {
		t.Errorf("sends %+v, want only the first approved message", site.sends)
	}
	if n, _ := st.SentToday(); n != 3 {
		t.Errorf("sent today = %d, want 3", n)
	}
}

func TestEachSendIsPrecededByAPauseWithinTheBounds(t *testing.T) {
	site := liveSite()
	c := messagesCfg()
	c.Messages.BetweenMinSeconds, c.Messages.BetweenMaxSeconds = 90, 300
	r, st, _ := runner(t, site, c)
	approvedMessage(t, st, "1", ana, "one")
	approvedMessage(t, st, "2", recruiter("bia"), "two")
	pauses := pausesRecorded(r, site)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"pause", "send", "pause", "send"}; !equalStrings(site.log, want) {
		t.Fatalf("order %v, want %v", site.log, want)
	}
	for _, d := range *pauses {
		if d < 90*time.Second || d > 300*time.Second {
			t.Errorf("pause %v outside 90s..300s", d)
		}
	}
}

func TestAMessageThatCannotBeMarkedSendingIsSkipped(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, messagesCfg())
	first := approvedMessage(t, st, "1", ana, "first")
	approvedMessage(t, st, "2", ana, "second")
	pauses := 0
	r.Pacer = pacing.New(r.Cfg.Pacing, true).WithSleep(func(context.Context, time.Duration) error {
		// Before the first pause ends, the first message is removed from the queue.
		if pauses++; pauses == 1 {
			if ok, err := st.DeleteMessage(first.ID); err != nil || !ok {
				t.Fatalf("could not remove the first message: ok=%v err=%v", ok, err)
			}
		}
		return nil
	})
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 1 || site.sends[0].text != "second" {
		t.Errorf("sends %+v, want only the second message", site.sends)
	}
}

func TestABlockedSendStopsFurtherSendsInThatRun(t *testing.T) {
	site := liveSite()
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) { return linkedin.SendBlocked, nil }
	r, st, _ := runner(t, site, messagesCfg())
	first := approvedMessage(t, st, "1", ana, "first")
	second := approvedMessage(t, st, "2", ana, "second")
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 1 {
		t.Errorf("%d sends after a block, want 1", len(site.sends))
	}
	if got := stateOf(t, st, first.ID); got.State != store.MessageFailed || got.Error == "" {
		t.Errorf("blocked message: %+v, want failed with a reason", got)
	}
	if got := stateOf(t, st, second.ID); got.State != store.MessageApproved {
		t.Errorf("the message after the block is %q, want approved for a later run", got.State)
	}
}

func TestARecipientWhoChangedIsNotSentAndEndsNoRecipient(t *testing.T) {
	site := liveSite()
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) { return linkedin.SendNoRecipient, nil }
	r, st, _ := runner(t, site, messagesCfg())
	m := approvedMessage(t, st, "1", ana, "Oi")
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	got := stateOf(t, st, m.ID)
	if got.State != store.MessageNoRecipient || got.SentAt != "" || got.Error == "" {
		t.Errorf("after the changed recipient: %+v", got)
	}
	if n, _ := st.SentToday(); n != 0 {
		t.Errorf("sent today = %d, want 0", n)
	}
}

func TestACancelledContextSendsNothingMore(t *testing.T) {
	site := liveSite()
	ctx, cancel := context.WithCancel(context.Background())
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) { cancel(); return linkedin.SendSent, nil }
	r, st, _ := runner(t, site, messagesCfg())
	approvedMessage(t, st, "1", ana, "first")
	second := approvedMessage(t, st, "2", ana, "second")
	if _, err := r.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 1 {
		t.Errorf("%d sends after Ctrl-C, want 1", len(site.sends))
	}
	if got := stateOf(t, st, second.ID); got.State != store.MessageApproved {
		t.Errorf("the message after the cancel is %q, want approved", got.State)
	}
}

func TestASendErrorMarksTheMessageFailedAndBlocksTheRecruitersNext(t *testing.T) {
	site := liveSite()
	n := 0
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) {
		n++
		if n == 1 {
			return "", errors.New("the send button moved")
		}
		return linkedin.SendSent, nil
	}
	r, st, _ := runner(t, site, messagesCfg())
	first := approvedMessage(t, st, "1", ana, "first")
	second := approvedMessage(t, st, "2", ana, "second")
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, st, first.ID); got.State != store.MessageFailed || got.Error != "the send button moved" {
		t.Errorf("failed message: %+v", got)
	}
	if len(site.sends) != 1 {
		t.Errorf("sends %+v, want only the failed one: a failed send may have reached the recruiter", site.sends)
	}
	if got := stateOf(t, st, second.ID); got.State != store.MessageNoRecipient || got.Error != reasonMessaged {
		t.Errorf("next message: %+v, want no_recipient, already messaged this recruiter", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestASendingRowLeftByADeadProcessEndsFailedAndBlocksTheRecruiterNext(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, messagesCfg())
	orphan := approvedMessage(t, st, "1", ana, "orphan")
	if ok, err := st.MarkSending(orphan.ID); err != nil || !ok {
		t.Fatalf("MarkSending: ok=%v err=%v", ok, err)
	}
	next := approvedMessage(t, st, "2", ana, "next")
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 0 {
		t.Errorf("sends %+v, want none: the orphaned send may have reached the recruiter", site.sends)
	}
	if got := stateOf(t, st, orphan.ID); got.State != store.MessageFailed || got.Error != store.InterruptedSend || got.SentAt != "" {
		t.Errorf("orphaned message: %+v, want failed with the interrupted reason", got)
	}
	if got := stateOf(t, st, next.ID); got.State != store.MessageNoRecipient || got.Error != reasonMessaged {
		t.Errorf("next message: %+v, want no_recipient, already messaged this recruiter", got)
	}
}

func TestAStopDuringSendingRecordsTheStopCauseAndNotifiesOnce(t *testing.T) {
	site := liveSite()
	ctx, cancel := context.WithCancelCause(context.Background())
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) {
		cancel(&StopRequested{Reason: "stopped from the TUI"})
		return linkedin.SendSent, nil
	}
	r, st, _ := runner(t, site, messagesCfg())
	rec := &recorder{}
	r.Notifier = rec
	approvedMessage(t, st, "1", ana, "first")
	approvedMessage(t, st, "2", ana, "second")
	sum, err := r.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.End != EndStopped || sum.Reason != "stopped from the TUI" {
		t.Errorf("summary ended %q %q, want stopped with the stop request's reason", sum.End, sum.Reason)
	}
	run, err := st.GetRun(sum.RunID)
	if err != nil || run == nil {
		t.Fatalf("run %d: %v", sum.RunID, err)
	}
	if run.Reason != "stopped from the TUI" {
		t.Errorf("recorded reason %q, want the stop request's", run.Reason)
	}
	if len(rec.messages) != 1 {
		t.Errorf("%d notifier messages, want exactly 1", len(rec.messages))
	}
}

func TestAHirerWithoutAProfileURLIsRecordedNoRecipientNotQueued(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: messages.Recipient{Name: "Ana Silva"}, ok: true}}}
	r, st, _ := runner(t, site, messagesCfg())
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 1 || msgs[0].State != store.MessageNoRecipient || msgs[0].Text != "" || msgs[0].RecipientURL != "" {
		t.Fatalf("messages %+v, want one blank no_recipient row", msgs)
	}
	if next, _ := st.NextApproved(10); len(next) != 0 {
		t.Errorf("offered for sending: %+v", next)
	}
}

func TestAMessageIsMarkedSendingBeforeTheSendIsClicked(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, messagesCfg())
	m := approvedMessage(t, st, "1", ana, "Oi")
	var during store.Message
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) {
		during = stateOf(t, st, m.ID)
		return linkedin.SendSent, nil
	}
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if during.State != store.MessageSending {
		t.Errorf("during the send the row is %q, want sending", during.State)
	}
}

func TestANonStopSessionSendsApprovedMessagesAtTheEndOfEachRun(t *testing.T) {
	site := liveSite()
	c := messagesCfg()
	c.Settings.RunNonStop = true
	r, st, _ := runner(t, site, c)
	approvedMessage(t, st, "1", ana, "first")
	waits := 0
	r.Sleep = func(ctx context.Context, d time.Duration) error {
		if waits++; waits == 1 {
			approvedMessage(t, st, "2", recruiter("bia"), "second") // queued and approved while the session waits
			return nil
		}
		return errors.New("end the session") // a session ending during the wait returns cleanly
	}
	sums, err := r.Session(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("%d runs, want 2", len(sums))
	}
	if len(site.sends) != 2 || site.sends[0].text != "first" || site.sends[1].text != "second" {
		t.Errorf("sends %+v, want one message at the end of each run", site.sends)
	}
}

func TestAPanicInsideASendLeavesTheRowOutOfTheNextRunsSends(t *testing.T) {
	site := liveSite()
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) { panic("the send button vanished") }
	r, st, _ := runner(t, site, messagesCfg())
	m := approvedMessage(t, st, "1", ana, "Oi")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the run did not crash on the panic")
			}
		}()
		_, _ = r.Run(context.Background(), Options{})
	}()

	next := liveSite()
	r2 := *r // the next process: same store, a fresh site
	r2.Site = next
	if _, err := r2.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(next.sends) != 0 {
		t.Errorf("the next run sent %+v, want nothing: a message left sending must not be sent again", next.sends)
	}
	if got := stateOf(t, st, m.ID); got.State != store.MessageFailed || got.Error != store.InterruptedSend {
		t.Errorf("after the next run: %+v, want failed as interrupted", got)
	}
}

// Postings in each language, long enough for messages.DetectLanguage to be sure.
const (
	englishPosting    = "We are looking for a Senior Python Developer to join our team. You will design and build APIs, work with the data platform, and collaborate with product managers. Experience with Django is a plus. We offer remote work and a flexible schedule."
	portuguesePosting = "Procuramos um Desenvolvedor Python Sênior para integrar nossa equipe. Você irá projetar e desenvolver APIs, trabalhar com a plataforma de dados e colaborar com os times de produto. Experiência com Django é um diferencial. Oferecemos trabalho remoto para você."
	spanishPosting    = "Buscamos un Desarrollador Python Senior para unirse a nuestro equipo. Usted diseñará y construirá APIs, trabajará con la plataforma de datos y colaborará con los equipos de producto. La experiencia con Django es un plus. Ofrecemos trabajo remoto para los que se postulen."
)

// languageCfg sets a marker template per language and a market template of its
// own, so the text queued shows which one was chosen.
func languageCfg() config.Config {
	c := messagesCfg()
	c.Messages.Templates = config.MessageTemplates{En: "EN {first_name} {title} at {company}",
		Pt: "PT {first_name} {title} at {company}", Es: "ES {first_name} {title} at {company}"}
	c.Markets.Internacional.MessageTemplate = "MARKET {first_name} {title} at {company}"
	return c
}

func queuedTextFor(t *testing.T, description string, c config.Config) string {
	t.Helper()
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", description)},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
	r, st, _ := runner(t, site, c)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 1 {
		t.Fatalf("%d messages queued, want 1", len(msgs))
	}
	return msgs[0].Text
}

func TestTheMessageIsWrittenInTheLanguageOfThePosting(t *testing.T) {
	for _, tc := range []struct {
		name, description, want string
	}{
		{"an English posting", englishPosting, "EN Ana Go Developer at Acme"},
		{"a Portuguese posting", portuguesePosting, "PT Ana Go Developer at Acme"},
		{"a Spanish posting", spanishPosting, "ES Ana Go Developer at Acme"},
		{"a posting in no clear language", "desc", "MARKET Ana Go Developer at Acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := queuedTextFor(t, tc.description, languageCfg()); got != tc.want {
				t.Errorf("text %q, want %q", got, tc.want)
			}
		})
	}
}

func TestABlankLanguageTemplateFallsBackToTheMarketTemplate(t *testing.T) {
	c := languageCfg()
	c.Messages.Templates.Pt = "  "
	if got, want := queuedTextFor(t, portuguesePosting, c), "MARKET Ana Go Developer at Acme"; got != want {
		t.Errorf("text %q, want %q", got, want)
	}
}

func TestALanguageTemplateIsUsedEvenWhenTheMarketTemplateIsBlank(t *testing.T) {
	c := languageCfg()
	c.Markets.Internacional.MessageTemplate = ""
	if got, want := queuedTextFor(t, englishPosting, c), "EN Ana Go Developer at Acme"; got != want {
		t.Errorf("text %q, want %q", got, want)
	}
}

// autoSendCfg is messagesCfg with messages.auto_send on.
func autoSendCfg() config.Config {
	c := messagesCfg()
	c.Messages.AutoSend = true
	return c
}

// twoJobs is a page of two applicable jobs whose hirers are ana and bia.
func twoJobs() *fakeSite {
	bia := messages.Recipient{Name: "Bia Costa", URL: "https://www.linkedin.com/in/bia"}
	return &fakeSite{jobs: map[string]fakeJob{
		"1": job("1", "Go Developer", "Acme", "desc"),
		"2": job("2", "Go Engineer", "Initech", "desc"),
	}, pages: [][]string{{"1", "2"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}, "2": {to: bia, ok: true}}}
}

func TestAutoSendSendsEachMessageRightAfterItsApplication(t *testing.T) {
	site := twoJobs()
	r, st, _ := runner(t, site, autoSendCfg())
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 2 {
		t.Fatalf("applied %d, err %v", sum.Applied, err)
	}
	if len(site.here) != 2 {
		t.Fatalf("SendHere calls %+v, want 2", site.here)
	}
	for i, want := range []struct {
		opened string
		cards  int
		text   string
	}{{"1", 1, "Hi Ana, I applied for Go Developer at Acme."}, {"2", 2, "Hi Bia, I applied for Go Engineer at Initech."}} {
		if got := site.here[i]; got.opened != want.opened || got.cards != want.cards || got.text != want.text {
			t.Errorf("send %d = %+v, want on job %s with %d cards opened, text %q", i, got, want.opened, want.cards, want.text)
		}
	}
	if len(site.sends) != 0 {
		t.Errorf("SendMessage called %+v; an auto-sent message is sent where it was applied", site.sends)
	}
	for _, m := range allMessages(t, st) {
		if m.State != store.MessageSent {
			t.Errorf("message %+v, want sent", m)
		}
	}
}

func TestAutoSendAtTheDailyCapLeavesTheMessageApproved(t *testing.T) {
	site := twoJobs()
	c := autoSendCfg()
	c.Messages.MaxPerDay = 1
	r, st, _ := runner(t, site, c)
	sentEarlierToday(t, st, "9")
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 2 {
		t.Fatalf("applied %d, err %v", sum.Applied, err)
	}
	if len(site.here) != 0 || len(site.sends) != 0 {
		t.Errorf("sent past the cap: here %+v, end of run %+v", site.here, site.sends)
	}
	for _, m := range allMessages(t, st) {
		if m.JobID != "9" && m.State != store.MessageApproved {
			t.Errorf("message %+v, want approved", m)
		}
	}
}

func TestAutoSendOffQueuesAsBefore(t *testing.T) {
	site := twoJobs()
	r, st, _ := runner(t, site, messagesCfg())
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.here) != 0 || len(site.sends) != 0 {
		t.Errorf("sent without auto_send: here %+v, end of run %+v", site.here, site.sends)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 2 || msgs[0].State != store.MessageQueued || msgs[1].State != store.MessageQueued {
		t.Errorf("messages %+v, want two queued", msgs)
	}
}

func TestAutoSendInADryRunSendsAndRecordsNothing(t *testing.T) {
	site := twoJobs()
	r, st, _ := runner(t, site, autoSendCfg())
	if _, err := r.Run(context.Background(), Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(site.here) != 0 || len(site.sends) != 0 || len(allMessages(t, st)) != 0 {
		t.Errorf("a dry run sent or recorded: here %+v, sends %+v, rows %+v", site.here, site.sends, allMessages(t, st))
	}
}

func TestAutoSendBlockedSendsNoMoreButKeepsApplying(t *testing.T) {
	site := twoJobs()
	site.hereFunc = func(hereCall) (linkedin.SendResult, error) { return linkedin.SendBlocked, nil }
	r, st, _ := runner(t, site, autoSendCfg())
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 2 {
		t.Fatalf("applied %d, err %v; a block must not stop the applications", sum.Applied, err)
	}
	if len(site.here) != 1 || len(site.sends) != 0 {
		t.Errorf("sends after a block: here %+v, end of run %+v; want the one blocked", site.here, site.sends)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 2 || msgs[0].State != store.MessageFailed || msgs[1].State != store.MessageApproved {
		t.Errorf("messages %+v, want the first failed and the second approved", msgs)
	}
}

func TestAutoSendErrorFailsTheMessageAndTheRunGoesOn(t *testing.T) {
	site := twoJobs()
	site.hereFunc = func(c hereCall) (linkedin.SendResult, error) {
		if c.opened == "1" {
			return "", errors.New("the box did not open")
		}
		return linkedin.SendSent, nil
	}
	r, st, _ := runner(t, site, autoSendCfg())
	sum, err := r.Run(context.Background(), Options{})
	if err != nil || sum.Applied != 2 || sum.Failed != 0 {
		t.Fatalf("applied %d failed %d, err %v", sum.Applied, sum.Failed, err)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 2 || msgs[0].State != store.MessageFailed || msgs[0].Error != "the box did not open" || msgs[1].State != store.MessageSent {
		t.Errorf("messages %+v, want the first failed with its error and the second sent", msgs)
	}
}

func TestAFailedSendStillStopsTheRecruiterFromBeingMessagedAgain(t *testing.T) {
	site := sameRecruiter()
	site.hereFunc = func(hereCall) (linkedin.SendResult, error) { return "", errors.New("sent, but never shown") }
	r, st, _ := runner(t, site, autoSendCfg())
	if sum, err := r.Run(context.Background(), Options{}); err != nil || sum.Applied != 2 {
		t.Fatalf("applied %d, err %v", sum.Applied, err)
	}
	if len(site.here) != 1 || site.here[0].opened != "1" {
		t.Errorf("here %+v; want one send, on job 1, and none on job 2", site.here)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 2 || msgs[0].State != store.MessageFailed || msgs[1].State != store.MessageNoRecipient ||
		msgs[1].Error != "already messaged this recruiter" {
		t.Errorf("messages %+v, want job 1 failed and job 2 no_recipient, already messaged", msgs)
	}
}

// sameRecruiter is a page of two applicable jobs with ana as the hirer of both.
func sameRecruiter() *fakeSite {
	site := twoJobs()
	site.hirers["2"] = hirer{to: ana, ok: true}
	return site
}

func TestAutoSendMessagesARecruiterOnce(t *testing.T) {
	site := sameRecruiter()
	r, st, _ := runner(t, site, autoSendCfg())
	if sum, err := r.Run(context.Background(), Options{}); err != nil || sum.Applied != 2 {
		t.Fatalf("applied %d, err %v", sum.Applied, err)
	}
	if len(site.here) != 1 || site.here[0].opened != "1" || len(site.sends) != 0 {
		t.Errorf("here %+v, end of run %+v; want one send, on job 1", site.here, site.sends)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 2 || msgs[0].State != store.MessageSent || msgs[1].State != store.MessageNoRecipient ||
		msgs[1].Error != "already messaged this recruiter" || msgs[1].RecipientURL != ana.URL {
		t.Errorf("messages %+v, want job 1 sent and job 2 no_recipient for ana, already messaged", msgs)
	}
}

func TestARecruiterAlreadyMessagedIsNotQueuedAgain(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
	r, st, _ := runner(t, site, messagesCfg())
	sentEarlierTodayTo(t, st, "9", ana)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	msgs := allMessages(t, st)
	if len(msgs) != 2 || msgs[1].JobID != "1" || msgs[1].State != store.MessageNoRecipient || msgs[1].Error != "already messaged this recruiter" {
		t.Errorf("messages %+v, want job 1 recorded as already messaged", msgs)
	}
}

func TestTheEndOfARunSendsOneMessagePerRecruiter(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, messagesCfg())
	first := approvedMessage(t, st, "1", ana, "first")
	second := approvedMessage(t, st, "2", ana, "second")
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 1 || site.sends[0].text != "first" {
		t.Errorf("sends %+v, want only the first", site.sends)
	}
	if got := stateOf(t, st, first.ID); got.State != store.MessageSent {
		t.Errorf("first: %+v, want sent", got)
	}
	if got := stateOf(t, st, second.ID); got.State != store.MessageNoRecipient || got.Error != "already messaged this recruiter" {
		t.Errorf("second: %+v, want no_recipient, already messaged", got)
	}
}
