import { useState } from 'react';
import type { Options } from '../api';
import { Empty, Mark, Sparkline, ext, type State } from '../components/bits';
import { age, formatStat } from '../format';

type Props<T> = { data: T; options: Options };

function More<T>({ items, limit, children }: { items: T[]; limit: number; children: (shown: T[]) => React.ReactNode }) {
  const [open, setOpen] = useState(false);
  const shown = open ? items : items.slice(0, limit);
  return (
    <>
      {children(shown)}
      {items.length > limit && (
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
          <dd>{data.pods.Running ?? 0}</dd>
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

export function WorkloadsWidget({ data, options }: Props<Workloads>) {
  if (data.workloads.length === 0) return <Empty>Aucun workload dans les namespaces sélectionnés.</Empty>;
  return (
    <More items={data.workloads} limit={Number(options.limit) || 10}>
      {(shown) => (
        <ul className="rows">
          {shown.map((w) => (
            <li key={`${w.kind}/${w.namespace}/${w.name}`} className="row">
              <Mark state={w.healthy ? 'ok' : w.ready > 0 ? 'warn' : 'down'} label={w.healthy ? 'Prêt' : 'Incomplet'} />
              <div className="row-main">
                <span className="row-title">{w.name}</span>
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
  if (data.events.length === 0) return <Empty>Aucun avertissement récent dans le cluster.</Empty>;
  return (
    <ul className="rows">
      {data.events.map((e, i) => (
        <li key={i} className="row row-top">
          <div className="row-main">
            <span className="row-title">
              {e.reason}
              {e.count > 1 && <span className="count">×{e.count}</span>}
            </span>
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

export function ArgoWidget({ data, options }: Props<Argo>) {
  if (data.apps.length === 0) return <Empty>Aucune Application Argo CD visible avec ce compte de service.</Empty>;
  return (
    <More items={data.apps} limit={Number(options.limit) || 12}>
      {(shown) => (
        <ul className="rows">
          {shown.map((a) => {
            const state = a.ignored ? 'none' : argoState(a);
            const label = a.sync !== 'Synced' ? a.sync : a.health === 'Healthy' ? 'Synced' : a.health;
            return (
              <li key={a.name} className="row">
                <Mark state={state} />
                <div className="row-main">
                  {a.url ? (
                    <a className="row-title" {...ext(a.url)}>
                      {a.name}
                    </a>
                  ) : (
                    <span className="row-title">{a.name}</span>
                  )}
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
  endpoints: { name: string; group: string; up: boolean; uptime: number; ms: number; url?: string; results: { ok: boolean; ms: number; t: string }[] }[];
};

export function GatusWidget({ data }: Props<Gatus>) {
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
                  {e.url ? (
                    <a className="row-title" {...ext(e.url)}>
                      {e.name}
                    </a>
                  ) : (
                    <span className="row-title">{e.name}</span>
                  )}
                </div>
                <span className="row-aside">
                  {formatStat(e.uptime, 'percent')}
                  <small>{Math.round(e.ms)} ms</small>
                </span>
                <span className="ticks" aria-hidden>
                  {e.results.map((r, i) => (
                    <i key={i} className={r.ok ? 'tick-ok' : 'tick-down'} title={`${new Date(r.t).toLocaleString('fr-FR')}, ${Math.round(r.ms)} ms`} />
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

export function PrometheusWidget({ data }: Props<Prom>) {
  return (
    <div className="stats">
      {data.stats.map((s) => (
        <div key={s.label} className={`stat stat-${s.state}`}>
          <span className="stat-label">{s.label}</span>
          <span className="stat-value">{s.error ? 'n/d' : formatStat(s.value, s.format)}</span>
          {s.error ? <span className="stat-note">{s.error}</span> : <Sparkline values={s.series} />}
          {s.format === 'percent' && s.value !== null && (
            <span className="meter" aria-hidden>
              <i style={{ width: `${Math.min(100, Math.max(0, s.value))}%` }} />
            </span>
          )}
        </div>
      ))}
    </div>
  );
}

// --- Alerts ---

type Alerts = { alerts: { name: string; severity: string; namespace?: string; summary?: string; since: string }[] };

export function AlertsWidget({ data }: Props<Alerts>) {
  if (data.alerts.length === 0) return <Empty>Aucune alerte active.</Empty>;
  return (
    <More items={data.alerts} limit={6}>
      {(shown) => (
        <ul className="rows">
          {shown.map((a, i) => (
            <li key={i} className="row row-top">
              <Mark state={a.severity === 'critical' ? 'down' : 'warn'} label={a.severity || 'alerte'} />
              <div className="row-main">
                <span className="row-title">{a.name}</span>
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

type Bookmarks = { groups: { title: string; links: { title: string; url: string; description?: string; status?: 'up' | 'down'; ms?: number }[] }[] };

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
                  <span className="monogram" aria-hidden>
                    {l.title.slice(0, 1).toUpperCase()}
                  </span>
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

export function CertificatesWidget({ data, options }: Props<Certificates>) {
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
                  <span className="row-title">{c.host || c.name}</span>
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

export function BackupsWidget({ data }: Props<Backups>) {
  return (
    <ul className="rows">
      {data.backups.map((b) => (
        <li key={`${b.kind}/${b.namespace}/${b.name}`} className="row row-top">
          <Mark state={b.state} label={b.state === 'ok' ? 'À jour' : b.state === 'unknown' ? 'En attente' : 'En retard'} />
          <div className="row-main">
            <span className="row-title">{b.name}</span>
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
