import { useId, useState } from 'react';
import { api, ApiError } from '../api';
import { Frame } from '../components/Frame';
import { Empty, Failure, Loading, Notice } from '../components/States';
import { plural } from '../format';
import { useLoad } from '../hooks';
import type { ConfigKey, ConfigSection } from '../types';

// Config edits the YAML file through the API. Each key saves on its own: bools
// and enums on change, the others with Save once the text differs.

export function Config() {
  const list = useLoad(() => api.config(), []);
  const data = list.data;
  const [filter, setFilter] = useState('');

  const q = filter.trim().toLowerCase();
  const sections: ConfigSection[] = (data?.sections ?? [])
    .map((s) => ({
      ...s,
      keys: q ? s.keys.filter((k) => k.key.toLowerCase().includes(q) || k.doc.toLowerCase().includes(q)) : s.keys,
    }))
    .filter((s) => s.keys.length > 0);

  return (
    <section className="page" aria-labelledby="cfg-title">
      <header className="page__head cfg-head">
        <h1 id="cfg-title" className="h1">
          Config
        </h1>
        {data && <p className="meta cfg-path">{data.path}</p>}
      </header>
      <p className="muted page__help">
        Changes apply to the next run. Saving rewrites the file with ajai's own comments; comments you added by hand are not kept.
      </p>

      {list.error && <Failure message={list.error} onRetry={list.reload} />}
      {list.loading && !data && !list.error && <Loading />}

      {data && data.sections.length === 0 && <Empty>The config file has no settings.</Empty>}

      {data && data.sections.length > 0 && (
        <>
          <input
            type="search"
            className="cfg-filter"
            placeholder="Filter keys"
            aria-label="Filter keys"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />

          {sections.length > 0 && (
            <nav className="cfg-index" aria-label="Sections">
              {sections.map((s) => (
                <a
                  key={s.name}
                  href={`#${sectionId(s.name)}`}
                  onClick={(e) => {
                    e.preventDefault();
                    document.getElementById(sectionId(s.name))?.scrollIntoView({ block: 'start' });
                  }}
                >
                  {s.name}
                </a>
              ))}
            </nav>
          )}

          {sections.length === 0 && <Empty>No keys match “{filter.trim()}”.</Empty>}

          <div className="cfg-sections">
            {sections.map((s) => (
              <Frame key={s.name} id={sectionId(s.name)} caption={s.name} meta={plural(s.keys.length, 'key')}>
                <ul className="cfg-keys" aria-label={s.name}>
                  {s.keys.map((k) => (
                    <ConfigRow key={k.key} k={k} />
                  ))}
                </ul>
              </Frame>
            ))}
          </div>
        </>
      )}
    </section>
  );
}

function sectionId(name: string): string {
  return `cfg-${name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`;
}

// The env var that overrides a key: settings.window -> AJAI_CFG_SETTINGS_WINDOW.
function envName(key: string): string {
  return `AJAI_CFG_${key.toUpperCase().replace(/\./g, '_')}`;
}

// A list is one item per line in the editor and comma-separated in the API.
function listToValue(text: string): string {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .join(',');
}

function valueToList(value: string): string {
  return value
    .split(',')
    .map((l) => l.trim())
    .filter(Boolean)
    .join('\n');
}

type Note = { tone: 'ok' | 'error'; text: string } | null;

function ConfigRow({ k }: { k: ConfigKey }) {
  const nameId = useId();
  const isList = k.kind === 'list';
  const show = (v: string) => (isList ? valueToList(v) : v);
  const toApi = (v: string) => (isList ? listToValue(v) : v);

  const [saved, setSaved] = useState(k.value);
  const [draft, setDraft] = useState(show(k.value));
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note>(null);

  const changed = toApi(draft) !== saved;
  const immediate = k.kind === 'bool' || k.kind === 'enum';

  async function save(value: string) {
    const sent = show(value);
    setBusy(true);
    setNote(null);
    try {
      const res = await api.setConfig(k.key, value);
      setSaved(res.value);
      // Only replace the editor's text if the viewer has not typed since.
      setDraft((d) => (d === sent || immediate ? show(res.value) : d));
      setNote({ tone: 'ok', text: 'Saved' });
    } catch (e) {
      setNote({ tone: 'error', text: e instanceof ApiError ? e.message : 'The value could not be saved.' });
    } finally {
      setBusy(false);
    }
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (changed && !busy) save(toApi(draft));
  };

  const reset = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape' && changed) {
      e.preventDefault();
      setDraft(show(saved));
      setNote(null);
    }
  };

  const edit = (() => {
    if (k.kind === 'bool') {
      const on = saved === 'true';
      return (
        <label className="check">
          <input
            type="checkbox"
            checked={on}
            disabled={busy}
            aria-labelledby={nameId}
            onChange={(e) => save(e.target.checked ? 'true' : 'false')}
          />
          <span>{on ? 'On' : 'Off'}</span>
        </label>
      );
    }
    if (k.kind === 'enum') {
      return (
        <div className="cfg-field">
          <select aria-labelledby={nameId} value={saved} disabled={busy} onChange={(e) => save(e.target.value)}>
            {k.options.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
        </div>
      );
    }
    if (isList) {
      return (
        <div className="cfg-field cfg-field--wide">
          <textarea
            className="cfg-text"
            rows={Math.min(Math.max(draft.split('\n').length, 3), 10)}
            aria-labelledby={nameId}
            value={draft}
            disabled={busy}
            onChange={(e) => {
              setDraft(e.target.value);
              setNote(null);
            }}
          />
          <p className="meta">One per line</p>
        </div>
      );
    }
    return (
      <div className="cfg-field">
        <input
          type="text"
          inputMode={k.kind === 'int' ? 'numeric' : k.kind === 'float' ? 'decimal' : undefined}
          aria-labelledby={nameId}
          value={draft}
          disabled={busy}
          onChange={(e) => {
            setDraft(e.target.value);
            setNote(null);
          }}
        />
      </div>
    );
  })();

  return (
    <li className="cfg-key">
      <p id={nameId} className="cfg-key__name">
        {k.key}
      </p>
      <p className="cfg-key__doc">{k.doc}</p>

      <form className="cfg-key__edit" onSubmit={submit} onKeyDown={reset}>
        {edit}
        {!immediate && changed && (
          <button type="submit" className="btn btn--primary" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
        )}
      </form>

      {note && <Notice tone={note.tone}>{note.text}</Notice>}
      {k.env_override && (
        <p className="meta">Overridden by {envName(k.key)} in the environment; the file value is shown.</p>
      )}
      {k.daemon_restart && <p className="meta">Read when the daemon starts.</p>}
    </li>
  );
}
