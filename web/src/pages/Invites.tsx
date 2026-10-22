import { useState } from 'react';
import { api } from '../api';
import { Badge, toneFor } from '../components/Badge';
import { Empty, Failure, Loading } from '../components/States';
import { formatTime } from '../format';
import { Tabs, useLoad } from '../hooks';
import type { Invite } from '../types';

type InviteState = '' | Invite['state'];

const STATES: { value: InviteState; label: string }[] = [
  { value: '', label: 'All' },
  { value: 'queued', label: 'Queued' },
  { value: 'sent', label: 'Sent' },
  { value: 'already', label: 'Already connected' },
  { value: 'unavailable', label: 'Unavailable' },
  { value: 'failed', label: 'Failed' },
];

export function Invites() {
  const [state, setState] = useState<InviteState>('');
  const list = useLoad(() => api.invites(state), [state]);
  const data = list.data;

  return (
    <section className="page" aria-labelledby="inv-title">
      <header className="page__head">
        <h1 id="inv-title" className="h1">
          Invites
        </h1>
      </header>

      <Tabs label="Invite state" items={STATES} value={state} onChange={setState} />

      {list.error && <Failure message={list.error} onRetry={list.reload} />}
      {list.loading && !data && !list.error && <Loading />}
      {data && data.length === 0 && (
        <Empty>{state ? `No invites ${STATES.find((s) => s.value === state)!.label.toLowerCase()}.` : 'No connection invites yet.'}</Empty>
      )}

      {data && data.length > 0 && (
        <ul className="rows" aria-label="Invites">
          {data.map((i) => (
            <li key={i.id} className="row">
              <div className="row__main">
                <p className="row__title">
                  {i.recipient_url ? (
                    <a href={i.recipient_url} target="_blank" rel="noopener noreferrer">
                      {i.recipient_name || 'Unnamed recipient'}
                    </a>
                  ) : (
                    i.recipient_name || 'Unnamed recipient'
                  )}
                </p>
                <p className="row__meta">
                  {i.title || `Job ${i.job_id}`}
                  {i.company && ` · ${i.company}`}
                </p>
                {i.error && <p className="row__reason row__reason--error">{i.error}</p>}
              </div>
              <div className="row__side">
                <Badge tone={toneFor(i.state)}>{i.state.replace('_', ' ')}</Badge>
                <time className="row__when">{formatTime(i.sent_at || i.created_at)}</time>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
