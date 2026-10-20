package run

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/store"
)

// appliedJob records job id as applied, with no recruiter message row.
func appliedJob(t *testing.T, st *store.Store, id, title string) {
	t.Helper()
	if err := st.UpsertJob(store.Job{JobID: id, Outcome: store.Applied, Title: title, Company: "Acme"}); err != nil {
		t.Fatal(err)
	}
}

// sendPending runs SendPending with a backfill from yesterday and returns its lines.
func sendPending(t *testing.T, r *Runner, dryRun bool) ([]string, PendingSummary) {
	t.Helper()
	var lines []string
	sum, err := r.SendPending(context.Background(), PendingOptions{AppliedSince: time.Now().AddDate(0, 0, -1), DryRun: dryRun},
		func(l SendLine) { lines = append(lines, l.JobID+" "+l.Outcome) })
	if err != nil {
		t.Fatal(err)
	}
	return lines, sum
}

func TestSendPendingSendsTheApprovedMessagesFirstThenBackfillsAFreeHirer(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"2": {to: recruiter("bia"), ok: true}}
	r, st, _ := runner(t, site, messagesCfg())
	old := approvedMessage(t, st, "1", ana, "Oi Ana")
	appliedJob(t, st, "2", "Go Developer")
	pauses := pausesRecorded(r, site)

	lines, sum := sendPending(t, r, false)
	if want := []string{"1 " + LineSent, "2 " + LineSent}; fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("lines %v, want %v", lines, want)
	}
	if len(site.sends) != 1 || site.sends[0].jobID != "1" {
		t.Errorf("sends %+v, want the approved message on its job page", site.sends)
	}
	if len(site.here) != 1 || site.here[0].opened != "2" || site.here[0].text != "Hi bia, I applied for Go Developer at Acme." {
		t.Errorf("sends here %+v, want the backfill on job 2's page", site.here)
	}
	if len(*pauses) != 2 {
		t.Errorf("%d pauses, want one before each send", len(*pauses))
	}
	if got := stateOf(t, st, old.ID); got.State != store.MessageSent {
		t.Errorf("approved message %+v, want sent", got)
	}
	msgs := allMessages(t, st)
	if last := msgs[len(msgs)-1]; last.JobID != "2" || last.State != store.MessageSent || last.RecipientURL != recruiter("bia").URL {
		t.Errorf("backfilled message %+v, want sent to bia", last)
	}
	if sum.Sent != 2 || sum.Left != 0 {
		t.Errorf("summary %+v", sum)
	}
}

// A job page with no hirer is "no hirer", not "no free message" (that is a
// compose that would cost a credit), and every backfill job is read in order.
func TestSendPendingSkipsARecruiterAlreadyMessagedAndRecordsAJobWithNoHirer(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"2": {to: ana, ok: true}, "3": {ok: false}}
	r, st, _ := runner(t, site, messagesCfg())
	sentEarlierTodayTo(t, st, "1", ana)
	appliedJob(t, st, "2", "Go Developer")
	appliedJob(t, st, "3", "Go Developer")

	lines, _ := sendPending(t, r, false)
	got := map[string]bool{}
	for _, l := range lines {
		got[l] = true
	}
	if !got["2 "+LineMessaged] || !got["3 "+LineNoHirer] || len(lines) != 2 {
		t.Fatalf("lines %v", lines)
	}
	var order []string // the jobs as reported, one line each
	for _, l := range lines {
		order = append(order, l[:1])
	}
	if fmt.Sprint(site.visited) != fmt.Sprint(order) {
		t.Errorf("visited %v, want every job read once, in the order reported %v", site.visited, order)
	}
	if len(site.sends)+len(site.here) != 0 {
		t.Errorf("sent %+v %+v", site.sends, site.here)
	}
	for _, m := range allMessages(t, st) {
		switch m.JobID {
		case "2":
			if m.State != store.MessageNoRecipient || m.Error != reasonMessaged || m.RecipientURL != ana.URL {
				t.Errorf("job 2 recorded %+v", m)
			}
		case "3":
			if m.State != store.MessageNoRecipient || m.RecipientURL != "" {
				t.Errorf("job 3 recorded %+v", m)
			}
		}
	}
}

