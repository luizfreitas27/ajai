import { useEffect, useState } from 'react';
import { api } from '../api';
import { Badge, toneFor } from '../components/Badge';
import { Empty, Failure, Loading } from '../components/States';
import { formatTime } from '../format';
import type { Job } from '../types';

const PAGE = 50;

export function Jobs() {
  const [input, setInput] = useState('');
  const [text, setText] = useState('');
  const [jobs, setJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(true);
  const [more, setMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);

  // The search waits for a pause in typing before it reads.
  useEffect(() => {
    const t = window.setTimeout(() => setText(input.trim()), 300);
    return () => window.clearTimeout(t);
  }, [input]);

  // A new search, or a retry, starts again from the first page.
  useEffect(() => {
    let live = true;
    setLoading(true);
    setError(null);
    api
      .jobs(text, 0, PAGE)
      .then((rows) => {
        if (!live) return;
        setJobs(rows);
        setMore(rows.length === PAGE);
      })
      .catch((e: Error) => live && setError(e.message))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [text, retry]);

  const loadMore = async () => {
    setLoading(true);
    try {
      const rows = await api.jobs(text, jobs.length, PAGE);
      setJobs((prev) => [...prev, ...rows.filter((r) => !prev.some((p) => p.job_id === r.job_id))]);
      setMore(rows.length === PAGE);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'The next page could not be loaded.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <section className="page" aria-labelledby="jobs-title">
      <header className="page__head">
        <h1 id="jobs-title" className="h1">
          Jobs
        </h1>
        <label className="search">
          <span className="sr-only">Search jobs by title, company or location</span>
          <input
            type="search"
            placeholder="Search title, company, place"
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
        </label>
      </header>

      {error && jobs.length === 0 && <Failure message={error} onRetry={() => setRetry((n) => n + 1)} />}
      {loading && jobs.length === 0 && !error && <Loading />}
      {!loading && !error && jobs.length === 0 && (
        <Empty>
          {text ? `No job matches “${text}”.` : 'No jobs recorded yet. Every job a run looks at is listed here.'}
        </Empty>
      )}

      {jobs.length > 0 && (
        <ul className="rows" aria-label="Jobs">
          {jobs.map((j) => (
            <li key={j.job_id} className="row">
              <div className="row__main">
                <p className="row__title">
                  {j.job_link ? (
                    <a href={j.job_link} target="_blank" rel="noopener noreferrer">
                      {j.title || `Job ${j.job_id}`}
                    </a>
                  ) : (
                    j.title || `Job ${j.job_id}`
                  )}
                </p>
                <p className="row__meta">
                  {[j.company, j.work_location, j.work_style].filter(Boolean).join(' · ') || 'No details recorded'}
                </p>
                {j.reason && <p className="row__reason">{j.reason}</p>}
              </div>
              <div className="row__side">
                <Badge tone={toneFor(j.outcome)}>{j.outcome || 'recorded'}</Badge>
                <time className="row__when">{formatTime(j.recorded_at)}</time>
                {j.external_link && (
                  <a href={j.external_link} target="_blank" rel="noopener noreferrer" className="row__ext">
                    Company site
                  </a>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}

      {error && jobs.length > 0 && <p className="notice notice--error" role="alert">{error}</p>}
      {more && (
        <div className="page__more">
          <button type="button" className="btn btn--ghost" onClick={loadMore} disabled={loading}>
            {loading ? 'Loading…' : 'Load more'}
          </button>
        </div>
      )}
    </section>
  );
}
