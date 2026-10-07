import { createContext, useEffect, useLayoutEffect, useRef, useState } from 'react';
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

// The line draws itself in, and a dot marks the current value.
export function Sparkline({ values, className }: { values?: number[]; className?: string }) {
  if (!values || values.length < 2) return <span className={`spark ${className ?? ''}`} />;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const y = (v: number) => 26 - ((v - min) / span) * 24;
  const points = values.map((v, i) => `${(i / (values.length - 1)) * 100},${y(v)}`);
  return (
    <span className={`spark ${className ?? ''}`} aria-hidden>
      <svg viewBox="0 0 100 28" preserveAspectRatio="none">
        <polyline points={points.join(' ')} fill="none" strokeWidth="1.75" vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
      </svg>
      <i className="spark-dot" style={{ top: `${(y(values[values.length - 1]) / 28) * 100}%` }} />
    </span>
  );
}

// Ring gauge for a percentage; the arc eases to its new value.
export function Gauge({ value, state, children }: { value: number; state: State; children: React.ReactNode }) {
  const r = 26;
  const c = 2 * Math.PI * r;
  const v = Math.min(100, Math.max(0, value));
  return (
    <span className={`gauge gauge-${state}`}>
      <svg viewBox="0 0 64 64" aria-hidden>
        <circle cx="32" cy="32" r={r} className="gauge-track" />
        <circle cx="32" cy="32" r={r} className="gauge-arc" strokeDasharray={c} strokeDashoffset={c * (1 - v / 100)} transform="rotate(-90 32 32)" />
      </svg>
      <span className="gauge-value">{children}</span>
    </span>
  );
}

const still = () => typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// Rows marked with data-flip slide to their new place when the list is
// reordered, and fade in when they are new. Attach the ref to the list.
export function useFlip<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const seen = useRef<Map<string, number> | null>(null);
  useLayoutEffect(() => {
    const root = ref.current;
    if (!root) return;
    const next = new Map<string, number>();
    const items = root.querySelectorAll<HTMLElement>(':scope > [data-flip]');
    items.forEach((el) => next.set(el.dataset.flip!, el.offsetTop));
    const prev = seen.current;
    seen.current = next;
    if (!prev || still()) return;
    items.forEach((el) => {
      const before = prev.get(el.dataset.flip!);
      const now = next.get(el.dataset.flip!)!;
      if (before === undefined) {
        el.animate([{ opacity: 0, transform: 'translateY(-6px)' }, { opacity: 1, transform: 'none' }], { duration: 320, easing: 'ease-out' });
      } else if (Math.abs(before - now) > 2) {
        el.animate([{ transform: `translateY(${before - now}px)` }, { transform: 'none' }], { duration: 420, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)' });
      }
    });
  });
  return ref;
}

// True inside a widget opened in large: lists then show every row.
export const ExpandedContext = createContext(false);

export function Empty({ children }: { children: React.ReactNode }) {
  return <p className="empty">{children}</p>;
}

// Attributes for a link that leaves the portal.
export function ext(url?: string) {
  return url ? { href: url, target: '_blank', rel: 'noreferrer' } : {};
}

// Glides from the previous value to the new one, so a refresh reads as a
// change rather than a jump. With `flash`, the figure also blinks green or red
// in the direction of the change. Respects the reduced-motion preference.
export function AnimatedNumber({ value, format, flash }: { value: number; format: (n: number) => string; flash?: boolean }) {
  const [shown, setShown] = useState(value);
  const [dir, setDir] = useState('');
  const from = useRef(value);
  useEffect(() => {
    const start = from.current;
    from.current = value;
    if (start === value) return;
    let clear = 0;
    if (flash) {
      setDir(value > start ? 'tick-up' : 'tick-down');
      clear = window.setTimeout(() => setDir(''), 1100);
    }
    if (still()) {
      setShown(value);
      return () => clearTimeout(clear);
    }
    const t0 = performance.now();
    let frame = 0;
    const tick = (now: number) => {
      const k = Math.min(1, (now - t0) / 700);
      setShown(start + (value - start) * (1 - Math.pow(1 - k, 3)));
      if (k < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(clear);
    };
  }, [value, flash]);
  return <span className={dir || undefined}>{format(shown)}</span>;
}
