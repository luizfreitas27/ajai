import { useEffect, useState } from 'react';
import { useEvents, type Connection } from './events';
import { Dashboard } from './pages/Dashboard';
import { Jobs } from './pages/Jobs';
import { Messages } from './pages/Messages';
import { Invites } from './pages/Invites';
import { Answers } from './pages/Answers';
import { useTheme } from './theme';

const PAGES = [
  { id: 'dashboard', label: 'Dashboard' },
  { id: 'jobs', label: 'Jobs' },
  { id: 'messages', label: 'Messages' },
  { id: 'invites', label: 'Invites' },
  { id: 'answers', label: 'Answers' },
] as const;

type PageId = (typeof PAGES)[number]['id'];

function pageFromHash(): PageId {
  const id = window.location.hash.replace(/^#\/?/, '');
  return PAGES.some((p) => p.id === id) ? (id as PageId) : 'dashboard';
}

function useHashPage(): PageId {
  const [page, setPage] = useState<PageId>(pageFromHash);
  useEffect(() => {
    const on = () => setPage(pageFromHash());
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return page;
}

const CONNECTION_TEXT: Record<Connection, string> = {
  connecting: 'Connecting',
  connected: 'Live',
  reconnecting: 'Reconnecting',
};

function ConnectionState({ connection, retryIn }: { connection: Connection; retryIn: number }) {
  const text =
    connection === 'reconnecting' && retryIn > 0 ? `Reconnecting in ${retryIn}s` : CONNECTION_TEXT[connection];
  return (
    <p className={`conn conn--${connection}`} role="status" aria-live="polite">
      <span className="conn__dot" aria-hidden="true" />
      {text}
    </p>
  );
}

export default function App() {
  const page = useHashPage();
  const events = useEvents();
  const [menuOpen, setMenuOpen] = useState(false);
  const theme = useTheme();

  useEffect(() => setMenuOpen(false), [page]);

  // The header's Start run goes to the dashboard and puts the cursor on the market.
  const startRun = () => {
    window.location.hash = '#/dashboard';
    window.setTimeout(() => {
      document.getElementById('start')?.scrollIntoView({ block: 'start' });
      document.getElementById('market')?.focus();
    }, 0);
  };

  return (
    <div className="shell">
      <header className="top">
        <a className="wordmark" href="#/dashboard" aria-label="ajai, go to the dashboard">
          ajai
        </a>
        <nav className="nav nav--top" aria-label="Main">
          {PAGES.map((p) => (
            <a key={p.id} href={`#/${p.id}`} aria-current={page === p.id ? 'page' : undefined}>
              {p.label}
            </a>
          ))}
        </nav>
        <div className="top__actions">
          <ConnectionState connection={events.connection} retryIn={events.retryIn} />
          <button type="button" className="theme-toggle" onClick={theme.cycle} aria-label={`Theme: ${theme.label}. Change theme`}>
            Theme: {theme.label}
          </button>
          <button type="button" className="btn btn--primary" onClick={startRun}>
            Start run
          </button>
          <button
            type="button"
            className="btn btn--ghost menu-toggle"
            aria-expanded={menuOpen}
            aria-controls="menu"
            onClick={() => setMenuOpen((v) => !v)}
          >
            {menuOpen ? 'Close' : 'Menu'}
          </button>
        </div>
        <nav id="menu" className="menu" aria-label="Main" hidden={!menuOpen}>
          {PAGES.map((p) => (
            <a key={p.id} href={`#/${p.id}`} aria-current={page === p.id ? 'page' : undefined}>
              {p.label}
            </a>
          ))}
          <button type="button" className="theme-toggle theme-toggle--menu" onClick={theme.cycle}>
            Theme: {theme.label}
          </button>
        </nav>
      </header>

      <main className="main" id="main">
        {page === 'dashboard' && <Dashboard events={events} />}
        {page === 'jobs' && <Jobs />}
        {page === 'messages' && <Messages busy={!!events.status?.holder} />}
        {page === 'invites' && <Invites />}
        {page === 'answers' && <Answers />}
      </main>
    </div>
  );
}