func TestSendPendingStopsAtTheDailyCapAndSaysHowManyAreLeft(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"3": {to: recruiter("c"), ok: true}, "4": {to: recruiter("d"), ok: true}}
	c := messagesCfg()
	c.Messages.MaxPerDay = 2
	r, st, _ := runner(t, site, c)
	sentEarlierToday(t, st, "1")
	approvedMessage(t, st, "2", recruiter("b"), "two")
	appliedJob(t, st, "3", "Go Developer")
	appliedJob(t, st, "4", "Go Developer")

	lines, sum := sendPending(t, r, false)
	if want := []string{"2 " + LineSent, "4 " + LineCap, "3 " + LineCap}; fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("lines %v, want %v", lines, want)
	}
	if len(site.here) != 0 || len(site.visited) != 0 {
		t.Errorf("past the cap: visited %v, sent here %+v", site.visited, site.here)
	}
	if sum.Sent != 1 || sum.Left != 2 {
		t.Errorf("summary %+v, want 1 sent and 2 left", sum)
	}
}

func TestSendPendingOnADryRunReadsTheHirersAndWritesNothing(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"2": {to: recruiter("bia"), ok: true}, "3": {ok: false}}
	r, st, _ := runner(t, site, messagesCfg())
	approvedMessage(t, st, "1", ana, "Oi Ana")
	appliedJob(t, st, "2", "Go Developer")
	appliedJob(t, st, "3", "Go Developer")
	before := fmt.Sprint(allMessages(t, st))

	lines, _ := sendPending(t, r, true)
	if len(lines) != 3 || lines[0] != "1 "+LineWouldSend {
		t.Fatalf("lines %v", lines)
	}
	if len(site.sends)+len(site.here) != 0 {
		t.Errorf("a dry run sent %+v %+v", site.sends, site.here)
	}
	if len(site.visited) != 2 {
		t.Errorf("visited %v, want both backfill jobs read", site.visited)
	}
	if after := fmt.Sprint(allMessages(t, st)); after != before {
		t.Errorf("a dry run wrote messages:\n%s\nwant\n%s", after, before)
	}
}

func TestSendPendingStopsEverythingWhenLinkedInBlocks(t *testing.T) {
	site := liveSite()
	site.hirers = map[string]hirer{"3": {to: recruiter("c"), ok: true}}
	site.sendFunc = func(sendCall) (linkedin.SendResult, error) { return linkedin.SendBlocked, nil }
	r, st, _ := runner(t, site, messagesCfg())
	first := approvedMessage(t, st, "1", ana, "first")
	second := approvedMessage(t, st, "2", recruiter("b"), "second")
	appliedJob(t, st, "3", "Go Developer")

	lines, sum := sendPending(t, r, false)
	if len(lines) != 1 || lines[0] != "1 "+LineFailed {
		t.Fatalf("lines %v, want only the blocked send", lines)
	}
	if len(site.sends) != 1 || len(site.visited) != 0 {
		t.Errorf("after the block: sends %+v, visited %v", site.sends, site.visited)
	}
	if got := stateOf(t, st, first.ID); got.State != store.MessageFailed || got.Error != reasonBlocked {
		t.Errorf("blocked message %+v", got)
	}
	if got := stateOf(t, st, second.ID); got.State != store.MessageApproved {
		t.Errorf("the message after the block is %q, want approved", got.State)
	}
	if !sum.Blocked {
		t.Error("summary does not say LinkedIn blocked")
	}
}

func TestSendPendingStopsTheBackfillOnACheckpointPage(t *testing.T) {
	site := liveSite()
	site.hirerErr = linkedin.ErrBlocked
	r, st, _ := runner(t, site, messagesCfg())
	appliedJob(t, st, "1", "Go Developer")
	appliedJob(t, st, "2", "Go Developer")

	lines, sum := sendPending(t, r, false)
	if len(lines) != 1 || lines[0] != "2 "+LineFailed || len(site.visited) != 1 || !sum.Blocked {
		t.Errorf("lines %v, visited %v, summary %+v: want one blocked visit", lines, site.visited, sum)
	}
	if msgs := allMessages(t, st); len(msgs) != 0 {
		t.Errorf("recorded %+v on a checkpoint page", msgs)
	}
}
