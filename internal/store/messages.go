package store

import (
	"errors"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/messages"
)

// Recruiter message states, in the order a message moves through them.
const (
	MessageQueued      = "queued"
	MessageApproved    = "approved"
	MessageSending     = "sending"
	MessageSent        = "sent"
	MessageFailed      = "failed"
	MessageNoRecipient = "no_recipient"
)

// Message is one row of recruiter_messages: a message to one hirer of one job,
// or the record that a job had nobody to message.
type Message struct {
	ID            int64
	JobID         string
	RunID         int64
	RecipientName string
	RecipientURL  string
	Text          string
	State         string
	CreatedAt     string
	SentAt        string
	Error         string
}

// Errors the message transitions return when the change is refused.
var (
	ErrNoMessage    = errors.New("no such message")
	ErrNotQueued    = errors.New("the message is not queued")
	ErrTextChanged  = errors.New("the text changed since it was shown")
	ErrQueueChanged = errors.New("the queue changed since it was listed")
)

const messageColumns = `id, job_id, COALESCE(run_id, 0), recipient_name, recipient_url, text, state, created_at, COALESCE(sent_at, ''), error`

// QueueMessage records m unless a row for the same job and recipient exists;
// inserted reports whether it did. A blank State is queued.
func (s *Store) QueueMessage(m Message) (inserted bool, err error) {
	id, err := s.InsertMessage(m)
	return id > 0, err
}

// InsertMessage is QueueMessage giving the new row's id, 0 when a row for the
// same job and recipient exists and nothing was inserted.
func (s *Store) InsertMessage(m Message) (int64, error) {
	if m.State == "" {
		m.State = MessageQueued
	}
	res, err := s.db.Exec(`INSERT INTO recruiter_messages (job_id, run_id, recipient_name, recipient_url, text, state, created_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(job_id, recipient_url) DO NOTHING`,
		m.JobID, nullInt(m.RunID), m.RecipientName, m.RecipientURL, m.Text, m.State, Timestamp(s.now()), m.Error)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return 0, err
	}
	return res.LastInsertId()
}

// ListMessages returns messages oldest first; state "" means all.
func (s *Store) ListMessages(state string) ([]Message, error) {
	q := `SELECT ` + messageColumns + ` FROM recruiter_messages`
	var args []any
	if state != "" {
		q += ` WHERE state = ?`
		args = append(args, state)
	}
	return s.queryMessages(q+` ORDER BY id`, args...)
}

// NextApproved returns up to limit approved messages, oldest first.
func (s *Store) NextApproved(limit int) ([]Message, error) {
	return s.queryMessages(`SELECT `+messageColumns+` FROM recruiter_messages WHERE state = ? ORDER BY id LIMIT ?`,
		MessageApproved, limit)
}

func (s *Store) queryMessages(q string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.JobID, &m.RunID, &m.RecipientName, &m.RecipientURL, &m.Text, &m.State, &m.CreatedAt, &m.SentAt, &m.Error); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMessage returns the message with id, or nil.
func (s *Store) GetMessage(id int64) (*Message, error) {
	msgs, err := s.queryMessages(`SELECT `+messageColumns+` FROM recruiter_messages WHERE id = ?`, id)
	if err != nil || len(msgs) == 0 {
		return nil, err
	}
	return &msgs[0], nil
}

// ApproveMessage moves a queued message to approved when digest matches the
// text it has now, the text the user was shown. The update is bound to that
// text, so a message changed or approved since the read changes nothing.
func (s *Store) ApproveMessage(id int64, digest string) error {
	m, err := s.GetMessage(id)
	if err != nil {
		return err
	}
	if m == nil {
		return ErrNoMessage
	}
	if m.State != MessageQueued {
		return ErrNotQueued
	}
	if messages.Digest(m.Text) != digest {
		return ErrTextChanged
	}
	res, err := s.db.Exec(`UPDATE recruiter_messages SET state = ? WHERE id = ? AND state = ? AND text = ?`,
		MessageApproved, id, MessageQueued, m.Text)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	// Nothing changed: the message went away or moved on since the read. Say why.
	again, err := s.GetMessage(id)
	switch {
	case err != nil:
		return err
	case again == nil:
		return ErrNoMessage
	case again.State != MessageQueued:
		return ErrNotQueued
	}
	return ErrTextChanged
}

