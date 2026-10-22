import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { api, ApiError } from '../api';
import { Dialog } from '../components/Dialog';
import { Frame } from '../components/Frame';
import { Empty, Notice } from '../components/States';
import { formatTime, plural } from '../format';
import type { CurrentJob, JobStep, LogLine, Status } from '../types';

type Events = { status: Status | null; logs: LogLine[]; connection: string; retryIn: number };

const HEADING: Record<Status['kind'], string> = {
  idle: 'Idle.',
  starting: 'Starting.',
  running: 'Running.',
  waiting: 'Waiting.',
  stale: 'Stale.',
};

const LEAD: Record<Status['kind'], string> = {
  idle: 'No run is in progress. Start one below, or wait for a daily window.',
  starting: 'A run is opening Chrome and reading its config.',
  running: 'A run is applying to jobs now.',
  waiting: 'The run is waiting for its next window.',
  stale: 'A run ended without clearing its state.',
};

const STEP_LABEL: Record<JobStep, string> = {
  opening: 'Opening the job',
  screening: 'Checking the skip rules',
  applying: 'Filling in Easy Apply',
  messaging: 'Messaging the recruiter',
};

const STEP_NAME: Record<JobStep, string> = {
  opening: 'Opening',
  screening: 'Screening',
  applying: 'Applying',
  messaging: 'Messaging',
};

const MARKETS = [
  { value: 'brasil', label: 'Brasil' },
  { value: 'internacional', label: 'Internacional' },
];

export function Dashboard({ events }: { events: Events }) {
  const { status } = events;
  const runs = status?.last_runs ?? null;

  return (
    <div className="dash">
      {status?.running && status.state && <NowCard state={status.state} />}
      <RunCard status={status} />
      <StartForm busy={!!status?.holder} />
      <LogPanel logs={events.logs} />

      <Frame id="runs" caption="LAST RUNS" meta={runs ? plural(runs.length, 'run').toUpperCase() : undefined}>
        {runs && runs.length > 0 ? (
          <ol className="runs">
            {runs.map((r) => (
              <li key={r.id} className="run">
                <div className="run__head">
                  <time className="meta">{formatTime(r.started_at)}</time>
                  <span className="meta">
                    {r.market}
                    {r.dry_run ? ' · DRY RUN' : ''}
                  </span>
                </div>
                <p className="run__counts">
                  {r.applied} applied, {r.external} external, {r.failed} failed, {r.skipped} skipped
                  {r.pending_answers > 0 && <>, {plural(r.pending_answers, 'answer')} to review</>}
                </p>
                {r.reason && <p className="run__reason">{r.reason}</p>}
              </li>
            ))}
          </ol>
        ) : runs ? (
          <Empty>No runs yet. Start one above, or wait for a daily window.</Empty>
        ) : (
          <Empty>Waiting for the first status.</Empty>
        )}
      </Frame>
    </div>
  );
}

// NowCard shows the job a running run is on, or that it is between jobs.
function NowCard({ state }: { state: NonNullable<Status['state']> }) {
  const job = state.job;
  return (
    <Frame id="job" caption="NOW" meta={job ? STEP_NAME[job.step].toUpperCase() : undefined}>
      {job ? <JobNow job={job} /> : <p className="lead">{state.term ? `Between jobs. Searching "${state.term}".` : 'Between jobs.'}</p>}
    </Frame>
  );
}

function JobNow({ job }: { job: CurrentJob }) {
  const now = useNow();
  const started = Date.parse(job.since);
  const secs = Number.isNaN(started) ? null : Math.max(0, Math.floor((now - started) / 1000));
  const place = [job.company, job.location].filter(Boolean).join(' · ');

  return (
    <>
      <p className="h2">
        <a href={`https://www.linkedin.com/jobs/view/${job.id}/`} target="_blank" rel="noopener">
          {job.title}
        </a>
      </p>
      {place && <p className="muted">{place}</p>}
      <p className="lead">{STEP_LABEL[job.step]}</p>
      <p className="meta">
        Step: {STEP_NAME[job.step]}
        {secs !== null && ` · for ${formatElapsed(secs)}`}
      </p>
    </>
  );
}

