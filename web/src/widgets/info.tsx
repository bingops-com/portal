import { useEffect, useRef, useState } from 'react';
import { Cloud, CloudDrizzle, CloudFog, CloudLightning, CloudMoon, CloudRain, CloudSnow, CloudSun, Droplets, MessageSquare, Moon, Search, Sun, Wind } from 'lucide-react';
import type { Options } from '../api';
import { AnimatedNumber, Empty, Sparkline, ext } from '../components/bits';
import { age, clockTime, compact, host, money } from '../format';

type Props<T> = { data: T; options: Options; widgetId?: string };

// --- RSS ---

type Feed = { items: { title: string; link: string; source: string; published?: string }[]; failed?: string[] };

export function RssWidget({ data }: Props<Feed>) {
  if (data.items.length === 0) return <Empty>Ces flux ne contiennent aucun article pour le moment.</Empty>;
  return (
    <>
      <ul className="rows">
        {data.items.map((it) => (
          <li key={it.link} className="row row-top">
            <div className="row-main">
              <a className="row-title wrap" {...ext(it.link)}>
                {it.title}
              </a>
              <span className="row-sub">{it.source}</span>
            </div>
            <span className="row-aside">{age(it.published)}</span>
          </li>
        ))}
      </ul>
      {data.failed && data.failed.length > 0 && <p className="note">{data.failed.length > 1 ? `${data.failed.length} flux sont injoignables.` : 'Un flux est injoignable.'}</p>}
    </>
  );
}

// --- Videos ---

type Videos = { videos: { title: string; link: string; channel: string; thumbnail: string; published?: string }[] };

export function VideosWidget({ data }: Props<Videos>) {
  if (data.videos.length === 0) return <Empty>Aucune vidéo récente sur ces chaînes.</Empty>;
  return (
    <ul className="videos">
      {data.videos.map((v) => (
        <li key={v.link}>
          <a {...ext(v.link)}>
            <img src={v.thumbnail} alt="" loading="lazy" width={320} height={180} />
            <span className="row-title wrap">{v.title}</span>
            <span className="video-meta">
              <span>{v.channel}</span>
              <span>{age(v.published)}</span>
            </span>
          </a>
        </li>
      ))}
    </ul>
  );
}

// --- Markets ---

type Markets = { currency: string; coins: { id: string; name: string; symbol: string; image: string; price: number; change24h: number; sparkline: number[] }[] };

