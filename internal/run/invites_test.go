package run

import (
	"context"
	"errors"
	"testing"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/store"
)

// connectCfg turns invites on, with messages as messagesCfg sets them up.
func connectCfg() config.Config {
	c := messagesCfg()
	c.Messages.Connect, c.Messages.MaxInvitesPerDay = true, 15
	return c
}

func allInvites(t *testing.T, st *store.Store) []store.Invite {
	t.Helper()
	invs, err := st.QueuedInvites()
	if err != nil {
		t.Fatal(err)
	}
	return invs
}

func TestAnAppliedJobWithAHirerQueuesAnInviteEvenWhenTheMessageHasNoRecipient(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
		pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
	r, st, _ := runner(t, site, connectCfg())
	// Ana was messaged before: the message for this job ends no_recipient.
	sentEarlierTodayTo(t, st, "9", ana)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if msgs := allMessages(t, st); len(msgs) != 2 || msgs[1].State != store.MessageNoRecipient {
		t.Fatalf("messages %+v, want the message for job 1 no_recipient", msgs)
	}
	if want := []string{ana.URL}; !slicesEqual(connectedTo(site), want) {
		t.Fatalf("Connect calls %v, want one to Ana, sent at the end of the run", connectedTo(site))
	}
}

func TestNoInviteIsQueuedOnADryRunOrWithConnectOff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    config.Config
		dryRun bool
	}{
		{"dry run", connectCfg(), true},
		{"connect off", messagesCfg(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc")},
				pages: [][]string{{"1"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}}}
			r, st, _ := runner(t, site, tc.cfg)
			if _, err := r.Run(context.Background(), Options{DryRun: tc.dryRun}); err != nil {
				t.Fatal(err)
			}
			if invs := allInvites(t, st); len(invs) != 0 {
				t.Errorf("queued invites %+v, want none", invs)
			}
		})
	}
}

func TestTheSameRecruiterOnTwoAppliedJobsIsInvitedOnce(t *testing.T) {
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Developer", "Acme", "desc"), "2": job("2", "Go Engineer", "Acme", "desc")},
		pages: [][]string{{"1", "2"}}, hirers: map[string]hirer{"1": {to: ana, ok: true}, "2": {to: ana, ok: true}}}
	r, _, _ := runner(t, site, connectCfg())
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{ana.URL}; !slicesEqual(connectedTo(site), want) {
		t.Errorf("Connect calls %v, want one to Ana", connectedTo(site))
	}
}

var bob = messages.Recipient{Name: "Bob Lima", URL: "https://www.linkedin.com/in/bob"}
var carl = messages.Recipient{Name: "Carl Reis", URL: "https://www.linkedin.com/in/carl"}

// queuedInvite queues an invite to to for jobID, as an applied job does.
func queuedInvite(t *testing.T, st *store.Store, jobID string, to messages.Recipient) {
	t.Helper()
	if err := st.UpsertJob(store.Job{JobID: jobID, Outcome: store.Skipped, Title: "Go Developer", Company: "Acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueInvite(store.Invite{JobID: jobID, RecipientName: to.Name, RecipientURL: to.URL}); err != nil {
		t.Fatal(err)
	}
}

func connectedTo(site *fakeSite) []string {
	var urls []string
	for _, to := range site.connects {
		urls = append(urls, to.URL)
	}
	return urls
}

func TestQueuedInvitesAreSentAtTheEndOfTheRunOldestFirst(t *testing.T) {
	site := liveSite()
	site.connectFunc = func(to messages.Recipient) (linkedin.ConnectResult, error) {
		switch to.URL {
		case bob.URL:
			return linkedin.ConnectAlready, nil
		case carl.URL:
			return linkedin.ConnectUnavailable, nil
		}
		return linkedin.ConnectSent, nil
	}
	r, st, _ := runner(t, site, connectCfg())
	queuedInvite(t, st, "1", ana)
	queuedInvite(t, st, "2", bob)
	queuedInvite(t, st, "3", carl)
	sum, err := r.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{ana.URL, bob.URL, carl.URL}; !slicesEqual(connectedTo(site), want) {
		t.Errorf("Connect calls %v, want %v", connectedTo(site), want)
	}
	if invs := allInvites(t, st); len(invs) != 0 {
		t.Errorf("still queued: %+v", invs)
	}
	if sum.Invites != 1 {
		t.Errorf("summary invites %d, want 1 sent", sum.Invites)
	}
}

func TestTheDailyInviteCapLeavesTheRestQueued(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, connectCfg())
	r.Cfg.Messages.MaxInvitesPerDay = 1
	queuedInvite(t, st, "1", ana)
	queuedInvite(t, st, "2", bob)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{ana.URL}; !slicesEqual(connectedTo(site), want) {
		t.Errorf("Connect calls %v, want %v", connectedTo(site), want)
	}
	if invs := allInvites(t, st); len(invs) != 1 || invs[0].RecipientURL != bob.URL {
		t.Errorf("queued %+v, want Bob still queued", invs)
	}
}