// formatElapsed writes seconds as "12s", "2m 05s" or "1h 02m".
function formatElapsed(secs: number): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${pad(secs % 60)}s`;
  return `${Math.floor(secs / 3600)}h ${pad(Math.floor((secs % 3600) / 60))}m`;
}

// useNow returns the time, refreshed every second while the component is mounted.
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);
  return now;
}

function RunCard({ status }: { status: Status | null }) {
  const [stopOpen, setStopOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  if (!status) {
    return (
      <Frame id="now" caption="STATUS" meta="CONNECTING">
        <p className="lead" role="status">
          Connecting to ajai.
        </p>
      </Frame>
    );
  }

  const h = status.holder;
  const s = status.state;
  const counts: [string, number | string][] = [
    ['Applied', s ? s.applied : '–'],
    ['External', s ? s.external : '–'],
    ['Failed', s ? s.failed : '–'],
    ['Skipped', s ? s.skipped : '–'],
  ];

  const stop = async () => {
    if (!h) return;
    setBusy(true);
    setError(null);
    try {
      await api.stopRun(h.pid, h.started);
      setOk('Stop requested. The run ends after the job it is on.');
      setStopOpen(false);
    } catch (e) {
      setStopOpen(false);
      setError(e instanceof ApiError ? e.message : 'The stop request failed.');
    } finally {
      setBusy(false);
    }
  };

  const caption = s ? `RUN ${s.run_id}` : 'NO RUN';
  const meta = status.kind === 'running' ? 'LIVE' : status.kind.toUpperCase();

  return (
    <Frame id="now" caption={caption} meta={meta}>
      <h1 className="h1 state-head">{HEADING[status.kind]}</h1>
      <p className="lead">{LEAD[status.kind]}</p>

      <dl className="counts">
        {counts.map(([label, value]) => (
          <div key={label} className="count">
            <dt className="count__label">{label}</dt>
            <dd className="count__value">{value}</dd>
          </div>
        ))}
      </dl>

      {(s || h) && (
        <dl className="facts">
          {s && (
            <>
              <div>
                <dt>Market</dt>
                <dd>{s.market || '–'}</dd>
              </div>
              <div>
                <dt>Mode</dt>
                <dd>{s.dry_run ? 'Dry run' : 'Live'}</dd>
              </div>
              <div>
                <dt>Term</dt>
                <dd>{s.term || '–'}</dd>
              </div>
              {s.dry_run && (
                <div>
                  <dt>Would apply</dt>
                  <dd>{s.would_apply}</dd>
                </div>
              )}
              {s.next_run && (
                <div>
                  <dt>Next run</dt>
                  <dd>{formatTime(s.next_run)}</dd>
                </div>
              )}
            </>
          )}
          {h && (
            <>
              <div>
                <dt>From</dt>
                <dd>{h.origin || h.from || 'unknown'}</dd>
              </div>
              <div>
                <dt>Started</dt>
                <dd>{formatTime(h.started)}</dd>
              </div>
              <div>
                <dt>Process</dt>
                <dd>pid {h.pid}</dd>
              </div>
            </>
          )}
        </dl>
      )}

      {status.error && <Notice tone="error">{status.error}</Notice>}
      {error && <Notice tone="error">{error}</Notice>}
      {ok && !error && <Notice tone="ok">{ok}</Notice>}

      {h && (
        <div className="actions">
          <button type="button" className="btn btn--outline" onClick={() => setStopOpen(true)} disabled={busy}>
            Stop run
          </button>
        </div>
      )}

      <Dialog
        open={stopOpen}
        title="Stop this run?"
        confirmLabel="Stop run"
        danger
        busy={busy}
        onConfirm={stop}
        onCancel={() => setStopOpen(false)}
      >
        <p>
          The run (pid {h?.pid}, started {formatTime(h?.started ?? '')}) stops after the job it is on. Applications
          already submitted stay submitted.
        </p>
      </Dialog>
    </Frame>
  );
}

function StartForm({ busy }: { busy: boolean }) {
  const [market, setMarket] = useState('brasil');
  const [dryRun, setDryRun] = useState(true);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);
  const wasBusy = useRef(busy);

  // Clear the "Run started" notice once the run it announced has ended.
  useEffect(() => {
    if (wasBusy.current && !busy) setOk(null);
    wasBusy.current = busy;
  }, [busy]);

  const start = async () => {
    setSending(true);
    setError(null);
    setOk(null);
    try {
      const r = await api.startRun(market, dryRun);
      setOk(`Run started (pid ${r.pid}). It appears in the log in a moment.`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'The run could not start.');
    } finally {
      setSending(false);
      setConfirmOpen(false);
    }
  };

  const marketLabel = MARKETS.find((m) => m.value === market)?.label ?? market;

  return (
    <Frame id="start" caption="START A RUN" meta={busy ? 'IN PROGRESS' : 'READY'}>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          if (dryRun) void start();
          else setConfirmOpen(true);
        }}
      >
        <label className="field">
          <span className="field__label">Market</span>
          <select id="market" value={market} onChange={(e) => setMarket(e.target.value)} disabled={busy || sending}>
            {MARKETS.map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </select>
        </label>
        <label className="check">
          <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} disabled={busy || sending} />
          <span>
            Dry run. Looks at jobs and records what it would apply to. Submits nothing.
          </span>
        </label>
        <div className="actions">
          <button type="submit" className="btn btn--primary" disabled={busy || sending}>
            {sending ? 'Starting' : dryRun ? 'Start dry run' : 'Start live run'}
          </button>
        </div>
        {busy && !ok && !error && <Notice tone="ok">A run is in progress. Stop it to start another.</Notice>}
        {error && <Notice tone="error">{error}</Notice>}
        {ok && !error && <Notice tone="ok">{ok}</Notice>}
      </form>

      <Dialog
        open={confirmOpen}
        title="Start a live run?"
        confirmLabel="Start live run"
        danger
        busy={sending}
        onConfirm={start}
        onCancel={() => setConfirmOpen(false)}
      >
        <p>
          This run applies to real jobs on {marketLabel} from your LinkedIn account, and it sends the recruiter messages
          you approved. Stopping it does not undo what it already submitted.
        </p>
      </Dialog>
    </Frame>
  );
}

function LogPanel({ logs }: { logs: LogLine[] }) {
  const boxRef = useRef<HTMLOListElement>(null);
  const [following, setFollowing] = useState(true);

  useLayoutEffect(() => {
    const box = boxRef.current;
    if (following && box) box.scrollTop = box.scrollHeight;
  }, [logs, following]);

  const onScroll = () => {
    const box = boxRef.current;
    if (!box) return;
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
    if (atBottom !== following) setFollowing(atBottom);
  };

  const meta = following ? `FOLLOWING · ${logs.length} LINES` : `PAUSED · ${logs.length} LINES`;

  return (
    <Frame id="log" caption="LOG" meta={meta} className="log">
      {!following && (
        <div className="log__jump">
          <button type="button" className="btn btn--ghost" onClick={() => setFollowing(true)}>
            Jump to latest
          </button>
        </div>
      )}
      {logs.length === 0 ? (
        <Empty>No log lines yet. They appear here as ajai writes them.</Empty>
      ) : (
        <ol className="log__lines" ref={boxRef} onScroll={onScroll} aria-label="Log lines" tabIndex={0}>
          {logs.map((l) => (
            <li key={l.key} className="log__line">
              <time className="log__time">{l.time ? new Date(l.time).toLocaleTimeString() : ''}</time>
              <span className={`log__level lvl--${l.level.toLowerCase()}`}>{l.level}</span>
              <span className="log__msg">
                {l.msg}
                {l.fields.length > 0 && (
                  <span className="log__fields">
                    {' '}
                    {l.fields.map(([k, v]) => `${k}=${v}`).join(' ')}
                  </span>
                )}
              </span>
            </li>
          ))}
        </ol>
      )}
    </Frame>
  );
}
