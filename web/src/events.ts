import { useEffect, useRef, useState } from 'react';
import type { LogLine, Status } from './types';

export type Connection = 'connecting' | 'connected' | 'reconnecting';

export const MAX_LOG_LINES = 500;

// parseLine reads one slog JSON line. A line that is not JSON keeps its text as
// the message, so nothing the log wrote is lost.
export function parseLine(raw: string, key: number): LogLine {
  try {
    const obj = JSON.parse(raw) as Record<string, unknown>;
    const { time, level, msg, ...rest } = obj;
    const fields = Object.entries(rest).map(([k, v]) => [k, typeof v === 'string' ? v : JSON.stringify(v)] as [string, string]);
    return {
      key,
      time: typeof time === 'string' ? time : '',
      level: typeof level === 'string' ? level.toUpperCase() : 'INFO',
      msg: typeof msg === 'string' ? msg : raw,
      fields,
    };
  } catch {
    return { key, time: '', level: 'INFO', msg: raw, fields: [] };
  }
}

const MAX_BACKOFF = 30_000;

// useEvents follows /api/events. EventSource would reconnect on its own, but
// without a visible schedule, so the stream is reopened here with a backoff
// (1 s, 2 s, 4 s ... 30 s) that the header can show.
export function useEvents() {
  const [status, setStatus] = useState<Status | null>(null);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [connection, setConnection] = useState<Connection>('connecting');
  const [retryIn, setRetryIn] = useState(0);
  const keyRef = useRef(0);

  useEffect(() => {
    let source: EventSource | null = null;
    let timer: number | undefined;
    let countdown: number | undefined;
    let attempts = 0;
    let closed = false;

    const open = () => {
      source = new EventSource('/api/events');
      source.onopen = () => {
        attempts = 0;
        // the server sends the day's backlog on every connect, so drop what is shown
        setLogs([]);
        setConnection('connected');
        setRetryIn(0);
      };
      source.addEventListener('status', (e) => {
        try {
          setStatus(JSON.parse((e as MessageEvent).data) as Status);
        } catch {
          /* a malformed status keeps the last good one */
        }
      });
      source.addEventListener('log', (e) => {
        const line = parseLine((e as MessageEvent).data as string, ++keyRef.current);
        setLogs((prev) => {
          const next = prev.length >= MAX_LOG_LINES ? prev.slice(prev.length - MAX_LOG_LINES + 1) : prev.slice();
          next.push(line);
          return next;
        });
      });
      source.onerror = () => {
        source?.close();
        source = null;
        if (closed) return;
        attempts++;
        const wait = Math.min(1000 * 2 ** (attempts - 1), MAX_BACKOFF);
        setConnection('reconnecting');
        let left = Math.ceil(wait / 1000);
        setRetryIn(left);
        countdown = window.setInterval(() => {
          left -= 1;
          setRetryIn(left);
        }, 1000);
        timer = window.setTimeout(() => {
          window.clearInterval(countdown);
          open();
        }, wait);
      };
    };

    open();
    return () => {
      closed = true;
      window.clearTimeout(timer);
      window.clearInterval(countdown);
      source?.close();
    };
  }, []);

  return { status, logs, connection, retryIn };
}
