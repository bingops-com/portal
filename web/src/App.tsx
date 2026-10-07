import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Command as CommandIcon, Download, LogOut, Monitor, Moon, PencilLine, Plus, RotateCcw, Sun, Trash2 } from 'lucide-react';
import { stringify } from 'yaml';
import { api, type Page, type PortalConfig, type SummaryItem } from './api';
import { Mark } from './components/bits';
import { PageEditor, PageView } from './components/PageView';
import { Palette, type Command } from './components/Palette';
import { ago } from './format';

type Theme = 'auto' | 'light' | 'dark';

function readTheme(fallback?: string): Theme {
  try {
    const stored = localStorage.getItem('portal-theme');
    if (stored === 'light' || stored === 'dark' || stored === 'auto') return stored;
  } catch {
    /* storage unavailable: use the configured default */
  }
  return fallback === 'light' || fallback === 'dark' ? fallback : 'auto';
}

function useTheme(fallback?: string): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(() => readTheme(fallback));
  useEffect(() => setTheme(readTheme(fallback)), [fallback]);
  useEffect(() => {
    if (theme === 'auto') delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = theme;
  }, [theme]);
  const cycle = () => {
    const next: Theme = theme === 'auto' ? 'light' : theme === 'light' ? 'dark' : 'auto';
    setTheme(next);
    try {
      localStorage.setItem('portal-theme', next);
    } catch {
      /* the choice simply lasts for this visit */
    }
  };
  return [theme, cycle];
}

const slugFromPath = () => decodeURIComponent(window.location.pathname.replace(/^\/+|\/+$/g, ''));

function useSummary(enabled: boolean): SummaryItem[] {
  const [items, setItems] = useState<SummaryItem[]>([]);
  useEffect(() => {
    if (!enabled) return;
    const ctl = new AbortController();
    const load = () => {
      if (!document.hidden) api.summary(ctl.signal).then((r) => setItems(r.items)).catch(() => {});
    };
    load();
    const timer = setInterval(load, 30_000);
    return () => {
      ctl.abort();
      clearInterval(timer);
    };
  }, [enabled]);
  return items;
}

const rank = { ok: 0, unknown: 1, warn: 2, down: 3 } as const;
const stateColors = { ok: '#1443d6', unknown: '#1443d6', warn: '#c77700', down: '#c81e45' };

