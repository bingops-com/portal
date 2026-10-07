import { createContext, useContext, useEffect, useRef, useState } from 'react';
import { ExternalLink, X } from 'lucide-react';
import { ago, formatStat } from '../format';
import { Mark, type State } from './bits';

export type DetailRow = { state?: State; title: string; sub?: string; aside?: string; text?: string; time?: string };
export type DetailData = {
  facts?: { label: string; value: string }[];
  sections?: { title: string; empty?: string; rows: DetailRow[] }[];
  chart?: { label: string; format: string; points: [number, number][] };
  url?: string;
};
// What a widget row asks the panel to show. `load` receives the chosen time
// range when `ranges` is set.
export type DetailRequest = { title: string; subtitle?: string; ranges?: boolean; linkLabel?: string; load: (range: string) => Promise<DetailData> };

const DetailContext = createContext<((req: DetailRequest) => void) | null>(null);
export const DetailProvider = DetailContext.Provider;
// Null while the layout is being edited: rows are then not clickable.
export const useDetail = () => useContext(DetailContext);

const ranges: [string, string][] = [['3h', '3 heures'], ['24h', '24 heures'], ['7d', '7 jours']];

function Chart({ chart }: { chart: NonNullable<DetailData['chart']> }) {
  const [hover, setHover] = useState<number | null>(null);
  const pts = chart.points;
  if (pts.length < 2) return <p className="empty">Prometheus n’a pas de données sur cette période.</p>;
  const W = 600;
  const H = 200;
  const t0 = pts[0][0];
  const t1 = pts[pts.length - 1][0];
  const values = pts.map((p) => p[1]);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const pad = (max - min || Math.abs(max) || 1) * 0.08;
  const lo = min - pad;
  const hi = max + pad;
  const x = (t: number) => ((t - t0) / (t1 - t0 || 1)) * W;
  const y = (v: number) => H - ((v - lo) / (hi - lo)) * H;
  const line = pts.map((p) => `${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`).join(' ');
  const when = (t: number) => new Date(t * 1000).toLocaleString('fr-FR', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    const t = t0 + ((e.clientX - box.left) / box.width) * (t1 - t0);
    let best = 0;
    for (let i = 1; i < pts.length; i++) if (Math.abs(pts[i][0] - t) < Math.abs(pts[best][0] - t)) best = i;
    setHover(best);
  };
  const shown = hover === null ? pts[pts.length - 1] : pts[hover];
  return (
    <figure className="chart">
      <figcaption>
        <strong>{formatStat(shown[1], chart.format)}</strong>
        <span>{hover === null ? 'dernière valeur' : when(shown[0])}</span>
      </figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" onMouseMove={onMove} onMouseLeave={() => setHover(null)} role="img" aria-label={`Évolution de ${chart.label}`}>
        <polygon points={`0,${H} ${line} ${W},${H}`} className="chart-area" />
        <polyline points={line} className="chart-line" vectorEffect="non-scaling-stroke" />
        {hover !== null && <line x1={x(shown[0])} x2={x(shown[0])} y1={0} y2={H} className="chart-cursor" vectorEffect="non-scaling-stroke" />}
      </svg>
      <div className="chart-axis">
        <span>{when(t0)}</span>
        <span>
          min {formatStat(min, chart.format)}, max {formatStat(max, chart.format)}
        </span>
        <span>{when(t1)}</span>
      </div>
    </figure>
  );
}

export function DetailDrawer({ request, onClose }: { request: DetailRequest; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [range, setRange] = useState('24h');
  const [data, setData] = useState<DetailData | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (dialog.current && !dialog.current.open) dialog.current.showModal();
  }, []);
  useEffect(() => {
    let live = true;
    setError('');
    request
      .load(range)
      .then((d) => live && setData(d))
      .catch((err: Error) => live && setError(err.message));
    return () => {
      live = false;
    };
  }, [request, range]);

  return (
    <dialog
      ref={dialog}
      className="drawer"
      onClose={onClose}
      onClick={(e) => {
        if (e.target === dialog.current) onClose();
      }}
    >
      <header>
        <div>
          <h2>{request.title}</h2>
          {request.subtitle && <p>{request.subtitle}</p>}
        </div>
        <button className="icon-btn" onClick={onClose} aria-label="Fermer le détail">
          <X size={18} aria-hidden />
        </button>
      </header>
      <div className="drawer-body">
        {request.ranges && (
          <div className="segmented" role="group" aria-label="Période">
            {ranges.map(([value, label]) => (
              <button key={value} aria-pressed={range === value} onClick={() => setRange(value)}>
                {label}
              </button>
            ))}
          </div>
        )}
        {error && <p className="problem">Détail indisponible : {error}</p>}
        {!data && !error && (
          <div className="skeleton" aria-label="Chargement">
            <i />
            <i />
            <i />
          </div>
        )}
        {data?.chart && <Chart chart={data.chart} />}
        {data?.facts && (
          <dl className="drawer-facts">
            {data.facts
              .filter((f) => f.value)
              .map((f) => (
                <div key={f.label}>
                  <dt>{f.label}</dt>
                  <dd>{f.value}</dd>
                </div>
              ))}
          </dl>
        )}
        {data?.sections?.map((s) => (
          <section key={s.title}>
            <h3>{s.title}</h3>
            {s.rows.length === 0 ? (
              <p className="empty">{s.empty ?? 'Rien à afficher.'}</p>
            ) : (
              <ul className="rows">
                {s.rows.map((r, i) => (
                  <li key={i} className="row row-top">
                    {r.state && <Mark state={r.state} />}
                    <div className="row-main">
                      <span className="row-title wrap">{r.title}</span>
                      {r.sub && <span className="row-sub">{r.sub}</span>}
                      {r.text && <span className="row-text row-text-full">{r.text}</span>}
                    </div>
                    <span className="row-aside">
                      {r.aside}
                      {r.time && <small>{ago(r.time)}</small>}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        ))}
        {data?.url && (
          <a className="btn" href={data.url} target="_blank" rel="noreferrer">
            <ExternalLink size={15} aria-hidden /> {request.linkLabel ?? 'Ouvrir'}
          </a>
        )}
      </div>
    </dialog>
  );
}
