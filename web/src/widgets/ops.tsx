import { useContext, useState } from 'react';
import { api, type Options } from '../api';
import { useDetail, type DetailData } from '../components/Detail';
import { Bell, Check, Copy, DatabaseBackup, Rocket, RotateCw } from 'lucide-react';
import { AnimatedNumber, Empty, ExpandedContext, Gauge, Mark, Sparkline, ext, useFlip, type State } from '../components/bits';
import { ago, age, formatStat } from '../format';

type Props<T> = { data: T; options: Options; widgetId: string };

// A row title that opens the detail panel; plain text while editing.
function Open({ onOpen, copy, children }: { onOpen?: () => void; copy?: string; children: React.ReactNode }) {
  const [copied, setCopied] = useState(false);
  if (!onOpen) return <span className="row-title">{children}</span>;
  return (
    <span className="row-head">
      <button className="row-title row-open" onClick={onOpen}>
        {children}
      </button>
      {copy && (
        <button
          className="row-action"
          aria-label={`Copier ${copy}`}
          title={copied ? 'Copié' : 'Copier le nom'}
          onClick={() => {
            navigator.clipboard?.writeText(copy).then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            });
          }}
        >
          {copied ? <Check size={13} aria-hidden /> : <Copy size={13} aria-hidden />}
        </button>
      )}
    </span>
  );
}

function More<T>({ items, limit, children }: { items: T[]; limit: number; children: (shown: T[]) => React.ReactNode }) {
  const [open, setOpen] = useState(false);
  const expanded = useContext(ExpandedContext);
  const shown = open || expanded ? items : items.slice(0, limit);
  return (
    <>
      {children(shown)}
      {items.length > limit && !expanded && (
        <button className="more" onClick={() => setOpen(!open)} aria-expanded={open}>
          {open ? 'Réduire la liste' : `Afficher les ${items.length - limit} autres`}
        </button>
      )}
    </>
  );
}

// --- Cluster ---

type Cluster = {
  name: string;
  version: string;
  nodes: { name: string; ready: boolean; roles: string[]; version: string; os: string; created: string }[];
  nodesReady: number;
  namespaces: number;
  pods: Record<string, number>;
  podsTotal: number;
  problems: { namespace: string; name: string; reason: string; restarts: number }[];
};

