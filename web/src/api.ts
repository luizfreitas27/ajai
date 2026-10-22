// Thin wrappers over the /api contract. Every failure becomes an ApiError that
// carries the HTTP status and the server's {"error"} text when it sent one.

export class ApiError extends Error {
  status: number;
  body: unknown;

  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      ...init,
      headers: init?.body ? { 'Content-Type': 'application/json', ...init?.headers } : init?.headers,
    });
  } catch {
    throw new ApiError(0, 'Could not reach ajai serve. Is it still running?', null);
  }
  const text = await res.text();
  let body: unknown = null;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    body = text;
  }
  if (!res.ok) {
    const msg =
      body && typeof body === 'object' && 'error' in body && typeof (body as { error: unknown }).error === 'string'
        ? (body as { error: string }).error
        : `Request failed with status ${res.status}.`;
    throw new ApiError(res.status, msg, body);
  }
  return body as T;
}

const post = (path: string, data: unknown) =>
  request<unknown>(path, { method: 'POST', body: JSON.stringify(data) });

export const api = {
  runs: (limit = 20) => request<import('./types').Run[]>(`/api/runs?limit=${limit}`),
  startRun: (market: string, dryRun: boolean) =>
    post('/api/runs', { market, dry_run: dryRun }) as Promise<{ pid: number; args: string[] }>,
  stopRun: (pid: number, started: string) =>
    post('/api/runs/stop', { pid, started }) as Promise<{ holder: unknown }>,

  jobs: (text: string, offset: number, limit: number) => {
    const q = new URLSearchParams({ offset: String(offset), limit: String(limit) });
    if (text) q.set('text', text);
    return request<import('./types').Job[]>(`/api/jobs?${q}`);
  },

  messages: (state: string) =>
    request<import('./types').MessagesResponse>(`/api/messages?state=${encodeURIComponent(state)}`),
  approveMessage: (id: number, digest: string) =>
    post(`/api/messages/${id}/approve`, { digest }) as Promise<{ id: number; state: string }>,
  approveAll: (digest: string) => post('/api/messages/approve-all', { digest }) as Promise<{ approved: number }>,
  startBackfill: () => post('/api/messages/backfill', {}) as Promise<{ pid: number; args: string[] }>,

  invites: (state: string) =>
    request<import('./types').Invite[]>(`/api/invites?limit=100${state ? `&state=${state}` : ''}`),

  answers: (state: 'pending' | 'approved') =>
    request<import('./types').Answer[]>(`/api/answers?state=${state}`),
  approveAnswer: (id: string, digest: string, answer?: string, blank?: boolean) =>
    post(
      `/api/answers/${encodeURIComponent(id)}/approve`,
      blank ? { digest, blank: true } : answer === undefined ? { digest } : { digest, answer },
    ),
};
