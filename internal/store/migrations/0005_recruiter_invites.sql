-- Recruiter invites: one row per recruiter profile, ever. An applied job whose
-- hirer has a profile queues one connection invite to it; the row is never
-- queued twice, so a recruiter is invited at most once across all jobs.
CREATE TABLE recruiter_invites (
    id              INTEGER PRIMARY KEY,
    job_id          TEXT NOT NULL REFERENCES jobs(job_id),
    run_id          INTEGER REFERENCES runs(id),
    recipient_name  TEXT NOT NULL DEFAULT '',
    recipient_url   TEXT NOT NULL,  -- normalised /in/<slug> profile URL
    state           TEXT NOT NULL CHECK (state IN ('queued', 'sent', 'already', 'unavailable', 'failed')),
    created_at      TEXT NOT NULL,
    sent_at         TEXT,
    error           TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX recruiter_invites_recipient ON recruiter_invites (recipient_url);
CREATE INDEX recruiter_invites_state ON recruiter_invites (state, id);
