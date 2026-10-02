-- Schedule windows (managed by `aja schedule`) and the openings the scheduler
-- has handled, so a window gets one run per day it opens.
CREATE TABLE schedule_windows (
    id         INTEGER PRIMARY KEY,
    days       TEXT NOT NULL,                 -- "mon,tue,..." (schedule.EncodeDays)
    start_min  INTEGER NOT NULL CHECK (start_min BETWEEN 0 AND 1439),
    end_min    INTEGER NOT NULL CHECK (end_min BETWEEN 1 AND 1440),
    market     TEXT NOT NULL DEFAULT '',      -- '' runs the configured active market
    dry_run    INTEGER NOT NULL DEFAULT 0,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    CHECK (end_min > start_min)
);

CREATE TABLE schedule_openings (
    window_id  INTEGER NOT NULL REFERENCES schedule_windows(id) ON DELETE CASCADE,
    date       TEXT NOT NULL,                 -- the local date the window opened
    outcome    TEXT NOT NULL CHECK (outcome IN ('started', 'skipped', 'failed')),
    detail     TEXT NOT NULL DEFAULT '',
    run_id     INTEGER REFERENCES runs(id),
    handled_at TEXT NOT NULL,
    PRIMARY KEY (window_id, date)
);
