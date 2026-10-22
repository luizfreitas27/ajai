import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from './api';

export type Load<T> = {
  data: T | null;
  error: string | null;
  loading: boolean;
  reload: () => void;
};

// useLoad fetches on mount and whenever deps change. A response that arrives
// after a newer request started is dropped, so a slow old read never wins.
export function useLoad<T>(fetcher: () => Promise<T>, deps: unknown[]): Load<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const seq = useRef(0);

  useEffect(() => {
    const id = ++seq.current;
    setLoading(true);
    fetcher()
      .then((d) => {
        if (id !== seq.current) return;
        setData(d);
        setError(null);
      })
      .catch((e: unknown) => {
        if (id !== seq.current) return;
        setError(e instanceof ApiError ? e.message : 'The list could not be loaded.');
      })
      .finally(() => {
        if (id === seq.current) setLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}

// Tabs is a row of buttons that switch a list's state filter.
export function Tabs<T extends string>({
  label,
  items,
  value,
  onChange,
}: {
  label: string;
  items: { value: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div className="tabs" role="group" aria-label={label}>
      {items.map((i) => (
        <button
          key={i.value}
          type="button"
          className="tab"
          aria-pressed={value === i.value}
          onClick={() => onChange(i.value)}
        >
          {i.label}
        </button>
      ))}
    </div>
  );
}
