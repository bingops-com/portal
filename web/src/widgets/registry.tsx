import type { ComponentType } from 'react';
import type { Options } from '../api';
import { AlertsWidget, ArgoWidget, BackupsWidget, BookmarksWidget, CertificatesWidget, ClusterWidget, EventsWidget, GatusWidget, PrometheusWidget, WorkloadsWidget, alertsBadge, argoBadge, backupsBadge, certificatesBadge, clusterBadge, gatusBadge, workloadsBadge } from './ops';
import { CalendarWidget, ClockWidget, MarketsWidget, PostsWidget, RssWidget, SearchWidget, VideosWidget, WeatherWidget, weatherBadge } from './info';

export type Field = {
  key: string;
  label: string;
  kind: 'text' | 'number' | 'bool' | 'select' | 'lines' | 'yaml';
  help?: string;
  placeholder?: string;
  choices?: [string, string][];
};

export type WidgetMeta = {
  type: string;
  label: string;
  description: string;
  group: 'Ops' | 'Informations' | 'Outils';
  // Seconds between refreshes; 0 means the widget needs no server data.
  refresh: number;
  defaults: Options;
  fields: Field[];
  component: ComponentType<{ data: any; options: Options }>;
  badge?: (data: any) => string;
};

const context: Field = { key: 'context', label: 'Contexte kubeconfig', kind: 'text', help: 'Laissez vide pour le cluster où tourne le portail.' };
const limit = (help = 'Nombre de lignes affichées avant « Afficher les autres ».'): Field => ({ key: 'limit', label: 'Lignes affichées', kind: 'number', help });
const promUrl: Field = { key: 'url', label: 'Adresse de Prometheus', kind: 'text', placeholder: 'http://prometheus.monitoring.svc:9090' };

