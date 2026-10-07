import { AlertTriangle, Check, HelpCircle, X } from 'lucide-react';

export type State = 'ok' | 'warn' | 'down' | 'unknown' | 'none';

const labels: Record<State, string> = { ok: 'Sain', warn: 'À surveiller', down: 'En panne', unknown: 'Inconnu', none: '' };

// Status marker: colour plus a distinct glyph, so state never relies on hue alone.
export function Mark({ state, label }: { state: State; label?: string }) {
  const Icon = state === 'ok' ? Check : state === 'warn' ? AlertTriangle : state === 'down' ? X : HelpCircle;
  return (
    <span className={`mark mark-${state}`} role="img" aria-label={label ?? labels[state]} title={label ?? labels[state]}>
      <Icon size={11} strokeWidth={3} aria-hidden />
    </span>
  );
}

export function Sparkline({ values, className }: { values?: number[]; className?: string }) {
  if (!values || values.length < 2) return <span className={`spark ${className ?? ''}`} />;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const points = values.map((v, i) => `${(i / (values.length - 1)) * 100},${26 - ((v - min) / span) * 24}`);
  return (
    <svg className={`spark ${className ?? ''}`} viewBox="0 0 100 28" preserveAspectRatio="none" aria-hidden>
      <polyline points={points.join(' ')} fill="none" strokeWidth="1.75" vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

export function Empty({ children }: { children: React.ReactNode }) {
  return <p className="empty">{children}</p>;
}

// Attributes for a link that leaves the portal.
export function ext(url?: string) {
  return url ? { href: url, target: '_blank', rel: 'noreferrer' } : {};
}
