package store

import (
	"database/sql"
	"errors"

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
	if m.State == "" {
		m.State = MessageQueued
	}
	res, err := s.db.Exec(`INSERT INTO recruiter_messages (job_id, run_id, recipient_name, recipient_url, text, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(job_id, recipient_url) DO NOTHING`,
		m.JobID, nullInt(m.RunID), m.RecipientName, m.RecipientURL, m.Text, m.State, Timestamp(s.now()))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
// text it has now, the text the user was shown.
func (s *Store) ApproveMessage(id int64, digest string) error {
	var state, text string
	err := s.db.QueryRow(`SELECT state, text FROM recruiter_messages WHERE id = ?`, id).Scan(&state, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoMessage
	}
	if err != nil {
		return err
	}
	if state != MessageQueued {
		return ErrNotQueued
	}
	if messages.Digest(text) != digest {
		return ErrTextChanged
	}
	_, err = s.db.Exec(`UPDATE recruiter_messages SET state = ? WHERE id = ? AND state = ?`, MessageApproved, id, MessageQueued)
	return err
}

// ApproveAll approves every queued message when their list digest (see
// messages.ListDigest) is listDigest: exactly the queue the user last listed.
// Any change to the queue since refuses the whole approval. It returns how many
// it approved.
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
	for rows.Next() {
		var it messages.Item
		var text string
		if err := rows.Scan(&it.ID, &text); err != nil {
			_ = rows.Close()
			return 0, err
		}
		it.Digest = messages.Digest(text)
		items = append(items, it)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if messages.ListDigest(items) != listDigest {
		return 0, ErrQueueChanged
	}
	res, err := tx.Exec(`UPDATE recruiter_messages SET state = ? WHERE state = ?`, MessageApproved, MessageQueued)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// MarkSending moves an approved message to sending, just before the send is
// clicked. It reports false, and changes nothing, unless the message is
// approved: a message is offered for sending once.
func (s *Store) MarkSending(id int64) (bool, error) {
	return s.move(id, MessageApproved, MessageSending, "")
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

// DeleteMessage removes a message that is not being sent; false when there is
// none to remove or it is sending.
func (s *Store) DeleteMessage(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM recruiter_messages WHERE id = ? AND state <> ?`, id, MessageSending)
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