const list: WidgetMeta[] = [
  {
    type: 'kubernetes', label: 'Cluster', group: 'Ops', refresh: 30,
    description: 'Nœuds, pods et pods en difficulté d’un cluster Kubernetes.',
    defaults: {}, fields: [context], component: ClusterWidget, badge: clusterBadge,
  },
  {
    type: 'workloads', label: 'Workloads', group: 'Ops', refresh: 30,
    description: 'Deployments, StatefulSets et DaemonSets, les incomplets en premier.',
    defaults: { exclude: ['kube-system'], limit: 10 },
    fields: [
      context,
      { key: 'namespaces', label: 'Namespaces à afficher', kind: 'lines', help: 'Un par ligne. Vide : tous.' },
      { key: 'exclude', label: 'Namespaces à masquer', kind: 'lines' },
      limit(),
    ],
    component: WorkloadsWidget, badge: workloadsBadge,
  },
  {
    type: 'events', label: 'Événements', group: 'Ops', refresh: 30,
    description: 'Derniers avertissements émis par le cluster.',
    defaults: { limit: 6 },
    fields: [context, { key: 'namespaces', label: 'Namespaces à afficher', kind: 'lines', help: 'Un par ligne. Vide : tous.' }, limit('Nombre d’événements affichés.')],
    component: EventsWidget,
  },
  {
    type: 'argocd', label: 'Argo CD', group: 'Ops', refresh: 30,
    description: 'Synchronisation et santé des Applications, lues via l’API Kubernetes.',
    defaults: { url: '' },
    fields: [
      { key: 'url', label: 'Adresse de l’interface Argo CD', kind: 'text', placeholder: 'https://argocd.example.com', help: 'Sert uniquement aux liens vers chaque application.' },
      { key: 'namespace', label: 'Namespace des Applications', kind: 'text', help: 'Vide : tous les namespaces.' },
      { key: 'ignore', label: 'Applications à ignorer', kind: 'lines', help: 'Un nom par ligne. Elles restent affichées mais ne comptent plus dans le bandeau.' },
      context,
      limit(),
    ],
    component: ArgoWidget, badge: argoBadge,
  },
  {
    type: 'gatus', label: 'Disponibilité (Gatus)', group: 'Ops', refresh: 30,
    description: 'État, disponibilité et temps de réponse des endpoints surveillés par Gatus.',
    defaults: { url: '' },
    fields: [
      { key: 'url', label: 'Adresse de Gatus', kind: 'text', placeholder: 'http://gatus.gatus.svc:8080' },
      { key: 'publicUrl', label: 'Adresse publique de Gatus', kind: 'text', help: 'Facultatif : ajoute un lien vers le détail de chaque endpoint.' },
    ],
    component: GatusWidget, badge: gatusBadge,
  },
  {
    type: 'prometheus', label: 'Métriques', group: 'Ops', refresh: 30,
    description: 'Valeurs PromQL avec seuils et tendance sur trois heures.',
    defaults: {
      url: '',
      stats: [{ label: 'CPU', query: '100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)', format: 'percent', warn: 70, danger: 90 }],
    },
    fields: [
      promUrl,
      { key: 'stats', label: 'Requêtes', kind: 'yaml', help: 'Liste de { label, query, format, warn, danger }. Formats : percent, number, bytes, duration.' },
    ],
    component: PrometheusWidget,
  },
  {
    type: 'alerts', label: 'Alertes', group: 'Ops', refresh: 30,
    description: 'Alertes Prometheus en cours, les critiques en premier.',
    defaults: { url: '' },
    fields: [promUrl, { key: 'ignore', label: 'Alertes à ignorer', kind: 'lines', help: 'Un nom par ligne. Watchdog et InfoInhibitor le sont déjà.' }],
    component: AlertsWidget, badge: alertsBadge,
  },
  {
    type: 'certificates', label: 'Certificats', group: 'Ops', refresh: 300,
    description: 'Certificats cert-manager : état d’émission et jours avant expiration.',
    defaults: { warnDays: 14 },
    fields: [
      { key: 'warnDays', label: 'Alerte avant expiration (jours)', kind: 'number' },
      { key: 'namespaces', label: 'Namespaces à afficher', kind: 'lines', help: 'Un par ligne. Vide : tous.' },
      { key: 'exclude', label: 'Namespaces à masquer', kind: 'lines' },
      context,
      limit(),
    ],
    component: CertificatesWidget, badge: certificatesBadge,
  },
  {
    type: 'backups', label: 'Sauvegardes', group: 'Ops', refresh: 120,
    description: 'Dernière sauvegarde réussie des bases CloudNativePG et des CronJobs de sauvegarde.',
    defaults: { maxAgeHours: 26, cronjobs: [] },
    fields: [
      { key: 'cronjobs', label: 'CronJobs de sauvegarde', kind: 'lines', help: 'Un par ligne, au format namespace/nom. Les clusters CloudNativePG sont détectés automatiquement.' },
      { key: 'maxAgeHours', label: 'Âge maximal d’une sauvegarde (heures)', kind: 'number', help: 'Au-delà, la source passe en retard. 26 convient à une sauvegarde quotidienne.' },
      context,
    ],
    component: BackupsWidget, badge: backupsBadge,
  },
  {
    type: 'bookmarks', label: 'Applications et liens', group: 'Outils', refresh: 60,
    description: 'Liens groupés vers vos applications, avec vérification de disponibilité facultative.',
    defaults: { groups: [{ title: 'Lab', links: [{ title: 'Exemple', url: 'https://example.com', description: 'Description', checkUrl: '' }] }] },
    fields: [{ key: 'groups', label: 'Groupes de liens', kind: 'yaml', help: 'Liste de { title, links: [{ title, url, description, checkUrl }] }. checkUrl est interrogée par le serveur.' }],
    component: BookmarksWidget,
  },
  {
    type: 'rss', label: 'Flux RSS', group: 'Informations', refresh: 600,
    description: 'Derniers articles de plusieurs flux RSS ou Atom, fusionnés par date.',
    defaults: { limit: 12, feeds: [{ title: 'Kubernetes', url: 'https://kubernetes.io/feed.xml' }] },
    fields: [{ key: 'feeds', label: 'Flux', kind: 'yaml', help: 'Liste de { title, url }.' }, limit('Nombre d’articles affichés.')],
    component: RssWidget,
  },
  {
    type: 'videos', label: 'Vidéos YouTube', group: 'Informations', refresh: 900,
    description: 'Dernières vidéos des chaînes que vous suivez.',
    defaults: { limit: 8, channels: [] },
    fields: [{ key: 'channels', label: 'Identifiants de chaîne', kind: 'lines', help: 'Un identifiant UC… par ligne.' }, limit('Nombre de vidéos affichées.')],
    component: VideosWidget,
  },
  {
    type: 'markets', label: 'Marchés crypto', group: 'Informations', refresh: 300,
    description: 'Cours, variation sur 24 h et tendance sur 7 jours (CoinGecko).',
    defaults: { currency: 'usd', coins: ['bitcoin', 'ethereum'] },
    fields: [
      { key: 'coins', label: 'Identifiants CoinGecko', kind: 'lines', help: 'Un par ligne : bitcoin, ethereum, solana…' },
      { key: 'currency', label: 'Devise', kind: 'select', choices: [['usd', 'Dollar (USD)'], ['eur', 'Euro (EUR)'], ['chf', 'Franc suisse (CHF)'], ['gbp', 'Livre (GBP)']] },
    ],
    component: MarketsWidget,
  },
  {
    type: 'weather', label: 'Météo', group: 'Informations', refresh: 900,
    description: 'Conditions actuelles et prévisions d’une ville (Open-Meteo).',
    defaults: { location: 'Paris' },
    fields: [{ key: 'location', label: 'Ville', kind: 'text', placeholder: 'Antibes' }],
    component: WeatherWidget, badge: weatherBadge,
  },
  {
    type: 'calendar', label: 'Calendrier', group: 'Informations', refresh: 600,
    description: 'Mois en cours et prochains événements de vos agendas iCal.',
    defaults: { calendars: [] },
    fields: [{ key: 'calendars', label: 'Agendas iCal', kind: 'yaml', help: 'Liste de { title, url }. Pour une adresse privée, utilisez une variable serveur : ${PORTAL_VAR_ICAL_URL}.' }],
    component: CalendarWidget,
  },
  {
    type: 'hackernews', label: 'Hacker News', group: 'Informations', refresh: 600,
    description: 'Publications de la page d’accueil de Hacker News.',
    defaults: { limit: 8 }, fields: [limit('Nombre de publications affichées.')], component: PostsWidget,
  },
  {
    type: 'reddit', label: 'Reddit', group: 'Informations', refresh: 600,
    description: 'Publications populaires des subreddits choisis.',
    defaults: { subreddits: ['selfhosted'], sort: 'hot', limit: 8 },
    fields: [
      { key: 'subreddits', label: 'Subreddits', kind: 'lines', help: 'Un par ligne, sans le préfixe r/.' },
      { key: 'sort', label: 'Tri', kind: 'select', choices: [['hot', 'Populaires'], ['top', 'Meilleures du jour'], ['new', 'Récentes']] },
      limit('Nombre de publications affichées.'),
    ],
    component: PostsWidget,
  },
  {
    type: 'search', label: 'Recherche', group: 'Outils', refresh: 0,
    description: 'Barre de recherche web avec raccourcis (touche / pour y accéder).',
    defaults: { engine: 'duckduckgo', bangs: [{ prefix: '!gh', url: 'https://github.com/search?q={q}' }] },
    fields: [
      { key: 'engine', label: 'Moteur', kind: 'select', choices: [['duckduckgo', 'DuckDuckGo'], ['google', 'Google'], ['kagi', 'Kagi'], ['startpage', 'Startpage'], ['brave', 'Brave']] },
      { key: 'bangs', label: 'Raccourcis', kind: 'yaml', help: 'Liste de { prefix, url } ; {q} est remplacé par la recherche.' },
    ],
    component: SearchWidget as WidgetMeta['component'],
  },
  {
    type: 'clock', label: 'Horloges', group: 'Outils', refresh: 0,
    description: 'Heure locale et autres fuseaux horaires.',
    defaults: { zones: [{ label: 'New York', timezone: 'America/New_York' }] },
    fields: [{ key: 'zones', label: 'Autres fuseaux', kind: 'yaml', help: 'Liste de { label, timezone }, par exemple Asia/Tokyo.' }],
    component: ClockWidget as WidgetMeta['component'],
  },
];

export const registry: Record<string, WidgetMeta> = Object.fromEntries(list.map((m) => [m.type, m]));
export const catalog = list;
