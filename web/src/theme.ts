import { useCallback, useState } from 'react';

// The theme is System (follow prefers-color-scheme) until the viewer picks Light
// or Dark. The choice is kept in localStorage; every access is guarded, because
// storage can be blocked, and the page then simply follows the system.
export type Theme = 'system' | 'light' | 'dark';

const KEY = 'ajai-theme';
const ORDER: Theme[] = ['system', 'light', 'dark'];
const LABEL: Record<Theme, string> = { system: 'System', light: 'Light', dark: 'Dark' };

export function readTheme(): Theme {
  try {
    const v = window.localStorage.getItem(KEY);
    if (v === 'light' || v === 'dark') return v;
  } catch {
    /* blocked storage: follow the system */
  }
  return 'system';
}

function writeTheme(t: Theme) {
  try {
    if (t === 'system') window.localStorage.removeItem(KEY);
    else window.localStorage.setItem(KEY, t);
  } catch {
    /* not saved: the choice lasts until the page is reloaded */
  }
}

// applyTheme sets data-theme on <html>; no attribute means System. index.html
// runs the same logic before first paint, so the page never flashes.
export function applyTheme(t: Theme) {
  const root = document.documentElement;
  if (t === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', t);
}

export function useTheme(): { theme: Theme; label: string; cycle: () => void } {
  const [theme, setTheme] = useState<Theme>(readTheme);
  const cycle = useCallback(() => {
    setTheme((prev) => {
      const next = ORDER[(ORDER.indexOf(prev) + 1) % ORDER.length];
      writeTheme(next);
      applyTheme(next);
      return next;
    });
  }, []);
  return { theme, label: LABEL[theme], cycle };
}
