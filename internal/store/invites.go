package store

// Recruiter invite states. A queued invite is sent at the end of a run; the
// other four are where an invite ends.
const (
	InviteQueued      = "queued"
	InviteSent        = "sent"
	InviteAlready     = "already"
	InviteUnavailable = "unavailable"
	InviteFailed      = "failed"
)

// Invite is one row of recruiter_invites: a connection invite to one hirer.
type Invite struct {
	ID            int64
	JobID         string
	RunID         int64
	RecipientName string
	RecipientURL  string
	State         string
	CreatedAt     string
	SentAt        string
	Error         string
}

const inviteColumns = `id, job_id, COALESCE(run_id, 0), recipient_name, recipient_url, state, created_at, COALESCE(sent_at, ''), error`

// QueueInvite records an invite to inv.RecipientURL unless one exists for that
// recruiter already; inserted reports whether it did. A blank State is queued.
func (s *Store) QueueInvite(inv Invite) (inserted bool, err error) {
	if inv.State == "" {
		inv.State = InviteQueued
	}
	res, err := s.db.Exec(`INSERT INTO recruiter_invites (job_id, run_id, recipient_name, recipient_url, state, created_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(recipient_url) DO NOTHING`,
		inv.JobID, nullInt(inv.RunID), inv.RecipientName, inv.RecipientURL, inv.State, Timestamp(s.now()), inv.Error)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// QueuedInvites returns the invites not yet sent, oldest first.
func (s *Store) QueuedInvites() ([]Invite, error) {
	return s.queryInvites(`SELECT `+inviteColumns+` FROM recruiter_invites WHERE state = ? ORDER BY id`, InviteQueued)
}

func (s *Store) queryInvites(q string, args ...any) ([]Invite, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Invite
	for rows.Next() {
		var v Invite
		if err := rows.Scan(&v.ID, &v.JobID, &v.RunID, &v.RecipientName, &v.RecipientURL, &v.State, &v.CreatedAt, &v.SentAt, &v.Error); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// HasInvited reports whether the recruiter at profile url has an invite in any
// state. An invite is one per recruiter, ever.
func (s *Store) HasInvited(url string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM recruiter_invites WHERE recipient_url = ?`, url).Scan(&n)
	return n > 0, err
}

// MarkInvite ends a queued invite as state (sent, already, unavailable or
// failed) with errText; a sent invite is stamped with the store clock. It
// reports false, and changes nothing, unless the invite is queued.
func (s *Store) MarkInvite(id int64, state, errText string) (bool, error) {
	sentAt := ""
	if state == InviteSent {
		sentAt = Timestamp(s.now().Local())
	}
	res, err := s.db.Exec(`UPDATE recruiter_invites SET state = ?, sent_at = NULLIF(?, ''), error = ? WHERE id = ? AND state = ?`,
		state, sentAt, errText, id, InviteQueued)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// InvitesSentToday counts the invites sent on the local date of the store's clock.
func (s *Store) InvitesSentToday() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM recruiter_invites WHERE state = ? AND substr(sent_at, 1, 10) = ?`,
		InviteSent, s.now().Local().Format("2006-01-02")).Scan(&n)
	return n, err
}

// ListInvites returns the invites in state ("" for all), newest first, at most
// limit of them (0 for all).
func (s *Store) ListInvites(state string, limit int) ([]Invite, error) {
	q := `SELECT ` + inviteColumns + ` FROM recruiter_invites`
	var args []any
	if state != "" {
		q += ` WHERE state = ?`
		args = append(args, state)
	}
	q += ` ORDER BY id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.queryInvites(q, args...)
}