// ApproveAll approves every queued message when their list digest (see
// messages.ListDigest) is listDigest: exactly the queue the user last listed.
// Any change to the queue since refuses the whole approval. It returns how many
// it approved. A busy database during the write is a queue that changed under
// the read, so it is refused the same way.
func (s *Store) ApproveAll(listDigest string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`SELECT id, text FROM recruiter_messages WHERE state = ? ORDER BY id`, MessageQueued)
	if err != nil {
		return 0, err
	}
	var items []messages.Item
	var ids []any
	for rows.Next() {
		var it messages.Item
		var text string
		if err := rows.Scan(&it.ID, &text); err != nil {
			_ = rows.Close()
			return 0, err
		}
		it.Digest = messages.Digest(text)
		items = append(items, it)
		ids = append(ids, it.ID)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if messages.ListDigest(items) != listDigest {
		return 0, ErrQueueChanged
	}
	if len(ids) == 0 {
		return 0, tx.Commit()
	}
	res, err := tx.Exec(`UPDATE recruiter_messages SET state = ? WHERE state = ? AND id IN (`+placeholders(len(ids))+`)`,
		append([]any{MessageApproved, MessageQueued}, ids...)...)
	if isBusy(err) { // another writer got in between the read and the write: the queue moved on
		return 0, ErrQueueChanged
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != int64(len(items)) {
		return 0, ErrQueueChanged
	}
	return int(n), tx.Commit()
}

// placeholders returns n comma-separated SQL placeholders.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// MarkSending moves an approved message to sending, just before the send is
// clicked. It reports false, and changes nothing, unless the message is
// approved: a message is offered for sending once.
func (s *Store) MarkSending(id int64) (bool, error) {
	return s.move(id, MessageApproved, MessageSending, "")
}

// HasMessaged reports whether the recruiter at profile url, on any job, already
// has a message sent, sending or failed, or an approved one older than message
// except (0 for a message not recorded yet). Each recruiter gets one message,
// ever; the oldest approved one is the one that goes out. A failed message
// counts too: a send that failed after its click may still have reached the
// recruiter, so none more goes to them.
func (s *Store) HasMessaged(url string, except int64) (bool, error) {
	if url == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM recruiter_messages WHERE recipient_url = ? AND id != ?
		AND (state IN (?, ?, ?) OR (state = ? AND (? = 0 OR id < ?)))`,
		url, except, MessageSent, MessageSending, MessageFailed, MessageApproved, except, except).Scan(&n)
	return n > 0, err
}

// DropApproved records that an approved message will not be sent, and why, as
// no_recipient; false when it was not approved.
func (s *Store) DropApproved(id int64, reason string) (bool, error) {
	return s.move(id, MessageApproved, MessageNoRecipient, reason)
}

// MarkSent records that a sending message went out, stamped in local time.
func (s *Store) MarkSent(id int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE recruiter_messages SET state = ?, sent_at = ?, error = '' WHERE id = ? AND state = ?`,
		MessageSent, Timestamp(s.now().Local()), id, MessageSending)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkFailed records that a sending message did not go out, and why.
func (s *Store) MarkFailed(id int64, reason string) (bool, error) {
	return s.move(id, MessageSending, MessageFailed, reason)
}

// MarkNoRecipient records that a sending message was not sent because its hirer
// can no longer be messaged for free, and why.
func (s *Store) MarkNoRecipient(id int64, reason string) (bool, error) {
	return s.move(id, MessageSending, MessageNoRecipient, reason)
}

// move changes a message from one state to another; false when it was not in from.
func (s *Store) move(id int64, from, to, reason string) (bool, error) {
	res, err := s.db.Exec(`UPDATE recruiter_messages SET state = ?, error = ? WHERE id = ? AND state = ?`, to, reason, id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteMessage removes a message that is not being sent and was not sent; false
// when there is none to remove, or it is sending or sent. A sent row stays: it
// counts toward the daily cap.
func (s *Store) DeleteMessage(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM recruiter_messages WHERE id = ? AND state NOT IN (?, ?)`, id, MessageSending, MessageSent)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SentToday counts the messages sent on the local date of the store's clock.
func (s *Store) SentToday() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM recruiter_messages WHERE state = ? AND substr(sent_at, 1, 10) = ?`,
		MessageSent, s.now().Local().Format("2006-01-02")).Scan(&n)
	return n, err
}

// InterruptedSend is the error a message gets when a process died while it was sending.
const InterruptedSend = "interrupted while sending; it may have been sent"

// FailSendingMessages ends every sending message as failed with reason, and
// says how many. Only a run holding the run lock calls it, so no send is in
// progress: a sending row is left by a process that died mid-send. It is never
// moved back to approved, because it may already have been sent.
func (s *Store) FailSendingMessages(reason string) (int64, error) {
	res, err := s.db.Exec(`UPDATE recruiter_messages SET state = ?, error = ? WHERE state = ?`, MessageFailed, reason, MessageSending)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// sqliteBusy is SQLITE_BUSY. Its extended codes (SQLITE_BUSY_SNAPSHOT is 517)
// share the primary code in their low byte.
const sqliteBusy = 5

// isBusy reports whether err is SQLite saying the database is busy, as modernc's
// *sqlite.Error does through Code().
func isBusy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&0xff == sqliteBusy
}

// AppliedWithoutRecipient returns the jobs applied to on or after since, newest
// first, that have no recruiter message row naming a hirer: never read, or read
// with nobody to message (a blank no_recipient row). ajai messages send reads
// their hirers again.
func (s *Store) AppliedWithoutRecipient(since time.Time) ([]Job, error) {
	return s.listJobs(`outcome = ? AND recorded_at >= ? AND NOT EXISTS (SELECT 1 FROM recruiter_messages m
		WHERE m.job_id = jobs.job_id AND m.recipient_url != '')`, []any{string(Applied), Timestamp(since)}, 0, 0)
}

// unreached is the rule for a job still to be messaged or invited by ajai messages
// backfill: applied, with no message to its hirer in a state that will or did
// go out, and no invite sent or accepted.
func unreached() (string, []any) {
	return `outcome = ? AND NOT EXISTS (SELECT 1 FROM recruiter_messages m WHERE m.job_id = jobs.job_id
		AND m.state IN (?, ?, ?, ?)) AND NOT EXISTS (SELECT 1 FROM recruiter_invites i WHERE i.job_id = jobs.job_id
		AND i.state IN (?, ?))`,
		[]any{string(Applied), MessageSent, MessageApproved, MessageSending, MessageQueued, InviteSent, InviteAlready}
}

// UnreachedApplied returns the applied jobs whose hirer was never messaged or
// invited, newest first, at most limit of them (0 for all).
func (s *Store) UnreachedApplied(limit int) ([]Job, error) {
	where, args := unreached()
	return s.listJobs(where, args, limit, 0)
}

// CountUnreachedApplied counts the jobs UnreachedApplied lists.
func (s *Store) CountUnreachedApplied() (int, error) {
	where, args := unreached()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE `+where, args...).Scan(&n)
	return n, err
}
