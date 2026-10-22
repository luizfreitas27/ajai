// formatTime shows a stored RFC 3339 time in the viewer's zone, or a dash when
// there is none (a run not yet ended, a message not sent).
export function formatTime(iso: string): string {
  if (!iso || iso.startsWith('0001-')) return '–';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

export function plural(n: number, one: string, many = one + 's'): string {
  return `${n} ${n === 1 ? one : many}`;
}
