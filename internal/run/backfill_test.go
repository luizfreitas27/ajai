package run

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/store"
)

// backfillJob records job id as applied on October day, with no message row.
func backfillJob(t *testing.T, st *store.Store, id string, day int) {
	t.Helper()
	at := time.Date(2026, 10, day, 10, 0, 0, 0, time.Local)
	if err := st.UpsertJob(store.Job{JobID: id, Outcome: store.Applied, Title: "Go Developer", Company: "Acme",
		Description: "a Go role", RecordedAt: store.Timestamp(at)}); err != nil {
		t.Fatal(err)
	}
}

// sendJobs lists the job ids of the messages a fake site sent, sorted.
func sendJobs(site *fakeSite) []string {
	var ids []string
	for _, s := range site.sends {
		ids = append(ids, s.jobID)
	}
	return sortedStrings(ids)
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestBackfillMessagesAndInvitesEveryUnreachedJobsHirer(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {to: recruiter("bia"), ok: true}, "2": {to: recruiter("caio"), ok: true}}
	r, st, _ := runner(t, site, connectCfg())
	backfillJob(t, st, "1", 1)
	backfillJob(t, st, "2", 2)

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Checked != 2 || sum.Hirers != 2 || sum.NoHirer != 0 || sum.Messages != 2 || sum.Invites != 2 || sum.Stopped != "" {
		t.Errorf("summary %+v, want 2 checked, 2 hirers, 2 messages, 2 invites", sum)
	}
	if want := []string{"1", "2"}; fmt.Sprint(sendJobs(site)) != fmt.Sprint(want) {
		t.Errorf("messages sent on jobs %v, want %v", sendJobs(site), want)
	}
	if want := []string{recruiter("bia").URL, recruiter("caio").URL}; fmt.Sprint(sortedStrings(connectedTo(site))) != fmt.Sprint(sortedStrings(want)) {
		t.Errorf("invites to %v, want %v", connectedTo(site), want)
	}
	for _, s := range site.sends {
		if s.jobID == "1" && s.text != "Hi Bia, I applied for Go Developer at Acme." {
			t.Errorf("job 1 text %q", s.text)
		}
	}
}

func TestBackfillDoesNotMessageARecruiterAlreadyMessagedForAnotherJob(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {to: ana, ok: true}, "2": {to: recruiter("bia"), ok: true}}
	r, st, _ := runner(t, site, connectCfg())
	sentEarlierTodayTo(t, st, "9", ana)
	backfillJob(t, st, "1", 1)
	backfillJob(t, st, "2", 2)

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2"}; fmt.Sprint(sendJobs(site)) != fmt.Sprint(want) {
		t.Errorf("messages sent on jobs %v, want only job 2 (Ana was messaged already)", sendJobs(site))
	}
	if sum.Messages != 1 {
		t.Errorf("summary %+v, want one message", sum)
	}
	for _, m := range allMessages(t, st) {
		if m.JobID == "1" && (m.State != store.MessageNoRecipient || m.Error != reasonMessaged) {
			t.Errorf("job 1 recorded %+v, want no_recipient already messaged", m)
		}
	}
}

func TestBackfillCountsAJobWithNoHirerAndGoesOnToTheNext(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {ok: false}, "2": {to: recruiter("bia"), ok: true}}
	r, st, _ := runner(t, site, connectCfg())
	backfillJob(t, st, "1", 1)
	backfillJob(t, st, "2", 2)

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Checked != 2 || sum.NoHirer != 1 || sum.Hirers != 1 || sum.Messages != 1 || sum.Invites != 1 {
		t.Errorf("summary %+v, want job 1 without a hirer and job 2 messaged and invited", sum)
	}
	if want := []string{"2"}; fmt.Sprint(sendJobs(site)) != fmt.Sprint(want) {
		t.Errorf("messages sent on jobs %v, want %v", sendJobs(site), want)
	}
	for _, m := range allMessages(t, st) {
		if m.JobID == "1" && (m.State != store.MessageNoRecipient || m.RecipientURL != "") {
			t.Errorf("job 1 recorded %+v, want a blank no_recipient row", m)
		}
	}
}