export function MarketsWidget({ data }: Props<Markets>) {
  if (data.coins.length === 0) return <Empty>CoinGecko ne connaît aucun de ces identifiants.</Empty>;
  return (
    <ul className="rows">
      {data.coins.map((c) => {
        const up = c.change24h >= 0;
        return (
          <li key={c.id} className="row">
            <img className="coin" src={c.image} alt="" width={24} height={24} loading="lazy" />
            <div className="row-main">
              <a className="row-title" {...ext(`https://www.coingecko.com/fr/pi%C3%A8ces/${c.id}`)}>
                {c.name}
              </a>
              <span className="row-sub">{c.symbol}</span>
            </div>
            <Sparkline values={c.sparkline} className={up ? 'spark-up' : 'spark-down'} />
            <span className="row-aside">
              <AnimatedNumber value={c.price} format={(n) => money(n, data.currency)} />
              <small className={up ? 'up' : 'down'}>
                {up ? '+' : '−'}
                {Math.abs(c.change24h).toFixed(2).replace('.', ',')} %
              </small>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

// --- Weather ---

type Weather = {
  location: string;
  current: { temp: number; feels: number; humidity: number; code: number; wind: number; isDay: boolean };
  hourly: { time: string; temp: number; precip: number; code: number }[];
  daily: { date: string; code: number; max: number; min: number; precip: number }[];
};

function wmo(code: number, day = true): [typeof Sun, string] {
  if (code === 0) return [day ? Sun : Moon, 'Ciel dégagé'];
  if (code <= 2) return [day ? CloudSun : CloudMoon, 'Éclaircies'];
  if (code === 3) return [Cloud, 'Couvert'];
  if (code <= 48) return [CloudFog, 'Brouillard'];
  if (code <= 57) return [CloudDrizzle, 'Bruine'];
  if (code <= 67) return [CloudRain, 'Pluie'];
  if (code <= 77) return [CloudSnow, 'Neige'];
  if (code <= 82) return [CloudRain, 'Averses'];
  if (code <= 86) return [CloudSnow, 'Averses de neige'];
  return [CloudLightning, 'Orage'];
}

// The one decorative surface of the portal: the current conditions sit on a
// sky that matches them.
function sky(code: number, day: boolean): string {
  if (code >= 95) return 'storm';
  if ((code >= 71 && code <= 77) || code === 85 || code === 86) return 'snow';
  if (code >= 51) return 'rain';
  if (code >= 45) return 'fog';
  if (code === 3) return 'cloud';
  return day ? 'day' : 'night';
}

export function WeatherWidget({ data }: Props<Weather>) {
  const [Icon, label] = wmo(data.current.code, data.current.isDay);
  const hours = data.hourly.filter((_, i) => i % 3 === 0).slice(0, 6);
  return (
    <>
      <div className={`weather-now sky sky-${sky(data.current.code, data.current.isDay)}`}>
        <Icon size={44} strokeWidth={1.5} aria-hidden />
        <div>
          <span className="weather-temp">{Math.round(data.current.temp)}°</span>
          <span className="row-sub">
            {label}, ressenti {Math.round(data.current.feels)}°
          </span>
        </div>
        <ul className="weather-extra">
          <li title="Vent">
            <Wind size={14} aria-hidden /> {Math.round(data.current.wind)} km/h
          </li>
          <li title="Humidité">
            <Droplets size={14} aria-hidden /> {Math.round(data.current.humidity)} %
          </li>
        </ul>
      </div>
      <ol className="weather-hours">
        {hours.map((h) => {
          const [HIcon, hLabel] = wmo(h.code, Number(h.time.slice(11, 13)) >= 7 && Number(h.time.slice(11, 13)) < 20);
          return (
            <li key={h.time} title={hLabel}>
              <span>{h.time.slice(11, 13)} h</span>
              <HIcon size={18} strokeWidth={1.75} aria-hidden />
              <strong>{Math.round(h.temp)}°</strong>
            </li>
          );
        })}
      </ol>
      <ul className="rows">
        {data.daily.slice(1, 5).map((d) => {
          const [DIcon, dLabel] = wmo(d.code);
          return (
            <li key={d.date} className="row">
              <DIcon size={18} strokeWidth={1.75} aria-hidden />
              <div className="row-main">
                <span className="row-title capitalize">{new Date(d.date + 'T12:00').toLocaleDateString('fr-FR', { weekday: 'long' })}</span>
                <span className="row-sub">
                  {dLabel}
                  {d.precip >= 20 && `, pluie ${Math.round(d.precip)} %`}
                </span>
              </div>
              <span className="row-aside">
                {Math.round(d.max)}°<small>{Math.round(d.min)}°</small>
              </span>
            </li>
          );
        })}
      </ul>
    </>
  );
}
export const weatherBadge = (d: Weather) => d.location;

// --- Calendar ---

type Calendar = { events: { title: string; start: string; end: string; allDay: boolean; location?: string; calendar?: string }[]; failed?: string[] };

const dayKey = (d: Date) => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
// All-day events are dates, not instants: read them in UTC so they do not
// slide to the neighbouring day in the viewer's timezone.
const eventDay = (e: Calendar['events'][number]) => {
  const d = new Date(e.start);
  return e.allDay ? new Date(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()) : d;
};

export function CalendarWidget({ data }: Props<Calendar>) {
  const today = new Date();
  const first = new Date(today.getFullYear(), today.getMonth(), 1);
  const lead = (first.getDay() + 6) % 7;
  const count = new Date(today.getFullYear(), today.getMonth() + 1, 0).getDate();
  const busy = new Set(data.events.map((e) => dayKey(eventDay(e))));
  const startOfToday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  const upcoming = data.events.filter((e) => (e.allDay ? eventDay(e) >= startOfToday : new Date(e.end) >= today)).slice(0, 6);
  const cells: (number | null)[] = [...Array(lead).fill(null), ...Array.from({ length: count }, (_, i) => i + 1)];

  return (
    <>
      <p className="month capitalize">{today.toLocaleDateString('fr-FR', { month: 'long', year: 'numeric' })}</p>
      <div className="cal" role="grid" aria-label="Mois en cours">
        {['L', 'M', 'M', 'J', 'V', 'S', 'D'].map((d, i) => (
          <span key={i} className="cal-head" aria-hidden>
            {d}
          </span>
        ))}
        {cells.map((day, i) => {
          if (day === null) return <span key={i} />;
          const date = new Date(today.getFullYear(), today.getMonth(), day);
          const isToday = day === today.getDate();
          return (
            <span key={i} className={`cal-day${isToday ? ' cal-today' : ''}${busy.has(dayKey(date)) ? ' cal-busy' : ''}`} aria-current={isToday ? 'date' : undefined}>
              {day}
            </span>
          );
        })}
      </div>
      {upcoming.length > 0 ? (
        <ul className="rows">
          {upcoming.map((e, i) => {
            const d = eventDay(e);
            return (
              <li key={i} className="row row-top">
                <span className="cal-date">
                  <strong>{d.getDate()}</strong>
                  {d.toLocaleDateString('fr-FR', { month: 'short' })}
                </span>
                <div className="row-main">
                  <span className="row-title wrap">{e.title || 'Sans titre'}</span>
                  <span className="row-sub">
                    {e.allDay ? 'Toute la journée' : clockTime(e.start)}
                    {e.location ? `, ${e.location}` : ''}
                  </span>
                </div>
              </li>
            );
          })}
        </ul>
      ) : (
        <Empty>Aucun événement à venir. Ajoutez une adresse iCal dans les réglages pour voir votre agenda.</Empty>
      )}
      {data.failed && data.failed.length > 0 && <p className="note">Un calendrier est injoignable.</p>}
    </>
  );
}

// --- Hacker News / Reddit ---

type Posts = { scores: boolean; posts: { title: string; url: string; commentsUrl: string; points: number; comments: number; created: string; source?: string }[] };

export function PostsWidget({ data }: Props<Posts>) {
  if (data.posts.length === 0) return <Empty>Aucune publication à afficher.</Empty>;
  return (
    <ul className="rows">
      {data.posts.map((p) => (
        <li key={p.commentsUrl} className="row row-top">
          {data.scores && (
            <span className="points" title="Points">
              {compact(p.points)}
            </span>
          )}
          <div className="row-main">
            <a className="row-title wrap" {...ext(p.url)}>
              {p.title}
            </a>
            <span className="row-sub">
              {p.source ?? host(p.url)}
              {data.scores && (
                <a className="comments" {...ext(p.commentsUrl)} aria-label={`${p.comments} commentaires`}>
                  <MessageSquare size={12} aria-hidden /> {compact(p.comments)}
                </a>
              )}
            </span>
          </div>
          <span className="row-aside">{age(p.created)}</span>
        </li>
      ))}
    </ul>
  );
}

// --- Search (browser only) ---

const engines: Record<string, string> = {
  duckduckgo: 'https://duckduckgo.com/?q={q}',
  google: 'https://www.google.com/search?q={q}',
  kagi: 'https://kagi.com/search?q={q}',
  startpage: 'https://www.startpage.com/do/search?q={q}',
  brave: 'https://search.brave.com/search?q={q}',
};

export function SearchWidget({ options }: { options: Options }) {
  const input = useRef<HTMLInputElement>(null);
  const bangs: { prefix: string; url: string }[] = Array.isArray(options.bangs) ? options.bangs : [];
  const engine = engines[options.engine] ?? (typeof options.engine === 'string' && options.engine.includes('{q}') ? options.engine : engines.duckduckgo);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      if (e.key === '/' && !e.ctrlKey && !e.metaKey && !/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName) && !target.isContentEditable) {
        e.preventDefault();
        input.current?.focus();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const text = input.current?.value.trim() ?? '';
    if (!text) return;
    const [first, ...rest] = text.split(/\s+/);
    const bang = bangs.find((b) => b.prefix === first);
    const template = bang && rest.length ? bang.url : engine;
    const query = bang && rest.length ? rest.join(' ') : text;
    if (!/^https?:\/\//.test(template)) return;
    window.open(template.replace('{q}', encodeURIComponent(query)), '_blank', 'noreferrer');
    if (input.current) input.current.value = '';
  };

  return (
    <form className="search" onSubmit={submit} role="search">
      <Search size={18} aria-hidden />
      <input ref={input} type="search" aria-label="Rechercher sur le web" placeholder="Rechercher sur le web" autoComplete="off" />
      <kbd title="Appuyez sur / pour rechercher">/</kbd>
      {bangs.length > 0 && <p className="search-bangs">Raccourcis : {bangs.map((b) => b.prefix).join(', ')}</p>}
    </form>
  );
}

// --- Clock (browser only) ---

export function ClockWidget({ options }: { options: Options }) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), 15_000);
    return () => clearInterval(timer);
  }, []);
  const zones: { label: string; timezone: string }[] = Array.isArray(options.zones) ? options.zones : [];
  const safe = (tz: string) => {
    try {
      return clockTime(now, tz);
    } catch {
      return 'fuseau inconnu';
    }
  };
  return (
    <>
      <div className="clock">
        <time className="clock-time">{clockTime(now)}</time>
        <span className="row-sub capitalize">{now.toLocaleDateString('fr-FR', { weekday: 'long', day: 'numeric', month: 'long' })}</span>
      </div>
      {zones.length > 0 && (
        <ul className="rows">
          {zones.map((z) => (
            <li key={z.label + z.timezone} className="row">
              <div className="row-main">
                <span className="row-title">{z.label}</span>
              </div>
              <span className="row-aside">{safe(z.timezone)}</span>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
