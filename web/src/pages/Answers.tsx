import { useState } from 'react';
import { api, ApiError } from '../api';
import { Badge } from '../components/Badge';
import { Empty, Failure, Loading, Notice } from '../components/States';
import { formatTime, plural } from '../format';
import { Tabs, useLoad } from '../hooks';
import type { Answer } from '../types';

type AnswerState = 'pending' | 'approved';

const STATES: { value: AnswerState; label: string }[] = [
  { value: 'pending', label: 'To review' },
  { value: 'approved', label: 'Approved' },
];

export function Answers() {
  const [state, setState] = useState<AnswerState>('pending');
  const list = useLoad(() => api.answers(state), [state]);
  const data = list.data;

  return (
    <section className="page" aria-labelledby="ans-title">
      <header className="page__head">
        <h1 id="ans-title" className="h1">
          Answers
        </h1>
        {data && <p className="muted">{plural(data.length, state === 'pending' ? 'question' : 'answer')}</p>}
      </header>

      <Tabs label="Answer state" items={STATES} value={state} onChange={setState} />
      <p className="muted page__help">
        {state === 'pending'
          ? 'Questions a run could not answer from your config or memory. Approve an answer and later runs reuse it.'
          : 'Answers you approved. Runs reuse them for the same question.'}
      </p>

      {list.error && <Failure message={list.error} onRetry={list.reload} />}
      {list.loading && !data && !list.error && <Loading />}
      {data && data.length === 0 && (
        <Empty>
          {state === 'pending' ? 'Nothing to review. Questions appear here when a run cannot answer them.' : 'No approved answers yet.'}
        </Empty>
      )}

      {data && data.length > 0 && (
        <ul className="cards" aria-label={state === 'pending' ? 'Questions to review' : 'Approved answers'}>
          {data.map((a) => (
            <AnswerCard key={`${a.id}-${a.digest}`} a={a} readOnly={state === 'approved'} onChanged={list.reload} />
          ))}
        </ul>
      )}
    </section>
  );
}

const SOURCE: Record<string, string> = {
  ai: 'Suggested by the AI',
  user: 'Answered by you',
  form: 'Read from the form',
};

function AnswerCard({ a, readOnly, onChanged }: { a: Answer; readOnly: boolean; onChanged: () => void }) {
  // A checkbox takes no text: it is approved as it is, so only the digest goes.
  // A select or radio sends one of its options; a text question sends the text.
  const checkbox = a.kind === 'checkbox';
  const options = !checkbox && a.options && a.options.length > 0 ? a.options : null;
  const [picked, setPicked] = useState<string | null>(options ? (options.includes(a.answer) ? a.answer : null) : null);
  const [draft, setDraft] = useState(a.answer);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const boxAnswered = a.answer.trim() !== '';
  const boxValue = a.answer === 'checked' ? 'Checked' : 'Not checked';
  const canApprove = checkbox
    ? boxAnswered
    : options
      ? picked !== null || a.answer.trim() !== ''
      : draft.trim() !== '';

  // A text or textarea question may be approved as a deliberate blank: the run
  // then leaves it empty for good. Checkboxes, selects and radios cannot.
  const blankable = !checkbox && !options;

  const send = async (blank: boolean) => {
    setBusy(true);
    setError(null);
    try {
      const body = checkbox ? undefined : options ? (picked ?? undefined) : draft.trim();
      await api.approveAnswer(a.id, a.digest, blank ? undefined : body, blank);
      setDone(blank ? 'Left blank.' : 'Approved.');
      onChanged();
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        setError('This answer changed since you read it. Review it again.');
        onChanged();
      } else if (e instanceof ApiError && e.status === 400) {
        setError(e.message);
      } else {
        setError(e instanceof Error ? e.message : 'The approval failed.');
      }
    } finally {
      setBusy(false);
    }
  };

  const approve = () => send(false);
  const leaveBlank = () => send(true);

  return (
    <li className="ans">
      <div className="ans__head">
        <p className="ans__label">{a.label || 'Untitled question'}</p>
        <Badge tone={readOnly ? 'ok' : 'warn'}>{a.kind || 'text'}</Badge>
      </div>
      <p className="muted ans__meta">
        {a.source && <>{SOURCE[a.source] ?? a.source}. </>}
        Used {plural(a.uses, 'time')}
        {a.updated_at && <> · updated {formatTime(a.updated_at)}</>}
      </p>
      {a.last_job_link && (
        <a className="ans__job" href={a.last_job_link} target="_blank" rel="noopener noreferrer">
          Job it came from
        </a>
      )}

      {checkbox ? (
        <p className="ans__answer">
          {boxAnswered ? boxValue : <span className="muted">No answer recorded</span>}
        </p>
      ) : readOnly ? (
        <p className="ans__answer">{a.answer || <span className="muted">Blank</span>}</p>
      ) : options ? (
        <fieldset className="options">
          <legend className="muted">Choose an answer</legend>
          {options.map((o) => (
            <button
              key={o}
              type="button"
              className="option"
              aria-pressed={picked === o}
              onClick={() => setPicked(o)}
              disabled={busy}
            >
              {o}
            </button>
          ))}
        </fieldset>
      ) : (
        <label className="field">
          <span className="field__label">Answer</span>
          <input
            type="text"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            disabled={busy}
            placeholder="Type the answer a run should give"
          />
        </label>
      )}

      {!readOnly && checkbox && !boxAnswered && (
        <p className="notice notice--error">
          There is no recorded answer for this box, and a checkbox takes no text, so it cannot be approved here.
        </p>
      )}

      {!readOnly && (
        <div className="ans__actions">
          {error && <Notice tone="error">{error}</Notice>}
          {done && !error && <Notice tone="ok">{done}</Notice>}
          {blankable && (
            <button type="button" className="btn btn--outline" onClick={leaveBlank} disabled={busy}>
              Leave blank
            </button>
          )}
          <button type="button" className="btn btn--primary" onClick={approve} disabled={busy || !canApprove}>
            {busy ? 'Approving' : 'Approve'}
          </button>
        </div>
      )}
    </li>
  );
}
