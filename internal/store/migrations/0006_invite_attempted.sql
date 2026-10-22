-- When an invite left the queue, sent or not. The daily cap counts these, so a
-- failed attempt uses up the day's allowance (sent_at stays for sent invites only).
ALTER TABLE recruiter_invites ADD COLUMN attempted_at TEXT;
