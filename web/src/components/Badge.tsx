// Badge names an outcome or state. The text carries the meaning; the tone only
// adds colour, so it reads the same without it.
export function Badge({ tone, children }: { tone: string; children: React.ReactNode }) {
  return <span className={`badge badge--${tone}`}>{children}</span>;
}

const OUTCOME_TONE: Record<string, string> = {
  applied: 'ok',
  external: 'info',
  failed: 'error',
  skipped: 'muted',
  sent: 'ok',
  approved: 'info',
  queued: 'warn',
  sending: 'info',
  already: 'muted',
  unavailable: 'muted',
  no_recipient: 'muted',
  pending: 'warn',
};

export function toneFor(state: string): string {
  return OUTCOME_TONE[state] ?? 'muted';
}