// Mirrors the worst state in the tab icon, for when the portal is pinned.
function setFavicon(color: string) {
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
  if (!link) {
    link = document.createElement('link');
    link.rel = 'icon';
    document.head.appendChild(link);
  }
  const svg = `<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><rect width='32' height='32' rx='8' fill='${color}'/><path d='M9 8v16h14' fill='none' stroke='#fff' stroke-width='4' stroke-linecap='round' stroke-linejoin='round'/></svg>`;
  link.href = `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

// Labels of the readouts whose state changed on the latest refresh; they
// pulse once so a change catches the eye.
function useChanged(items: SummaryItem[]): Set<string> {
  const previous = useRef<Map<string, string>>(new Map());
  const [changed, setChanged] = useState<Set<string>>(new Set());
  useEffect(() => {
    const next = new Set<string>();
    for (const it of items) {
      const before = previous.current.get(it.label);
      if (before !== undefined && before !== it.state) next.add(it.label);
      previous.current.set(it.label, it.state);
    }
    if (next.size === 0) return;
    setChanged(next);
    const timer = setTimeout(() => setChanged(new Set()), 2400);
    return () => clearTimeout(timer);
  }, [items]);
  return changed;
}

const themeLabels: Record<Theme, string> = { auto: 'Thème : système', light: 'Thème : clair', dark: 'Thème : sombre' };

export function App() {
  const [config, setConfig] = useState<PortalConfig | null>(null);
  const [failure, setFailure] = useState('');
  const [slug, setSlug] = useState(slugFromPath);
  const [draft, setDraft] = useState<Page[] | null>(null);
  const [draftIndex, setDraftIndex] = useState(0);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [theme, cycleTheme] = useTheme(config?.theme);
  const summary = useSummary(config !== null);
  const changed = useChanged(summary);
  const [palette, setPalette] = useState(false);
  const worst = summary.reduce<SummaryItem['state']>((w, s) => (rank[s.state] > rank[w] ? s.state : w), 'ok');
  const troubled = summary.filter((s) => s.state === 'warn' || s.state === 'down').length;

  const load = useCallback(() => {
    api.config().then(setConfig).catch((err: Error) => setFailure(err.message));
  }, []);
  useEffect(load, [load]);

  useEffect(() => {
    const onPop = () => setSlug(slugFromPath());
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  const pages = draft ?? config?.pages ?? [];
  const viewIndex = Math.max(0, pages.findIndex((p) => p.slug === slug));
  const index = draft ? Math.min(draftIndex, pages.length - 1) : viewIndex;
  const page = pages[index];

  useEffect(() => {
    if (config && page) document.title = `${troubled ? `(${troubled}) ` : ''}${page.name} | ${config.title}`;
  }, [config, page, troubled]);
  useEffect(() => setFavicon(stateColors[worst]), [worst]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPalette((open) => !open);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const go = (i: number) => {
    setConfirmDelete(false);
    if (draft) {
      setDraftIndex(i);
      return;
    }
    const target = pages[i];
    window.history.pushState(null, '', i === 0 ? '/' : `/${target.slug}`);
    setSlug(i === 0 ? '' : target.slug);
  };

  const startEdit = () => {
    setDraft(structuredClone(config!.pages));
    setDraftIndex(viewIndex);
    setNotice('');
  };
  const needsLogin = Boolean(config?.loginRequired && !config.user);
  const loginUrl = `/auth/login?return=${encodeURIComponent(`${window.location.pathname}#edit`)}`;

  // Coming back from the login page with #edit resumes what the person asked for.
  useEffect(() => {
    if (!config || window.location.hash !== '#edit') return;
    window.history.replaceState(null, '', window.location.pathname);
    if (!config.readOnly && !needsLogin) startEdit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [config === null]);
  const stopEdit = (next?: PortalConfig) => {
    setDraft(null);
    setConfirmDelete(false);
    if (next) {
      setConfig(next);
      const keep = next.pages[index] ?? next.pages[0];
      const first = keep === next.pages[0];
      window.history.replaceState(null, '', first ? '/' : `/${keep.slug}`);
      setSlug(first ? '' : keep.slug);
    }
  };

  const run = (action: Promise<PortalConfig>, done: string) => {
    setSaving(true);
    setNotice('');
    action
      .then((next) => {
        stopEdit(next);
        setNotice(done);
      })
      .catch((err: Error) =>
        setNotice(
          /connexion requise/.test(err.message)
            ? 'Votre session a expiré. Exportez la disposition en YAML pour ne rien perdre, puis reconnectez-vous.'
            : `Enregistrement impossible : ${err.message}`,
        ),
      )
      .finally(() => setSaving(false));
  };

  const updatePage = (next: Page) => setDraft((d) => d!.map((p, i) => (i === index ? next : p)));
  const addPage = () => {
    setDraft((d) => [...d!, { name: `Page ${d!.length + 1}`, slug: '', columns: [{ size: 'full', widgets: [] }] }]);
    setDraftIndex(pages.length);
  };
  const removePage = () => {
    setDraft((d) => d!.filter((_, i) => i !== index));
    setDraftIndex(Math.max(0, index - 1));
    setConfirmDelete(false);
  };

  const exportYaml = () => {
    const doc = { title: config!.title, theme: config!.theme || 'auto', pages: pages.map(({ name, slug: s, columns }) => ({ name, ...(s ? { slug: s } : {}), columns })) };
    const blob = new Blob([stringify(doc, { lineWidth: 0 })], { type: 'application/yaml' });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = 'portal.yaml';
    link.click();
    URL.revokeObjectURL(link.href);
  };

  // Brings the widget behind a readout into view, switching page if needed.
  const reveal = (type?: string) => {
    if (!type || draft) return;
    const has = (p: Page) => p.columns.some((c) => c.widgets.some((w) => w.type === type));
    const target = has(pages[index]) ? index : pages.findIndex(has);
    if (target < 0) return;
    if (target !== index) go(target);
    setTimeout(() => {
      const el = document.querySelector(`.widget-${type}`);
      if (!el) return;
      el.scrollIntoView({ behavior: 'smooth', block: 'center' });
      el.classList.add('widget-flash');
      setTimeout(() => el.classList.remove('widget-flash'), 1800);
    }, 80);
  };

  const commands = useMemo<Command[]>(() => {
    if (!config) return [];
    const list: Command[] = pages.map((p, i) => ({ label: p.name, hint: 'Page', run: () => go(i) }));
    for (const p of config.pages)
      for (const c of p.columns)
        for (const w of c.widgets)
          if (w.type === 'bookmarks')
            for (const g of w.options?.groups ?? [])
              for (const l of g.links ?? []) if (l.url) list.push({ label: l.title, hint: g.title || 'Lien', run: () => window.open(l.url, '_blank', 'noreferrer') });
    list.push({ label: 'Changer de thème', hint: 'Action', run: cycleTheme });
    if (!config.readOnly && !draft) {
      list.push(
        config.loginRequired && !config.user
          ? { label: 'Se connecter pour modifier', hint: 'Action', run: () => window.location.assign(loginUrl) }
          : { label: 'Modifier la disposition', hint: 'Action', run: startEdit },
      );
    }
    if (config.user) list.push({ label: 'Se déconnecter', hint: 'Action', run: () => window.location.assign('/auth/logout') });
    return list;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [config, pages, draft, theme, index]);

  const ThemeIcon = theme === 'light' ? Sun : theme === 'dark' ? Moon : Monitor;
  const dirty = useMemo(() => draft !== null && JSON.stringify(draft) !== JSON.stringify(config?.pages), [draft, config]);

  if (failure) {
    return (
      <main className="fatal">
        <h1>Le portail ne peut pas démarrer</h1>
        <p>{failure}</p>
        <button className="btn btn-primary" onClick={() => { setFailure(''); load(); }}>
          Réessayer
        </button>
      </main>
    );
  }
  if (!config || !page) return <div className="boot" aria-label="Chargement" />;

  return (
    <>
      <header className="band" data-state={worst}>
        <div className="band-top">
          <a className="wordmark" href="/" onClick={(e) => { e.preventDefault(); go(0); }}>
            {config.title}
          </a>
          <nav aria-label="Pages">
            {pages.map((p, i) => (
              <a key={i} href={i === 0 ? '/' : `/${p.slug}`} aria-current={i === index ? 'page' : undefined} onClick={(e) => { e.preventDefault(); go(i); }}>
                {p.name}
              </a>
            ))}
          </nav>
          <div className="band-tools">
            <button className="band-btn band-btn-text band-btn-keys" onClick={() => setPalette(true)} aria-label="Ouvrir la palette de commandes" title="Palette de commandes">
              <CommandIcon size={15} aria-hidden /> <kbd>Ctrl K</kbd>
            </button>
            <button className="band-btn" onClick={cycleTheme} aria-label={themeLabels[theme]} title={themeLabels[theme]}>
              <ThemeIcon size={17} aria-hidden />
            </button>
            {!config.readOnly && !draft && needsLogin && (
              <a className="band-btn band-btn-text" href={loginUrl}>
                <PencilLine size={16} aria-hidden /> Se connecter pour modifier
              </a>
            )}
            {!config.readOnly && !draft && !needsLogin && (
              <button className="band-btn band-btn-text" onClick={startEdit}>
                <PencilLine size={16} aria-hidden /> Modifier
              </button>
            )}
            {config.user && !draft && (
              <a className="band-btn" href="/auth/logout" aria-label={`Se déconnecter (${config.user})`} title={`Connecté : ${config.user}. Se déconnecter`}>
                <LogOut size={16} aria-hidden />
              </a>
            )}
          </div>
        </div>
        {summary.length > 0 && (
          <ul className="readouts" aria-label="État du lab">
            {summary.map((s) => (
              <li key={s.label} className={changed.has(s.label) ? 'readout-changed' : undefined}>
                <button onClick={() => reveal(s.type)} title="Voir le détail">
                  <Mark state={s.state} />
                  <span className="readout-text">
                    <span className="readout-label">
                      {s.label}
                      {s.since && s.state !== 'ok' && <span className="readout-since">{ago(s.since)}</span>}
                    </span>
                    <span className="readout-detail">{s.detail}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </header>

      {draft && (
        <div className="editbar" role="region" aria-label="Édition de la page">
          <label className="editbar-name">
            <span>Nom de la page</span>
            <input value={page.name} onChange={(e) => updatePage({ ...page, name: e.target.value, slug: '' })} />
          </label>
          <button className="btn" disabled={page.columns.length >= 4} onClick={() => updatePage({ ...page, columns: [...page.columns, { size: 'small', widgets: [] }] })}>
            <Plus size={15} aria-hidden /> Ajouter une colonne
          </button>
          <button className="btn" onClick={addPage}>
            <Plus size={15} aria-hidden /> Nouvelle page
          </button>
          {pages.length > 1 &&
            (confirmDelete ? (
              <button className="btn btn-danger" onClick={removePage}>
                Confirmer la suppression
              </button>
            ) : (
              <button className="btn" onClick={() => setConfirmDelete(true)}>
                <Trash2 size={15} aria-hidden /> Supprimer la page
              </button>
            ))}
          <span className="editbar-gap" />
          <button className="btn" onClick={exportYaml} title="Télécharger cette disposition pour la versionner dans Git">
            <Download size={15} aria-hidden /> Exporter en YAML
          </button>
          {config.source === 'custom' && (
            <button className="btn" disabled={saving} onClick={() => run(api.resetLayout(), 'Disposition du fichier YAML rétablie.')}>
              <RotateCcw size={15} aria-hidden /> Revenir au YAML
            </button>
          )}
          <button className="btn" disabled={saving} onClick={() => stopEdit()}>
            Annuler
          </button>
          <button className="btn btn-primary" disabled={saving || !dirty} onClick={() => run(api.saveLayout(draft), 'Disposition enregistrée.')}>
            {saving ? 'Enregistrement…' : 'Enregistrer'}
          </button>
        </div>
      )}

      {notice && (
        <p className="toast" role="status">
          {notice}
          <button className="icon-btn" onClick={() => setNotice('')} aria-label="Fermer le message">
            ×
          </button>
        </p>
      )}

      {palette && <Palette commands={commands} onClose={() => setPalette(false)} />}

      <main className="page">
        {draft ? <PageEditor key={index} page={page} onChange={updatePage} /> : <PageView key={page.slug} page={page} />}
      </main>
    </>
  );
}
