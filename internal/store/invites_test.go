package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestAQueuedInviteIsListedWithItsRecipient(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	ok, err := s.QueueInvite(Invite{JobID: "1", RecipientName: "Ana", RecipientURL: "https://www.linkedin.com/in/ana"})
	must(t, err)
	if !ok {
		t.Fatal("queue: not inserted")
	}
	queue, err := s.QueuedInvites()
	must(t, err)
	if len(queue) != 1 {
		t.Fatalf("QueuedInvites = %d rows, want 1", len(queue))
	}
	got := queue[0]
	if got.State != InviteQueued || got.JobID != "1" || got.RecipientName != "Ana" || got.RecipientURL != "https://www.linkedin.com/in/ana" {
		t.Errorf("queued invite = %+v", got)
	}
}

func TestAnInviteIsQueuedOncePerRecruitmentAcrossJobs(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")
	const ana = "https://www.linkedin.com/in/ana"
	first, err := s.QueueInvite(Invite{JobID: "1", RecipientURL: ana})
	must(t, err)
	if !first {
		t.Fatal("first queue: not inserted")
	}
	again, err := s.QueueInvite(Invite{JobID: "2", RecipientURL: ana})
	must(t, err)
	if again {
		t.Error("the same recruiter on another job was queued again")
	}
	all, err := s.QueuedInvites()
	must(t, err)
	if len(all) != 1 || all[0].JobID != "1" {
		t.Errorf("QueuedInvites = %+v, want the first invite only", all)
	}
}

func TestHasInvitedSeesTheRecruiterAfterQueueing(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	const ana, bia = "https://www.linkedin.com/in/ana", "https://www.linkedin.com/in/bia"
	_, err := s.QueueInvite(Invite{JobID: "1", RecipientURL: ana})
	must(t, err)
	if ok, err := s.HasInvited(ana); err != nil || !ok {
		t.Errorf("HasInvited(queued recruiter) = %v, %v; want true", ok, err)
	}
	if ok, err := s.HasInvited(bia); err != nil || ok {
		t.Errorf("HasInvited(other recruiter) = %v, %v; want false", ok, err)
	}
}

func TestASentInviteIsStampedAndCountedOnlyOnItsLocalDay(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	s := open(t)
	seedJob(t, s, "1")
	seedJob(t, s, "2")
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, brt) }

	yesterday := s.WithClock(func() time.Time { return at(20, 10) })
	_, err := yesterday.QueueInvite(Invite{JobID: "1", RecipientURL: "https://www.linkedin.com/in/ana"})
	must(t, err)
	queue, err := yesterday.QueuedInvites()
	must(t, err)
	ok, err := yesterday.MarkInvite(queue[0].ID, InviteSent, "")
	must(t, err)
	if !ok {
		t.Fatal("MarkInvite on a queued invite changed nothing")
	}

	today := s.WithClock(func() time.Time { return at(21, 9) })
	_, err = today.QueueInvite(Invite{JobID: "2", RecipientURL: "https://www.linkedin.com/in/bia"})
	must(t, err)
	queue, err = today.QueuedInvites()
	must(t, err)
	if _, err := today.MarkInvite(queue[0].ID, InviteSent, ""); err != nil {
		t.Fatal(err)
	}
	var state, sentAt string
	must(t, today.db.QueryRow(`SELECT state, COALESCE(sent_at, '') FROM recruiter_invites WHERE id = ?`, queue[0].ID).Scan(&state, &sentAt))
	if state != InviteSent || sentAt != "2026-10-21T09:00:00-03:00" {
		t.Errorf("sent invite: state %q sent_at %q, want sent at 09:00 BRT", state, sentAt)
	}
	n, err := today.InvitesSentToday()
	must(t, err)
	if n != 1 {
		t.Errorf("InvitesSentToday on 21 Oct = %d, want 1 (the one sent yesterday is not counted)", n)
	}
}

func TestAMarkedInviteLeavesTheQueue(t *testing.T) {
	s := open(t)
	for i, state := range []string{InviteSent, InviteAlready, InviteUnavailable, InviteFailed} {
		job := string(rune('1' + i))
		seedJob(t, s, job)
		_, err := s.QueueInvite(Invite{JobID: job, RecipientURL: "https://www.linkedin.com/in/p" + job})
		must(t, err)
		queue, err := s.QueuedInvites()
		must(t, err)
		if ok, err := s.MarkInvite(queue[len(queue)-1].ID, state, "reason"); err != nil || !ok {
			t.Fatalf("MarkInvite %s: %v %v", state, ok, err)
		}
	}
	queue, err := s.QueuedInvites()
	must(t, err)
	if len(queue) != 0 {
		t.Errorf("QueuedInvites after marking all four = %+v, want none", queue)
	}
	if ok, err := s.MarkInvite(1, InviteSent, ""); err != nil || ok {
		t.Errorf("MarkInvite on a marked invite = %v, %v; want false, nil", ok, err)
	}
}

func TestTheInviteMigrationUpgradesAFourMigrationDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ajai.db")
	// A database as the last release left it: migrations 0001 to 0004 applied.
	db, err := sql.Open("sqlite", path)
	must(t, err)
	_, err = db.Exec(`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`)
	must(t, err)
	for _, name := range []string{"0001_initial", "0002_schedule", "0003_pending_review", "0004_recruiter_messages"} {
		body, err := migrations.ReadFile("migrations/" + name + ".sql")
		must(t, err)
		_, err = db.Exec(string(body))
		must(t, err)
		_, err = db.Exec(`INSERT INTO schema_migrations VALUES (?, '2026-10-01T10:00:00Z')`, "migrations/"+name+".sql")
		must(t, err)
	}
	_, err = db.Exec(`INSERT INTO jobs (job_id, outcome, recorded_at) VALUES ('1', 'applied', '2026-10-01T10:00:00Z')`)
	must(t, err)
	must(t, db.Close())

	s, err := Open(path)
	must(t, err)
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.QueueInvite(Invite{JobID: "1", RecipientURL: "https://www.linkedin.com/in/ana"}); err != nil {
		t.Fatalf("recruiter_invites missing after upgrading a four-migration database: %v", err)
	}
}

func TestListInvitesFiltersByStateNewestFirst(t *testing.T) {
	s := open(t)
	seedJob(t, s, "1")
	for _, url := range []string{"https://www.linkedin.com/in/ana", "https://www.linkedin.com/in/bia"} {
		if _, err := s.QueueInvite(Invite{JobID: "1", RecipientName: url, RecipientURL: url}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListInvites("", 0)
	must(t, err)
	if len(all) != 2 || all[0].RecipientURL != "https://www.linkedin.com/in/bia" {
		t.Fatalf("ListInvites(all) = %+v, want two rows, newest first", all)
	}
	if _, err := s.MarkInvite(all[1].ID, InviteSent, ""); err != nil {
		t.Fatal(err)
	}
	queued, err := s.ListInvites(InviteQueued, 0)
	must(t, err)
	if len(queued) != 1 || queued[0].ID != all[0].ID {
		t.Errorf("ListInvites(queued) = %+v, want only the newer invite", queued)
	}
	one, err := s.ListInvites("", 1)
	must(t, err)
	if len(one) != 1 {
		t.Errorf("ListInvites(limit 1) = %d rows", len(one))
	}
}
