CREATE TABLE runs (
    id              INTEGER PRIMARY KEY,
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    market          TEXT NOT NULL,
    dry_run         INTEGER NOT NULL DEFAULT 0,
    applied         INTEGER NOT NULL DEFAULT 0,
    external        INTEGER NOT NULL DEFAULT 0,
    failed          INTEGER NOT NULL DEFAULT 0,
    skipped         INTEGER NOT NULL DEFAULT 0,
    pending_answers INTEGER NOT NULL DEFAULT 0,
    reason          TEXT
);

CREATE TABLE jobs (
    job_id              TEXT PRIMARY KEY,
    outcome             TEXT NOT NULL CHECK (outcome IN ('applied', 'external', 'failed', 'skipped')),
    reason              TEXT NOT NULL DEFAULT '',
    title               TEXT NOT NULL DEFAULT '',
    company             TEXT NOT NULL DEFAULT '',
    work_location       TEXT NOT NULL DEFAULT '',
    work_style          TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    experience_required TEXT NOT NULL DEFAULT '',
    skills              TEXT NOT NULL DEFAULT '',
    hr_name             TEXT NOT NULL DEFAULT '',
    hr_link             TEXT NOT NULL DEFAULT '',
    resume              TEXT NOT NULL DEFAULT '',
    reposted            TEXT NOT NULL DEFAULT '',
    date_posted         TEXT NOT NULL DEFAULT '',
    date_applied        TEXT NOT NULL DEFAULT '',
    job_link            TEXT NOT NULL DEFAULT '',
    external_link       TEXT NOT NULL DEFAULT '',
    questions           TEXT NOT NULL DEFAULT '',
    connect_request     TEXT NOT NULL DEFAULT '',
    stack_trace         TEXT NOT NULL DEFAULT '',
    screenshot          TEXT NOT NULL DEFAULT '',
    run_id              INTEGER REFERENCES runs(id),
    recorded_at         TEXT NOT NULL
);
CREATE INDEX jobs_outcome_recorded ON jobs (outcome, recorded_at);

CREATE TABLE answers (
    id            TEXT PRIMARY KEY,
    label         TEXT NOT NULL,
    normalised    TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('text', 'textarea', 'select', 'radio', 'checkbox')),
    answer        TEXT NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('ai', 'user', 'form')),
    state         TEXT NOT NULL CHECK (state IN ('pending', 'approved')),
    uses          INTEGER NOT NULL DEFAULT 0,
    last_job_link TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (kind, normalised)
);

CREATE TABLE state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