export function ClusterWidget({ data }: Props<Cluster>) {
  return (
    <>
      <dl className="facts">
        <div>
          <dt>Nœuds prêts</dt>
          <dd>
            {data.nodesReady}/{data.nodes.length}
          </dd>
        </div>
        <div>
          <dt>Pods actifs</dt>
          <dd>
            <AnimatedNumber flash value={data.pods.Running ?? 0} format={(n) => String(Math.round(n))} />
          </dd>
        </div>
        <div>
          <dt>Namespaces</dt>
          <dd>{data.namespaces}</dd>
        </div>
      </dl>
      <ul className="rows">
        {data.nodes.map((n) => (
          <li key={n.name} className="row">
            <Mark state={n.ready ? 'ok' : 'down'} label={n.ready ? 'Prêt' : 'Non prêt'} />
            <div className="row-main">
              <span className="row-title">{n.name}</span>
              <span className="row-sub">{[n.roles.join(', ') || 'worker', n.os].filter(Boolean).join(', ')}</span>
            </div>
            <span className="row-aside">{n.version}</span>
          </li>
        ))}
      </ul>
      {data.problems.length === 0 ? (
        <Empty>Aucun pod en difficulté.</Empty>
      ) : (
        <More items={data.problems} limit={5}>
          {(shown) => (
            <ul className="rows">
              {shown.map((p) => (
                <li key={`${p.namespace}/${p.name}`} className="row">
                  <Mark state="warn" label="En difficulté" />
                  <div className="row-main">
                    <span className="row-title">{p.name}</span>
                    <span className="row-sub">{p.namespace}</span>
                  </div>
                  <span className="row-aside">
                    {p.reason}
                    {p.restarts > 0 && <small>{p.restarts} redémarrages</small>}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </More>
      )}
    </>
  );
}
export const clusterBadge = (d: Cluster) => `${d.name} ${d.version}`;

// --- Workloads ---

type Workloads = {
  cluster: string;
  healthy: number;
  workloads: { kind: string; namespace: string; name: string; ready: number; desired: number; healthy: boolean; image: string }[];
};
const kinds: Record<string, string> = { Deployment: 'deploy', StatefulSet: 'sts', DaemonSet: 'ds' };

export function WorkloadsWidget({ data, options, widgetId }: Props<Workloads>) {
  const detail = useDetail();
  const flip = useFlip<HTMLUListElement>();
  if (data.workloads.length === 0) return <Empty>Aucun workload dans les namespaces sélectionnés.</Empty>;
  return (
    <More items={data.workloads} limit={Number(options.limit) || 10}>
      {(shown) => (
        <ul className="rows" ref={flip}>
          {shown.map((w) => (
            <li key={`${w.kind}/${w.namespace}/${w.name}`} data-flip={`${w.kind}/${w.namespace}/${w.name}`} className="row">
              <Mark state={w.healthy ? 'ok' : w.ready > 0 ? 'warn' : 'down'} label={w.healthy ? 'Prêt' : 'Incomplet'} />
              <div className="row-main">
                <Open
                  copy={w.name}
                  onOpen={
                    detail
                      ? () => detail({ title: w.name, subtitle: `${w.kind} dans ${w.namespace}`, load: () => api.detail<DetailData>(widgetId, { kind: w.kind, namespace: w.namespace, name: w.name }) })
                      : undefined
                  }
                >
                  {w.name}
                </Open>
                <span className="row-sub">
                  {w.namespace} ({kinds[w.kind] ?? w.kind})
                </span>
              </div>
              <span className="row-image" title={w.image}>
                {w.image}
              </span>
              <span className="row-aside">
                {w.ready}/{w.desired}
              </span>
            </li>
          ))}
        </ul>
      )}
    </More>
  );
}
export const workloadsBadge = (d: Workloads) => `${d.healthy}/${d.workloads.length} prêts`;

// --- Events ---

type Events = { events: { reason: string; message: string; object: string; namespace: string; count: number; lastSeen: string }[] };

export function EventsWidget({ data }: Props<Events>) {
  const detail = useDetail();
  if (data.events.length === 0) return <Empty>Aucun avertissement récent dans le cluster.</Empty>;
  return (
    <ul className="rows">
      {data.events.map((e, i) => (
        <li key={i} className="row row-top">
          <div className="row-main">
            <Open
              onOpen={
                detail
                  ? () =>
                      detail({
                        title: e.reason,
                        subtitle: `${e.namespace}/${e.object}`,
                        load: async () => ({
                          facts: [
                            { label: 'Dernière occurrence', value: new Date(e.lastSeen).toLocaleString('fr-FR') },
                            { label: 'Occurrences', value: String(Math.max(1, e.count)) },
                          ],
                          sections: [{ title: 'Message', rows: [{ title: e.message }] }],
                        }),
                      })
                  : undefined
              }
            >
              {e.reason}
              {e.count > 1 && <span className="count">×{e.count}</span>}
            </Open>
            <span className="row-sub">
              {e.namespace}/{e.object}
            </span>
            <span className="row-text">{e.message}</span>
          </div>
          <span className="row-aside">{age(e.lastSeen)}</span>
        </li>
      ))}
    </ul>
  );
}

// --- Argo CD ---

type Argo = {
  healthy: number;
  tracked: number;
  url?: string;
  apps: { name: string; project: string; sync: string; health: string; revision: string; path: string; syncedAt?: string; url?: string; ignored?: boolean }[];
};

function argoState(a: Argo['apps'][number]): State {
  if (a.health === 'Degraded' || a.health === 'Missing') return 'down';
  if (a.sync === 'Synced' && a.health === 'Healthy') return 'ok';
  return a.sync === 'Unknown' && a.health === 'Unknown' ? 'unknown' : 'warn';
}

export function ArgoWidget({ data, options, widgetId }: Props<Argo>) {
  const detail = useDetail();
  const flip = useFlip<HTMLUListElement>();
  if (data.apps.length === 0) return <Empty>Aucune Application Argo CD visible avec ce compte de service.</Empty>;
  return (
    <More items={data.apps} limit={Number(options.limit) || 12}>
      {(shown) => (
        <ul className="rows" ref={flip}>
          {shown.map((a) => {
            const state = a.ignored ? 'none' : argoState(a);
            const label = a.sync !== 'Synced' ? a.sync : a.health === 'Healthy' ? 'Synced' : a.health;
            return (
              <li key={a.name} data-flip={a.name} className="row">
                <Mark state={state} />
                <div className="row-main">
                  <Open
                    copy={a.name}
                    onOpen={
                      detail
                        ? () => detail({ title: a.name, subtitle: 'Application Argo CD', linkLabel: 'Ouvrir dans Argo CD', load: () => api.detail<DetailData>(widgetId, { name: a.name }) })
                        : undefined
                    }
                  >
                    {a.name}
                  </Open>
                  <span className="row-sub">{a.path || a.project}</span>
                </div>
                <span className={`tag tag-${state === 'none' ? 'unknown' : state}`} title={a.ignored ? 'Écart accepté : ignorée dans les compteurs' : undefined}>
                  {label}
                </span>
                <span className="row-aside">
                  {a.revision}
                  {a.syncedAt && <small>{age(a.syncedAt)}</small>}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </More>
  );
}
export const argoBadge = (d: Argo) => `${d.healthy}/${d.tracked} à jour`;

// --- Gatus ---

type Gatus = {
  up: number;
  url?: string;
  endpoints: { name: string; group: string; key: string; up: boolean; uptime: number; window: string; ms: number; url?: string; results: { ok: boolean; ms: number; t: string }[] }[];
};

export function GatusWidget({ data, widgetId }: Props<Gatus>) {
  const detail = useDetail();
  if (data.endpoints.length === 0) return <Empty>Gatus ne surveille encore aucun endpoint.</Empty>;
  const groups = new Map<string, Gatus['endpoints']>();
  for (const e of data.endpoints) groups.set(e.group, [...(groups.get(e.group) ?? []), e]);
  return (
    <>
      {[...groups].map(([group, endpoints]) => (
        <div key={group} className="group">
          {group && <h3>{group}</h3>}
          <ul className="rows">
            {endpoints.map((e) => (
              <li key={e.name} className="row row-wrap">
                <Mark state={e.up ? 'ok' : 'down'} label={e.up ? 'En ligne' : 'Hors ligne'} />
                <div className="row-main">
                  <Open
                    onOpen={
                      detail
                        ? () => detail({ title: e.name, subtitle: `Disponibilité ${formatStat(e.uptime, 'percent')} (${e.window})`, load: () => api.detail<DetailData>(widgetId, { key: e.key }) })
                        : undefined
                    }
                  >
                    {e.name}
                  </Open>
                </div>
                <span className="row-aside" title={`Disponibilité sur ${e.window}`}>
                  {formatStat(e.uptime, 'percent')}
                  <small>{Math.round(e.ms)} ms</small>
                </span>
                <span className="ticks" aria-hidden>
                  {e.results.map((r) => (
                    <i key={r.t} className={r.ok ? 'tick-ok' : 'tick-down'} title={`${new Date(r.t).toLocaleString('fr-FR')}, ${Math.round(r.ms)} ms`} />
                  ))}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </>
  );
}
export const gatusBadge = (d: Gatus) => `${d.up}/${d.endpoints.length} en ligne`;

// --- Prometheus ---

type Prom = { stats: { label: string; value: number | null; format: string; state: State; series?: number[]; error?: string }[] };

export function PrometheusWidget({ data, widgetId }: Props<Prom>) {
  const detail = useDetail();
  return (
    <div className="stats">
      {data.stats.map((s, i) => (
        <div
          key={s.label}
          className={`stat stat-${s.state}${detail && !s.error ? ' stat-open' : ''}`}
          {...(detail && !s.error
            ? {
                role: 'button',
                tabIndex: 0,
                title: 'Voir l’historique',
                onClick: () => detail({ title: s.label, subtitle: 'Historique Prometheus', ranges: true, load: (range) => api.detail<DetailData>(widgetId, { stat: String(i), range }) }),
                onKeyDown: (e: React.KeyboardEvent) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    (e.currentTarget as HTMLElement).click();
                  }
                },
              }
            : {})}
        >
          <span className="stat-label">{s.label}</span>
          {s.format === 'percent' && s.value !== null && !s.error ? (
            <Gauge value={s.value} state={s.state}>
              <AnimatedNumber value={s.value} format={(n) => `${Math.round(n)}`} />
              <small>%</small>
            </Gauge>
          ) : (
            <span className="stat-value">{s.error || s.value === null ? 'n/d' : <AnimatedNumber flash value={s.value} format={(n) => formatStat(s.format === 'number' && Number.isInteger(s.value) ? Math.round(n) : n, s.format)} />}</span>
          )}
          {s.error ? <span className="stat-note">{s.error}</span> : <Sparkline values={s.series} />}
        </div>
      ))}
    </div>
  );
}

// --- Alerts ---

type Alerts = { alerts: { name: string; severity: string; namespace?: string; summary?: string; since: string; description?: string; labels?: Record<string, string> }[] };

export function AlertsWidget({ data }: Props<Alerts>) {
  const detail = useDetail();
  const open = (a: Alerts['alerts'][number]) => () =>
    detail?.({
      title: a.name,
      subtitle: `Alerte ${a.severity || 'sans sévérité'}`,
      load: async () => ({
        facts: [
          { label: 'Active depuis', value: new Date(a.since).toLocaleString('fr-FR') },
          ...Object.entries(a.labels ?? {})
            .filter(([k]) => !['alertname', 'severity', 'prometheus', 'cluster', 'environment'].includes(k))
            .map(([label, value]) => ({ label, value })),
        ],
        sections: [{ title: 'Description', empty: 'Cette alerte n’a pas de description.', rows: [a.summary, a.description].filter((t): t is string => Boolean(t)).map((t) => ({ title: t })) }],
      }),
    });
  if (data.alerts.length === 0) return <Empty>Aucune alerte active.</Empty>;
  return (
    <More items={data.alerts} limit={6}>
      {(shown) => (
        <ul className="rows">
          {shown.map((a, i) => (
            <li key={i} className="row row-top">
              <Mark state={a.severity === 'critical' ? 'down' : 'warn'} label={a.severity || 'alerte'} />
              <div className="row-main">
                <Open onOpen={detail ? open(a) : undefined}>{a.name}</Open>
                {a.namespace && <span className="row-sub">{a.namespace}</span>}
                {a.summary && <span className="row-text">{a.summary}</span>}
              </div>
              <span className="row-aside">{age(a.since)}</span>
            </li>
          ))}
        </ul>
      )}
    </More>
  );
}
export const alertsBadge = (d: Alerts) => (d.alerts.length ? `${d.alerts.length} actives` : '');

// --- Bookmarks ---

type Bookmarks = { groups: { title: string; links: { title: string; url: string; description?: string; icon?: string; status?: 'up' | 'down'; ms?: number }[] }[] };

// `si:<slug>` loads a brand icon from the Simple Icons CDN; an http(s) URL is
// used as is. Anything that fails to load falls back to the monogram.
function LinkIcon({ title, icon }: { title: string; icon?: string }) {
  const [failed, setFailed] = useState(false);
  const slug = icon?.startsWith('si:') ? icon.slice(3).toLowerCase() : '';
  const src = slug && /^[a-z0-9]+$/.test(slug) ? `https://cdn.simpleicons.org/${slug}` : icon && /^https?:\/\//.test(icon) ? icon : '';
  if (!src || failed) {
    return (
      <span className="monogram" aria-hidden>
        {title.slice(0, 1).toUpperCase()}
      </span>
    );
  }
  return (
    <span className="monogram monogram-icon" aria-hidden>
      <img src={src} alt="" width={18} height={18} loading="lazy" onError={() => setFailed(true)} />
    </span>
  );
}

export function BookmarksWidget({ data }: Props<Bookmarks>) {
  if (data.groups.length === 0) return <Empty>Ajoutez des liens dans les réglages du widget.</Empty>;
  return (
    <>
      {data.groups.map((g) => (
        <div key={g.title} className="group">
          {g.title && <h3>{g.title}</h3>}
          <ul className="links">
            {g.links.map((l) => (
              <li key={l.url + l.title}>
                <a {...ext(l.url)} className="link">
                  <LinkIcon title={l.title} icon={l.icon} />
                  <span className="row-main">
                    <span className="row-title">{l.title}</span>
                    {l.description && <span className="row-sub">{l.description}</span>}
                  </span>
                  {l.status && <Mark state={l.status === 'up' ? 'ok' : 'down'} label={l.status === 'up' ? `En ligne, ${l.ms} ms` : 'Ne répond pas'} />}
                </a>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </>
  );
}

// --- Certificates ---

type Certificates = {
  valid: number;
  warnDays: number;
  certificates: { namespace: string; name: string; host: string; ready: boolean; reason?: string; notAfter?: string; state: State }[];
};

const daysLeft = (date?: string) => (date ? Math.floor((new Date(date).getTime() - Date.now()) / 86_400_000) : null);

export function CertificatesWidget({ data, options, widgetId }: Props<Certificates>) {
  const detail = useDetail();
  if (data.certificates.length === 0) return <Empty>Aucun certificat cert-manager dans les namespaces sélectionnés.</Empty>;
  return (
    <More items={data.certificates} limit={Number(options.limit) || 8}>
      {(shown) => (
        <ul className="rows">
          {shown.map((c) => {
            const days = daysLeft(c.notAfter);
            return (
              <li key={`${c.namespace}/${c.name}`} className="row">
                <Mark state={c.state} label={c.state === 'ok' ? 'Valide' : c.state === 'warn' ? 'Expire bientôt' : 'À traiter'} />
                <div className="row-main">
                  <Open
                    onOpen={
                      detail
                        ? () => detail({ title: c.host || c.name, subtitle: 'Certificat cert-manager', load: () => api.detail<DetailData>(widgetId, { namespace: c.namespace, name: c.name }) })
                        : undefined
                    }
                  >
                    {c.host || c.name}
                  </Open>
                  <span className="row-sub">{c.namespace}</span>
                </div>
                <span className="row-aside">
                  {!c.ready ? c.reason || 'Non émis' : days === null ? 'n/d' : days < 0 ? 'Expiré' : `${days} j`}
                  {c.notAfter && <small>{new Date(c.notAfter).toLocaleDateString('fr-FR', { day: 'numeric', month: 'short' })}</small>}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </More>
  );
}
export const certificatesBadge = (d: Certificates) => `${d.valid}/${d.certificates.length} valides`;

// --- Backups ---

type Backups = {
  healthy: number;
  maxAgeHours: number;
  backups: { kind: string; namespace: string; name: string; lastSuccess?: string; detail?: string; state: State }[];
};

export function BackupsWidget({ data, widgetId }: Props<Backups>) {
  const detail = useDetail();
  return (
    <ul className="rows">
      {data.backups.map((b) => (
        <li key={`${b.kind}/${b.namespace}/${b.name}`} className="row row-top">
          <Mark state={b.state} label={b.state === 'ok' ? 'À jour' : b.state === 'unknown' ? 'En attente' : 'En retard'} />
          <div className="row-main">
            <Open
              onOpen={
                detail
                  ? () => detail({ title: b.name, subtitle: `Sauvegarde ${b.kind}`, load: () => api.detail<DetailData>(widgetId, { kind: b.kind, namespace: b.namespace, name: b.name }) })
                  : undefined
              }
            >
              {b.name}
            </Open>
            <span className="row-sub">
              {b.kind}, {b.namespace}
            </span>
            {b.detail && <span className="row-text">{b.detail}</span>}
          </div>
          <span className="row-aside">{b.lastSuccess ? age(b.lastSuccess) : 'jamais'}</span>
        </li>
      ))}
    </ul>
  );
}
export const backupsBadge = (d: Backups) => `${d.healthy}/${d.backups.length} à jour`;

// --- Activity ---

type Activity = { items: { time: string; kind: 'deploy' | 'alert' | 'backup' | 'restart'; title: string; detail?: string; state: State }[] };

const activityIcons = { deploy: Rocket, alert: Bell, backup: DatabaseBackup, restart: RotateCw };
const activityNames = { deploy: 'Déploiement', alert: 'Alerte', backup: 'Sauvegarde', restart: 'Redémarrage' };

export function ActivityWidget({ data }: Props<Activity>) {
  if (data.items.length === 0) return <Empty>Rien à signaler sur la période : ni déploiement, ni alerte, ni redémarrage.</Empty>;
  return (
    <ol className="timeline">
      {data.items.map((it, i) => {
        const Icon = activityIcons[it.kind] ?? Bell;
        return (
          <li key={i} className={`timeline-item timeline-${it.state}`}>
            <span className="timeline-icon" role="img" aria-label={activityNames[it.kind]} title={activityNames[it.kind]}>
              <Icon size={14} aria-hidden />
            </span>
            <div className="row-main">
              <span className="row-title">{it.title}</span>
              {it.detail && <span className="row-text">{it.detail}</span>}
            </div>
            <time className="row-aside" dateTime={it.time} title={new Date(it.time).toLocaleString('fr-FR')}>
              {ago(it.time)}
            </time>
          </li>
        );
      })}
    </ol>
  );
}

// --- Game server ---

type GameServer = { online: boolean; name?: string; map?: string; game?: string; players: number; max: number; names: string[] };

export function GameServerWidget({ data }: Props<GameServer>) {
  if (!data.online) {
    return (
      <div className="row">
        <Mark state="down" label="Hors ligne" />
        <div className="row-main">
          <span className="row-title">Serveur hors ligne</span>
          <span className="row-sub">Aucune réponse à la requête de statut.</span>
        </div>
      </div>
    );
  }
  return (
    <>
      <dl className="facts facts-2">
        <div>
          <dt>Joueurs</dt>
          <dd>
            <AnimatedNumber flash value={data.players} format={(n) => String(Math.round(n))} />/{data.max}
          </dd>
        </div>
        <div>
          <dt>Carte</dt>
          <dd className="facts-text">{data.map || 'n/d'}</dd>
        </div>
      </dl>
      <div className="row">
        <Mark state="ok" label="En ligne" />
        <div className="row-main">
          <span className="row-title">{data.name}</span>
          <span className="row-sub">{data.game}</span>
        </div>
      </div>
      {data.names.length > 0 && <p className="note">En jeu : {data.names.join(', ')}</p>}
    </>
  );
}
export const gameServerBadge = (d: GameServer) => (d.online ? 'en ligne' : 'hors ligne');

// --- GitHub releases and pull requests ---

type Releases = { failed: number; releases: { repo: string; tag: string; url: string; published: string }[] };

export function ReleasesWidget({ data }: Props<Releases>) {
  const fresh = (date: string) => Date.now() - new Date(date).getTime() < 7 * 86_400_000;
  return (
    <>
      <ul className="rows">
        {data.releases.map((r) => (
          <li key={r.repo} className="row">
            <img className="avatar avatar-square" src={`https://github.com/${encodeURIComponent(r.repo.split('/')[0])}.png?size=48`} alt="" width={24} height={24} loading="lazy" />
            <div className="row-main">
              <a className="row-title" {...ext(r.url)}>
                {r.repo.split('/')[1]}
              </a>
              <span className="row-sub">{r.repo.split('/')[0]}</span>
            </div>
            {fresh(r.published) && <span className="tag tag-ok">Nouveau</span>}
            <span className="row-aside">
              {r.tag}
              <small>{age(r.published)}</small>
            </span>
          </li>
        ))}
      </ul>
      {data.failed > 0 && <p className="note">{data.failed > 1 ? `${data.failed} dépôts sont illisibles ou sans release.` : 'Un dépôt est illisible ou sans release.'}</p>}
    </>
  );
}

type Pulls = { total: number; url: string; pulls: { number: number; title: string; author: string; url: string; created: string; draft: boolean; labels: string[] }[] };

export function PullsWidget({ data }: Props<Pulls>) {
  if (data.pulls.length === 0) return <Empty>Aucune pull request ouverte.</Empty>;
  return (
    <>
      <ul className="rows">
        {data.pulls.map((p) => (
          <li key={p.number} className="row row-top">
            <img className="avatar" src={`https://github.com/${encodeURIComponent(p.author)}.png?size=48`} alt="" width={24} height={24} loading="lazy" />
            <div className="row-main">
              <a className="row-title wrap" {...ext(p.url)}>
                {p.title}
              </a>
              <span className="row-sub">
                #{p.number}, {p.author}
                {p.draft ? ', brouillon' : ''}
              </span>
            </div>
            <span className="row-aside">{age(p.created)}</span>
          </li>
        ))}
      </ul>
      {data.total > data.pulls.length && (
        <a className="more" {...ext(data.url)}>
          Voir les {data.total} pull requests ouvertes
        </a>
      )}
    </>
  );
}
export const pullsBadge = (d: Pulls) => `${d.total} ouvertes`;

// --- Incidents ---

type Incident = { id: number; label: string; state: State; detail: string; opened: string; closed?: string; suspects?: unknown[] };
type Incidents = { open: Incident[]; recent: Incident[]; days: number; count: number; minutes: number };

function span(from: string, to?: string): string {
  const minutes = Math.max(1, Math.round(((to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime()) / 60_000));
  if (minutes < 60) return `${minutes} min`;
  if (minutes < 48 * 60) return `${Math.floor(minutes / 60)} h ${String(minutes % 60).padStart(2, '0')}`;
  return `${Math.round(minutes / 1440)} j`;
}

export function IncidentsWidget({ data, widgetId }: Props<Incidents>) {
  const detail = useDetail();
  const open = (it: Incident) =>
    detail ? () => detail({ title: it.label, subtitle: it.closed ? `Incident clos, ${span(it.opened, it.closed)}` : 'Incident en cours', load: () => api.detail<DetailData>(widgetId, { id: String(it.id) }) }) : undefined;
  const row = (it: Incident) => (
    <li key={it.id} className="row row-top">
      <Mark state={it.closed ? 'ok' : it.state} label={it.closed ? 'Clos' : 'En cours'} />
      <div className="row-main">
        <Open onOpen={open(it)}>{it.label}</Open>
        <span className="row-text">{it.detail}</span>
      </div>
      <span className="row-aside">
        {span(it.opened, it.closed)}
        <small>{it.closed ? ago(it.closed) : 'en cours'}</small>
      </span>
    </li>
  );
  return (
    <>
      <dl className="facts facts-2">
        <div>
          <dt>Incidents sur {data.days} j</dt>
          <dd>{data.count}</dd>
        </div>
        <div>
          <dt>Durée cumulée</dt>
          <dd>{data.minutes < 120 ? `${data.minutes} min` : `${Math.round(data.minutes / 60)} h`}</dd>
        </div>
      </dl>
      {data.open.length + data.recent.length === 0 ? (
        <Empty>Aucun incident relevé. Le journal se remplit tout seul quand un indicateur du bandeau se dégrade.</Empty>
      ) : (
        <ul className="rows">
          {data.open.map(row)}
          {data.recent.map(row)}
        </ul>
      )}
    </>
  );
}
export const incidentsBadge = (d: Incidents) => (d.open.length ? `${d.open.length} en cours` : 'aucun en cours');

// --- Deadlines ---

type Deadlines = { failed?: string[]; items: { title: string; kind: string; note?: string; date: string; days: number; state: State }[] };

export function DeadlinesWidget({ data, options }: Props<Deadlines>) {
  if (data.items.length === 0) return <Empty>Aucune échéance suivie. Ajoutez vos jetons et noms de domaine dans les réglages.</Empty>;
  return (
    <>
      <More items={data.items} limit={Number(options.limit) || 7}>
        {(shown) => (
          <ul className="rows">
            {shown.map((d) => (
              <li key={d.kind + d.title} className="row">
                <Mark state={d.state} label={d.state === 'ok' ? 'Lointaine' : d.state === 'warn' ? 'Proche' : 'Urgente'} />
                <div className="row-main">
                  <span className="row-title">{d.title}</span>
                  {d.note && <span className="row-sub">{d.note}</span>}
                </div>
                <span className="row-aside">
                  {d.days < 0 ? 'Dépassée' : d.days === 0 ? 'Aujourd’hui' : `${d.days} j`}
                  <small>{new Date(d.date).toLocaleDateString('fr-FR', { day: 'numeric', month: 'short', year: '2-digit' })}</small>
                </span>
              </li>
            ))}
          </ul>
        )}
      </More>
      {data.failed && data.failed.length > 0 && <p className="note">{data.failed[0]}</p>}
    </>
  );
}

// --- Version lag ---

type Versions = { current: number; items: { name: string; running: string; latest: string; lag: string; state: State; url?: string }[] };

export function VersionsWidget({ data }: Props<Versions>) {
  const flip = useFlip<HTMLUListElement>();
  return (
    <ul className="rows" ref={flip}>
      {data.items.map((v) => (
        <li key={v.name} data-flip={v.name} className="row">
          <Mark state={v.state === 'none' ? 'unknown' : v.state} label={v.lag} />
          <div className="row-main">
            {v.url ? (
              <a className="row-title" {...ext(v.url)}>
                {v.name}
              </a>
            ) : (
              <span className="row-title">{v.name}</span>
            )}
            <span className="row-sub">{v.lag}</span>
          </div>
          <span className="row-aside">
            {v.running || 'n/d'}
            {v.latest && v.state !== 'ok' && <small>dernière : {v.latest}</small>}
          </span>
        </li>
      ))}
    </ul>
  );
}
export const versionsBadge = (d: Versions) => `${d.current}/${d.items.length} à jour`;

// --- Saturation forecast ---

type Forecast = { hours: number; items: { name: string; used: number; free: number; days: number | null; state: State }[] };

export function ForecastWidget({ data }: Props<Forecast>) {
  return (
    <ul className="rows">
      {data.items.map((f) => (
        <li key={f.name} className={`row row-wrap stat-${f.state}`}>
          <Mark state={f.state} />
          <div className="row-main">
            <span className="row-title">{f.name}</span>
            <span className="row-sub">
              {formatStat(f.used, 'percent')} utilisés, {formatStat(f.free, 'bytes')} libres
            </span>
          </div>
          <span className="row-aside">
            {f.days === null ? 'Stable' : f.days < 1 ? 'Plein sous 24 h' : `Plein dans ${Math.round(f.days)} j`}
            <small>tendance sur {data.hours} h</small>
          </span>
          <span className="meter meter-row" aria-hidden>
            <i style={{ width: `${Math.min(100, f.used)}%` }} />
          </span>
        </li>
      ))}
    </ul>
  );
}

// --- Lab map ---

type Topology = { nodes: { id: string; label: string; layer: number; note?: string; state: State }[]; links: { from: string; to: string; state: State }[] };

export function TopologyWidget({ data }: Props<Topology>) {
  const layers = [...new Set(data.nodes.map((n) => n.layer))].sort((a, b) => a - b);
  const columns = layers.map((l) => data.nodes.filter((n) => n.layer === l));
  const tallest = Math.max(...columns.map((c) => c.length));
  const at = new Map<string, [number, number]>();
  columns.forEach((col, ci) => col.forEach((n, ni) => at.set(n.id, [((ci + 0.5) / columns.length) * 100, ((ni + 0.5) / col.length) * 100])));
  return (
    <div className="topology" style={{ height: `${Math.max(3, tallest) * 4.25}rem` }}>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden>
        {data.links.map((l) => {
          const [x1, y1] = at.get(l.from)!;
          const [x2, y2] = at.get(l.to)!;
          const mid = (x1 + x2) / 2;
          return <path key={`${l.from}>${l.to}`} d={`M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${x2} ${y2}`} className={`flow flow-${l.state}`} vectorEffect="non-scaling-stroke" />;
        })}
      </svg>
      {data.nodes.map((n) => {
        const [x, y] = at.get(n.id)!;
        return (
          <div key={n.id} className={`topo-node topo-${n.state}`} style={{ left: `${x}%`, top: `${y}%` }} title={n.note}>
            {n.state !== 'none' && <Mark state={n.state} />}
            <span>{n.label}</span>
          </div>
        );
      })}
      <p className="sr-only">
        {data.nodes.filter((n) => n.state === 'down' || n.state === 'warn').length === 0
          ? 'Tous les éléments suivis sont sains.'
          : `À traiter : ${data.nodes.filter((n) => n.state === 'down' || n.state === 'warn').map((n) => n.label).join(', ')}.`}
      </p>
    </div>
  );
}

// --- Top consumers ---

type Consumer = { namespace: string; pod: string; container?: string; value: number };
type Top = { cpu: Consumer[]; memory: Consumer[]; tight: Consumer[] | null };

function Bars({ title, items, format, max }: { title: string; items: Consumer[]; format: (v: number) => string; max?: number }) {
  const flip = useFlip<HTMLUListElement>();
  const top = max ?? Math.max(...items.map((i) => i.value), 0.0001);
  return (
    <div className="group">
      <h3>{title}</h3>
      <ul className="bars" ref={flip}>
        {items.map((c) => (
          <li key={`${c.namespace}/${c.pod}/${c.container ?? ''}`} data-flip={`${c.namespace}/${c.pod}/${c.container ?? ''}`}>
            <span className="bar-name" title={`${c.namespace}/${c.pod}`}>
              {c.pod}
            </span>
            <span className="bar-value">{format(c.value)}</span>
            <span className="bar-track" aria-hidden>
              <i style={{ width: `${Math.min(100, (c.value / top) * 100)}%` }} />
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function TopWidget({ data }: Props<Top>) {
  const cores = (v: number) => (v < 1 ? `${Math.round(v * 1000)} m` : `${v.toFixed(2).replace('.', ',')} cœurs`);
  return (
    <div className="top-grid">
      <Bars title="Processeur" items={data.cpu} format={cores} />
      <Bars title="Mémoire" items={data.memory} format={(v) => formatStat(v, 'bytes')} />
      {data.tight && data.tight.length > 0 && <Bars title="Proches de leur limite mémoire" items={data.tight} format={(v) => formatStat(v * 100, 'percent')} max={1} />}
    </div>
  );
}

// --- PostgreSQL ---

type Postgres = {
  metrics: boolean;
  clusters: { namespace: string; name: string; phase: string; ready: number; instances: number; primary: string; archiving: State | 'unknown'; storage?: string; size?: number; connections?: number; walAge?: number; lastBackup?: string; state: State }[];
};

export function PostgresWidget({ data }: Props<Postgres>) {
  return (
    <>
      {data.clusters.map((c) => (
        <div key={`${c.namespace}/${c.name}`} className="group">
          <div className="row">
            <Mark state={c.state} />
            <div className="row-main">
              <span className="row-title">{c.name}</span>
              <span className="row-sub">
                {c.namespace}, {c.phase || 'état inconnu'}
              </span>
            </div>
            <span className="row-aside">
              {c.ready}/{c.instances}
              <small>instances</small>
            </span>
          </div>
          <dl className="pairs">
            <div>
              <dt>Archivage continu</dt>
              <dd className={c.archiving === 'down' ? 'down' : undefined}>{c.archiving === 'ok' ? 'actif' : c.archiving === 'down' ? 'en échec' : 'inconnu'}</dd>
            </div>
            <div>
              <dt>Dernière sauvegarde</dt>
              <dd>{c.lastBackup ? ago(c.lastBackup) : 'aucune'}</dd>
            </div>
            <div>
              <dt>Taille</dt>
              <dd>{c.size !== undefined ? `${formatStat(c.size, 'bytes')}${c.storage ? ` sur ${c.storage}` : ''}` : c.storage ? `volume de ${c.storage}` : 'n/d'}</dd>
            </div>
            {c.connections !== undefined && (
              <div>
                <dt>Connexions</dt>
                <dd>{Math.round(c.connections)}</dd>
              </div>
            )}
            {c.walAge !== undefined && (
              <div>
                <dt>Dernier journal archivé</dt>
                <dd>il y a {formatStat(c.walAge, 'duration')}</dd>
              </div>
            )}
          </dl>
        </div>
      ))}
      {!data.metrics && <p className="note">Taille et connexions apparaîtront quand Prometheus collectera les métriques CloudNativePG.</p>}
    </>
  );
}

// --- Third-party status ---

type Upstream = { healthy: number; services: { name: string; state: State; detail: string; url: string; incidents: number }[] };

export function StatusWidget({ data }: Props<Upstream>) {
  const flip = useFlip<HTMLUListElement>();
  return (
    <ul className="rows" ref={flip}>
      {data.services.map((s) => (
        <li key={s.url} data-flip={s.url} className="row">
          <Mark state={s.state} />
          <div className="row-main">
            <a className="row-title" {...ext(s.url)}>
              {s.name || s.url}
            </a>
            <span className="row-sub">{s.detail}</span>
          </div>
          {s.incidents > 0 && <span className="tag tag-warn">{s.incidents > 1 ? `${s.incidents} incidents` : '1 incident'}</span>}
        </li>
      ))}
    </ul>
  );
}
export const statusBadge = (d: Upstream) => `${d.healthy}/${d.services.length} nominaux`;

// --- Daily digest ---

type Digest = { generated: string; lines: { state: State; text: string }[] };

export function DigestWidget({ data }: Props<Digest>) {
  return (
    <ul className="digest">
      {data.lines.map((l, i) => (
        <li key={i} className={`digest-${l.state}`}>
          {l.text}
        </li>
      ))}
    </ul>
  );
}

// --- Configuration drift ---

type Drift = { applications: number; branch: string; items: { kind: string; name: string; detail: string; state: State }[] };

export function DriftWidget({ data, options }: Props<Drift>) {
  if (data.items.length === 0) return <Empty>Rien ne vit hors de Git : les {data.applications} Applications suivent « {data.branch} » et chaque workload est géré par Argo CD.</Empty>;
  return (
    <More items={data.items} limit={Number(options.limit) || 8}>
      {(shown) => (
        <ul className="rows">
          {shown.map((d) => (
            <li key={d.kind + d.name} className="row">
              <Mark state={d.state} label={d.state === 'down' ? 'À corriger' : 'À vérifier'} />
              <div className="row-main">
                <span className="row-title wrap">{d.name}</span>
                <span className="row-text">
                  {d.kind}, {d.detail}
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </More>
  );
}
export const driftBadge = (d: Drift) => (d.items.length > 1 ? `${d.items.length} écarts` : d.items.length === 1 ? '1 écart' : 'aucun écart');