func TestBackfillStopsSendingAtTheDailyCapButInvitesGoOn(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {to: recruiter("bia"), ok: true}, "2": {to: recruiter("caio"), ok: true}}
	c := connectCfg()
	c.Messages.MaxPerDay = 1
	r, st, _ := runner(t, site, c)
	sentEarlierToday(t, st, "9")
	backfillJob(t, st, "1", 1)
	backfillJob(t, st, "2", 2)

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 0 || sum.Messages != 0 {
		t.Errorf("past the daily cap: sends %+v, summary %+v", site.sends, sum)
	}
	if sum.Left != 2 || !strings.Contains(sum.Stopped, "daily message cap") {
		t.Errorf("summary %+v, want 2 left and the cap named", sum)
	}
	if sum.Invites != 2 || len(connectedTo(site)) != 2 {
		t.Errorf("invites %d (%v), want both sent: the invite cap is its own", sum.Invites, connectedTo(site))
	}
}

func TestBackfillStopsAtACheckpointPageWhenReadingAHirer(t *testing.T) {
	site := liveSite()
	site.hirerErr = linkedin.ErrBlocked
	r, st, _ := runner(t, site, connectCfg())
	backfillJob(t, st, "1", 1)
	backfillJob(t, st, "2", 2)

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.visited) != 1 || sum.Stopped == "" || sum.Messages+sum.Invites != 0 {
		t.Errorf("visited %v, summary %+v: want the backfill to stop at the first checkpoint", site.visited, sum)
	}
	if msgs := allMessages(t, st); len(msgs) != 0 {
		t.Errorf("recorded %+v on a checkpoint page", msgs)
	}
}

func TestBackfillStopsEverythingWhenLinkedInBlocksASend(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {to: recruiter("bia"), ok: true}, "2": {to: recruiter("caio"), ok: true}}
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) { return linkedin.SendBlocked, nil }
	r, st, _ := runner(t, site, connectCfg())
	backfillJob(t, st, "1", 1)
	backfillJob(t, st, "2", 2)

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.sends) != 1 || len(connectedTo(site)) != 0 || sum.Invites != 0 {
		t.Errorf("after the block: sends %+v, invites %v, summary %+v", site.sends, connectedTo(site), sum)
	}
	if !strings.Contains(sum.Stopped, "blocked") {
		t.Errorf("stopped %q, want the block named", sum.Stopped)
	}
}

func TestBackfillQueuesNothingAfterItsContextIsCancelled(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {to: recruiter("bia"), ok: true}}
	r, st, _ := runner(t, site, connectCfg())
	backfillJob(t, st, "1", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Backfill(ctx, 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v, want context.Canceled", err)
	}
	if len(site.visited) != 0 || len(site.sends) != 0 || len(connectedTo(site)) != 0 {
		t.Errorf("after cancel: visited %v, sends %+v, invites %v", site.visited, site.sends, connectedTo(site))
	}
}

func TestBackfillKeepsAnOldBlankNoRecipientRowFromBlockingTheMessage(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {to: recruiter("bia"), ok: true}}
	r, st, _ := runner(t, site, messagesCfg())
	backfillJob(t, st, "1", 1)
	if _, err := st.InsertMessage(store.Message{JobID: "1", State: store.MessageNoRecipient, Error: reasonNoRecipient}); err != nil {
		t.Fatal(err)
	}

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Messages != 1 || fmt.Sprint(sendJobs(site)) != "[1]" {
		t.Errorf("summary %+v, sends %+v: want the new message sent", sum, site.sends)
	}
	if msgs := allMessages(t, st); len(msgs) != 2 || msgs[1].State != store.MessageSent || msgs[1].RecipientURL != recruiter("bia").URL {
		t.Errorf("messages %+v, want the blank row kept and the new one sent", msgs)
	}
}

func TestBackfillInviteStillGoesWhenTheMessageRowIsAlreadyThere(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"1": {to: recruiter("bia"), ok: true}}
	r, st, _ := runner(t, site, connectCfg())
	backfillJob(t, st, "1", 1)
	// Premium-only before: a no_recipient row for this very recruiter on this job.
	if _, err := st.InsertMessage(store.Message{JobID: "1", RecipientURL: recruiter("bia").URL, State: store.MessageNoRecipient}); err != nil {
		t.Fatal(err)
	}

	sum, err := r.Backfill(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Messages != 0 || len(site.sends) != 0 {
		t.Errorf("summary %+v, sends %+v: the existing row stands, nothing is sent", sum, site.sends)
	}
	if want := []string{recruiter("bia").URL}; fmt.Sprint(connectedTo(site)) != fmt.Sprint(want) || sum.Invites != 1 {
		t.Errorf("invites %v (%d), want one to bia", connectedTo(site), sum.Invites)
	}
}
