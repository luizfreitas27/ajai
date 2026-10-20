-- Recruiter messages: one row per (job, recipient profile). A job whose hiring
-- team has no free Message button keeps one no_recipient row with a blank
-- recipient URL, so it is recorded once and never sent.
CREATE TABLE recruiter_messages (
    id              INTEGER PRIMARY KEY,
    job_id          TEXT NOT NULL REFERENCES jobs(job_id),
    run_id          INTEGER REFERENCES runs(id),
    recipient_name  TEXT NOT NULL DEFAULT '',
    recipient_url   TEXT NOT NULL DEFAULT '',  -- /in/<slug> profile URL, '' for no_recipient
    text            TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL CHECK (state IN ('queued', 'approved', 'sending', 'sent', 'failed', 'no_recipient')),
    created_at      TEXT NOT NULL,
    sent_at         TEXT,
    error           TEXT NOT NULL DEFAULT '',
    UNIQUE (job_id, recipient_url)
);
CREATE INDEX recruiter_messages_state ON recruiter_messages (state, id);
