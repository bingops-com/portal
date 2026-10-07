import { useEffect, useRef, useState } from 'react';
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

// Glides from the previous value to the new one, so a refresh reads as a
// change rather than a jump. Respects the reduced-motion preference.
export function AnimatedNumber({ value, format }: { value: number; format: (n: number) => string }) {
  const [shown, setShown] = useState(value);
  const from = useRef(value);
  useEffect(() => {
    const start = from.current;
    from.current = value;
    if (start === value || window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      setShown(value);
      return;
    }
    const t0 = performance.now();
    let frame = 0;
    const tick = (now: number) => {
      const k = Math.min(1, (now - t0) / 700);
      setShown(start + (value - start) * (1 - Math.pow(1 - k, 3)));
      if (k < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [value]);
  return <>{format(shown)}</>;
}
