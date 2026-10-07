const units: [number, string][] = [
  [60, 's'],
  [60, 'min'],
  [24, 'h'],
  [30, 'j'],
  [12, 'mois'],
  [Infinity, 'an'],
];

// "5 min", "3 h", "2 j": compact age of a past date.
export function age(date?: string | null): string {
  if (!date) return '';
  let value = Math.max(0, (Date.now() - new Date(date).getTime()) / 1000);
  for (const [size, unit] of units) {
    if (value < size) {
      const n = Math.floor(value);
      if (unit === 's') return 'à l’instant';
      return `${n} ${unit === 'an' && n > 1 ? 'ans' : unit}`;
    }
    value /= size;
  }
  return '';
}

export const ago = (date?: string | null) => {
  const a = age(date);
  return !a || a === 'à l’instant' ? a : `il y a ${a}`;
};

export const clockTime = (date: string | Date, timeZone?: string) =>
  new Intl.DateTimeFormat('fr-FR', { hour: '2-digit', minute: '2-digit', timeZone }).format(new Date(date));

const nf = (digits: number) => new Intl.NumberFormat('fr-FR', { maximumFractionDigits: digits, minimumFractionDigits: 0 });

export function formatStat(value: number | null | undefined, format?: string): string {
  if (value === null || value === undefined) return 'n/d';
  switch (format) {
    case 'percent':
      return `${nf(value < 10 ? 1 : 0).format(value)} %`;
    case 'bytes': {
      const names = ['o', 'Kio', 'Mio', 'Gio', 'Tio'];
      let v = value;
      let i = 0;
      while (v >= 1024 && i < names.length - 1) {
        v /= 1024;
        i++;
      }
      return `${nf(v < 10 ? 1 : 0).format(v)} ${names[i]}`;
    }
    case 'duration': {
      if (value < 60) return `${nf(0).format(value)} s`;
      if (value < 3600) return `${nf(0).format(value / 60)} min`;
      if (value < 86400) return `${nf(1).format(value / 3600)} h`;
      return `${nf(1).format(value / 86400)} j`;
    }
    default:
      return nf(Math.abs(value) < 10 ? 2 : 0).format(value);
  }
}

export function money(value: number, currency: string): string {
  const digits = value >= 100 ? 0 : value >= 1 ? 2 : 4;
  try {
    return new Intl.NumberFormat('fr-FR', { style: 'currency', currency: currency.toUpperCase(), maximumFractionDigits: digits }).format(value);
  } catch {
    return `${nf(digits).format(value)} ${currency.toUpperCase()}`;
  }
}

export const compact = (n: number) => new Intl.NumberFormat('fr-FR', { notation: 'compact', maximumFractionDigits: 1 }).format(n);

export function host(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '');
  } catch {
    return '';
  }
}

export function newId(type: string): string {
  const rand = Math.random().toString(36).slice(2, 8);
  return `${type}-${rand}`;
}
