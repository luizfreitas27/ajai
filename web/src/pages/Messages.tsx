import { useEffect, useRef, useState } from 'react';
import { api, ApiError } from '../api';
import { Badge, toneFor } from '../components/Badge';
import { Dialog } from '../components/Dialog';
import { Frame } from '../components/Frame';
import { Empty, Failure, Loading, Notice } from '../components/States';
import { formatTime, plural } from '../format';
import { Tabs, useLoad } from '../hooks';
import type { Message, MessageState } from '../types';

const STATES: { value: MessageState; label: string; help: string }[] = [
  { value: 'queued', label: 'Queued', help: 'Waiting for your approval. Nothing is sent from here until you approve it.' },
  { value: 'approved', label: 'Approved', help: 'Approved. The next live run sends them.' },
  { value: 'sent', label: 'Sent', help: 'Delivered to the recruiter.' },
  { value: 'no_recipient', label: 'No recipient', help: 'The hirer could not be messaged for free.' },
  { value: 'failed', label: 'Failed', help: 'The send failed. The error is shown on each message.' },
];

const STALE = 'This list changed since you read it. Review the messages again.';

export function Messages({ busy }: { busy: boolean }) {
  const [state, setState] = useState<MessageState>('queued');
  const list = useLoad(() => api.messages(state), [state]);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [notice, setNotice] = useState<{ tone: 'ok' | 'error'; text: string } | null>(null);
  const [approveAllOpen, setApproveAllOpen] = useState(false);
  const [approvingAll, setApprovingAll] = useState(false);
  const [reachOpen, setReachOpen] = useState(false);
  const [reaching, setReaching] = useState(false);
  const [reached, setReached] = useState<{ tone: 'ok' | 'error'; text: string } | null>(null);
  const wasBusy = useRef(busy);

  const data = list.data;
  const current = STATES.find((s) => s.value === state)!;
  const unreached = data?.unreached ?? 0;

  // Reread the count once a run ends, so the button follows what the run reached.
  useEffect(() => {
    if (wasBusy.current && !busy) list.reload();
    wasBusy.current = busy;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [busy]);

  const reach = async () => {
    setReaching(true);
    setReached(null);
    try {
      const r = await api.startBackfill();
      setReached({ tone: 'ok', text: `Started (pid ${r.pid}). Follow it in the Dashboard log.` });
    } catch (e) {
      setReached({ tone: 'error', text: e instanceof ApiError ? e.message : 'The backfill could not start.' });
    } finally {
      setReaching(false);
      setReachOpen(false);
    }
  };

  const approve = async (m: Message) => {
    setBusyId(m.id);
    setNotice(null);
    try {
      await api.approveMessage(m.id, m.digest);
      setNotice({ tone: 'ok', text: `Approved the message to ${m.recipient_name || 'the recruiter'}.` });
      list.reload();
    } catch (e) {
      setNotice({ tone: 'error', text:staleOrError(e) });
      if (e instanceof ApiError && e.status === 409) list.reload();
    } finally {
      setBusyId(null);
    }
  };

  const approveAll = async () => {
    if (!data) return;
    setApprovingAll(true);
    setNotice(null);
    try {
      const r = await api.approveAll(data.queue_digest);
      setNotice({ tone: 'ok', text: `Approved ${plural(r.approved, 'message')}.` });
      setApproveAllOpen(false);
      list.reload();
    } catch (e) {
      setApproveAllOpen(false);
      setNotice({ tone: 'error', text: staleOrError(e) });
      if (e instanceof ApiError && e.status === 409) list.reload();
    } finally {
      setApprovingAll(false);
    }
  };

  return (
    <section className="page" aria-labelledby="msg-title">
      <header className="page__head">
        <h1 id="msg-title" className="h1">
          Messages
        </h1>
        {state === 'queued' && data && data.queued > 0 && (
          <button type="button" className="btn btn--primary" onClick={() => setApproveAllOpen(true)}>
            Approve all {data.queued}
          </button>
        )}
      </header>

      <Tabs label="Message state" items={STATES} value={state} onChange={setState} />
      <p className="muted page__help">{current.help}</p>

      {data && (
        <Frame id="reach" caption="PAST APPLICATIONS">
          <p>{plural(unreached, 'past application')} whose recruiter was never reached.</p>
          <div className="actions">
            <button
              type="button"
              className="btn btn--primary"
              onClick={() => setReachOpen(true)}
              disabled={busy || reaching || unreached === 0}
            >
              {reaching ? 'Starting' : 'Reach them'}
            </button>
            {busy && <p className="muted">A run is in progress. Wait for it to end.</p>}
          </div>
          {reached && <Notice tone={reached.tone}>{reached.text}</Notice>}
        </Frame>
      )}

      {notice && <Notice tone={notice.tone}>{notice.text}</Notice>}

      {list.error && <Failure message={list.error} onRetry={list.reload} />}
      {list.loading && !data && !list.error && <Loading />}
      {data && data.messages.length === 0 && <Empty>No {current.label.toLowerCase()} messages.</Empty>}

      {data && data.messages.length > 0 && (
        <ul className="cards" aria-label={`${current.label} messages`}>
          {data.messages.map((m) => (
            <li key={m.id} className="msg">
              <div className="msg__head">
                <p className="msg__to">
                  To{' '}
                  {m.recipient_url ? (
                    <a href={m.recipient_url} target="_blank" rel="noopener noreferrer">
                      {m.recipient_name || 'the hirer'}
                    </a>
                  ) : (
                    <strong>{m.recipient_name || 'the hirer'}</strong>
                  )}
                </p>
                <Badge tone={toneFor(m.state)}>{m.state.replace('_', ' ')}</Badge>
              </div>
              <p className="msg__job">
                {m.title || `Job ${m.job_id}`}
                {m.company && <span className="muted"> · {m.company}</span>}
              </p>
              <p className="msg__text">{m.text}</p>
              {m.error && <p className="msg__error">{m.error}</p>}
              <div className="msg__foot">
                <time className="muted">
                  {m.sent_at && m.sent_at !== m.created_at ? `Sent ${formatTime(m.sent_at)}` : `Queued ${formatTime(m.created_at)}`}
                </time>
                {m.state === 'queued' && (
                  <button type="button" className="btn btn--primary" onClick={() => approve(m)} disabled={busyId !== null}>
                    {busyId === m.id ? 'Approving…' : 'Approve'}
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}

      <Dialog
        open={approveAllOpen}
        title={`Approve ${data?.queued ?? 0} messages?`}
        confirmLabel="Approve all"
        busy={approvingAll}
        onConfirm={approveAll}
        onCancel={() => setApproveAllOpen(false)}
      >
        <p>Approved messages go out during the next live run. You can read each one above before you approve.</p>
      </Dialog>

      <Dialog
        open={reachOpen}
        title={`Reach ${plural(unreached, 'past application')}?`}
        confirmLabel="Reach them"
        busy={reaching}
        onConfirm={reach}
        onCancel={() => setReachOpen(false)}
      >
        <p>
          Open each job page, read the recruiter, and send a message and an invite, within today's limits
          (messages.max_per_day, messages.max_invites_per_day). Runs like a run: one at a time.
        </p>
      </Dialog>
    </section>
  );
}

function staleOrError(e: unknown): string {
  if (e instanceof ApiError && e.status === 409) return STALE;
  return e instanceof Error ? e.message : 'The approval failed.';
}