func TestALimitedInviteStopsAllInvitesForTheRunAndLeavesThemQueued(t *testing.T) {
	site := liveSite()
	site.connectFunc = func(messages.Recipient) (linkedin.ConnectResult, error) { return linkedin.ConnectLimited, nil }
	r, st, _ := runner(t, site, connectCfg())
	queuedInvite(t, st, "1", ana)
	queuedInvite(t, st, "2", bob)
	queuedInvite(t, st, "3", carl)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{ana.URL}; !slicesEqual(connectedTo(site), want) {
		t.Errorf("Connect calls %v, want only %v", connectedTo(site), want)
	}
	if invs := allInvites(t, st); len(invs) != 3 {
		t.Errorf("queued %+v, want all three still queued", invs)
	}
}

func TestABlockedInviteStopsInvitesAndLeavesThemQueued(t *testing.T) {
	site := liveSite()
	site.connectFunc = func(messages.Recipient) (linkedin.ConnectResult, error) { return linkedin.ConnectBlocked, nil }
	r, st, _ := runner(t, site, connectCfg())
	queuedInvite(t, st, "1", ana)
	queuedInvite(t, st, "2", bob)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{ana.URL}; !slicesEqual(connectedTo(site), want) {
		t.Errorf("Connect calls %v, want only %v", connectedTo(site), want)
	}
	if invs := allInvites(t, st); len(invs) != 2 {
		t.Errorf("queued %+v, want both still queued", invs)
	}
}

func TestACancelledRunSendsNoInvite(t *testing.T) {
	site := liveSite()
	r, st, _ := runner(t, site, connectCfg())
	queuedInvite(t, st, "1", ana)
	queuedInvite(t, st, "2", bob)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.sendInvites(ctx)
	if len(site.connects) != 0 {
		t.Errorf("Connect calls %v, want none", connectedTo(site))
	}
	if invs := allInvites(t, st); len(invs) != 2 {
		t.Errorf("queued %+v, want both still queued", invs)
	}
}

func TestAnInviteThatErrorsIsFailedAndTheNextOneIsStillSent(t *testing.T) {
	site := liveSite()
	site.connectFunc = func(to messages.Recipient) (linkedin.ConnectResult, error) {
		if to.URL == ana.URL {
			return "", errors.New("the Connect button moved")
		}
		return linkedin.ConnectSent, nil
	}
	r, st, _ := runner(t, site, connectCfg())
	queuedInvite(t, st, "1", ana)
	queuedInvite(t, st, "2", bob)
	if _, err := r.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{ana.URL, bob.URL}; !slicesEqual(connectedTo(site), want) {
		t.Errorf("Connect calls %v, want %v", connectedTo(site), want)
	}
	if invs := allInvites(t, st); len(invs) != 0 {
		t.Errorf("still queued: %+v, want the failed one marked and Bob sent", invs)
	}
}

func slicesEqual(a, b []string) bool {
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
