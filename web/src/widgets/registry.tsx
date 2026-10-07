import type { ComponentType } from 'react';
import type { Options } from '../api';
import { ActivityWidget, AlertsWidget, ArgoWidget, BackupsWidget, BookmarksWidget, CertificatesWidget, ClusterWidget, DeadlinesWidget, DigestWidget, DriftWidget, EventsWidget, ForecastWidget, GameServerWidget, GatusWidget, IncidentsWidget, PostgresWidget, PrometheusWidget, PullsWidget, ReleasesWidget, StatusWidget, TopWidget, TopologyWidget, VersionsWidget, WorkloadsWidget, alertsBadge, argoBadge, backupsBadge, certificatesBadge, clusterBadge, driftBadge, gameServerBadge, gatusBadge, incidentsBadge, pullsBadge, statusBadge, versionsBadge, workloadsBadge } from './ops';
import { CalendarWidget, ClockWidget, MarketsWidget, PostsWidget, RssWidget, SearchWidget, VideosWidget, WeatherWidget, weatherBadge } from './info';

export type Field = {
  key: string;
  label: string;
  kind: 'text' | 'number' | 'bool' | 'select' | 'lines' | 'yaml' | 'list';
  help?: string;
  placeholder?: string;
  choices?: [string, string][];
  // For `list`: the fields of each entry (text, number, select or a nested list).
  item?: Field[];
  itemName?: string;
  wide?: boolean;
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
  component: ComponentType<{ data: any; options: Options; widgetId: string }>;
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
      {
        key: 'stats', label: 'Métriques', kind: 'list', itemName: 'une métrique',
        item: [
          { key: 'label', label: 'Nom', kind: 'text', placeholder: 'CPU' },
          { key: 'format', label: 'Format', kind: 'select', choices: [['number', 'Nombre'], ['percent', 'Pourcentage'], ['bytes', 'Octets'], ['duration', 'Durée (secondes)']] },
          { key: 'query', label: 'Requête PromQL', kind: 'text', wide: true },
          { key: 'warn', label: 'Seuil d’avertissement', kind: 'number' },
          { key: 'danger', label: 'Seuil critique', kind: 'number' },
        ],
      },
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
    type: 'activity', label: 'Activité récente', group: 'Ops', refresh: 60,
    description: 'Fil chronologique des déploiements, alertes, sauvegardes et redémarrages.',
    defaults: { hours: 72, limit: 12, url: '', cronjobs: [] },
    fields: [
      { key: 'url', label: 'Adresse de Prometheus', kind: 'text', help: 'Facultatif : ajoute les alertes au fil.' },
      { key: 'cronjobs', label: 'CronJobs de sauvegarde', kind: 'lines', help: 'Un par ligne, au format namespace/nom.' },
      { key: 'hours', label: 'Période (heures)', kind: 'number' },
      limit('Nombre d’entrées affichées.'),
      context,
    ],
    component: ActivityWidget,
  },
  {
    type: 'digest', label: 'Résumé des 24 heures', group: 'Ops', refresh: 300,
    description: 'Quelques phrases sur la journée écoulée : incidents, déploiements, sauvegardes, alertes.',
    defaults: { url: '', cronjobs: [] },
    fields: [
      { key: 'url', label: 'Adresse de Prometheus', kind: 'text', help: 'Facultatif : compte aussi les alertes actives.' },
      { key: 'cronjobs', label: 'CronJobs de sauvegarde', kind: 'lines', help: 'Un par ligne, au format namespace/nom.' },
      context,
    ],
    component: DigestWidget,
  },
  {
    type: 'top', label: 'Top consommateurs', group: 'Ops', refresh: 30,
    description: 'Les pods qui consomment le plus de processeur et de mémoire, et ceux qui approchent de leur limite.',
    defaults: { url: '', limit: 5 },
    fields: [promUrl, limit('Nombre de pods par classement.')],
    component: TopWidget,
  },
  {
    type: 'postgres', label: 'PostgreSQL', group: 'Ops', refresh: 60,
    description: 'Santé des bases CloudNativePG : instances, archivage, dernière sauvegarde, taille et connexions.',
    defaults: { url: '' },
    fields: [{ key: 'url', label: 'Adresse de Prometheus', kind: 'text', help: 'Facultatif : ajoute la taille et les connexions si les métriques CloudNativePG sont collectées.' }, context],
    component: PostgresWidget,
  },
  {
    type: 'drift', label: 'Dérive de configuration', group: 'Ops', refresh: 120,
    description: 'Ce qui vit dans le cluster hors de Git : Applications détachées de la branche, namespaces et workloads non gérés.',
    defaults: { branch: 'master', ignore: [] },
    fields: [
      { key: 'branch', label: 'Branche de référence', kind: 'text', placeholder: 'master' },
      { key: 'ignore', label: 'Namespaces à ignorer', kind: 'lines', help: 'Un par ligne, pour ceux gérés par un autre outil.' },
      context,
      limit(),
    ],
    component: DriftWidget, badge: driftBadge,
  },
  {
    type: 'status', label: 'Services tiers', group: 'Informations', refresh: 300,
    description: 'État annoncé par les pages de statut de vos fournisseurs (GitHub, Cloudflare, Tailscale…).',
    defaults: { services: [{ title: 'GitHub', url: 'https://www.githubstatus.com' }, { title: 'Cloudflare', url: 'https://www.cloudflarestatus.com' }] },
    fields: [
      {
        key: 'services', label: 'Pages de statut', kind: 'list', itemName: 'un service',
        item: [
          { key: 'title', label: 'Nom', kind: 'text' },
          { key: 'url', label: 'Adresse de la page', kind: 'text', placeholder: 'https://www.githubstatus.com', help: 'Pages au format Statuspage.' },
        ],
      },
    ],
    component: StatusWidget, badge: statusBadge,
  },
  {
    type: 'topology', label: 'Carte du lab', group: 'Ops', refresh: 30,
    description: 'Schéma vivant des services et de leurs dépendances ; les liaisons s’animent et prennent la couleur de l’état.',
    defaults: {
      nodes: [
        { id: 'internet', label: 'Internet', layer: 0 },
        { id: 'app', label: 'Application', layer: 1, app: '' },
      ],
      links: [{ from: 'internet', to: 'app' }],
    },
    fields: [
      {
        key: 'nodes', label: 'Éléments', kind: 'list', itemName: 'un élément',
        item: [
          { key: 'id', label: 'Identifiant', kind: 'text', placeholder: 'traefik' },
          { key: 'label', label: 'Nom affiché', kind: 'text' },
          { key: 'layer', label: 'Colonne', kind: 'number', help: '0 à gauche, puis 1, 2…' },
          { key: 'app', label: 'Application Argo CD', kind: 'text', help: 'Donne son état à l’élément.' },
          { key: 'endpoint', label: 'Endpoint Gatus', kind: 'text' },
          { key: 'note', label: 'Note', kind: 'text' },
        ],
      },
      {
        key: 'links', label: 'Liaisons', kind: 'list', itemName: 'une liaison',
        item: [
          { key: 'from', label: 'De', kind: 'text' },
          { key: 'to', label: 'Vers', kind: 'text' },
        ],
      },
      { key: 'gatus', label: 'Adresse de Gatus', kind: 'text', help: 'Nécessaire si des éléments citent un endpoint.' },
      context,
    ],
    component: TopologyWidget,
  },
  {
    type: 'incidents', label: 'Journal d’incidents', group: 'Ops', refresh: 30,
    description: 'Incidents ouverts et clos automatiquement d’après le bandeau, avec leur durée et ce qui a changé juste avant.',
    defaults: { days: 30, limit: 6 },
    fields: [
      { key: 'days', label: 'Période des statistiques (jours)', kind: 'number' },
      limit('Nombre d’incidents clos affichés.'),
    ],
    component: IncidentsWidget, badge: incidentsBadge,
  },
  {
    type: 'deadlines', label: 'Échéances', group: 'Ops', refresh: 1800,
    description: 'Compte à rebours de tout ce qui expire : certificats, jetons, noms de domaine, fins de support.',
    defaults: { warnDays: 30, certificates: true, items: [], eol: [{ product: 'kubernetes', cycle: '' }] },
    fields: [
      {
        key: 'items', label: 'Échéances saisies', kind: 'list', itemName: 'une échéance',
        item: [
          { key: 'title', label: 'Nom', kind: 'text', placeholder: 'Jeton GitHub du portail' },
          { key: 'date', label: 'Date', kind: 'text', placeholder: '2027-01-31', help: 'Au format AAAA-MM-JJ.' },
          { key: 'note', label: 'Note', kind: 'text', wide: true },
        ],
      },
      {
        key: 'eol', label: 'Fins de support (endoflife.date)', kind: 'list', itemName: 'un produit',
        item: [
          { key: 'product', label: 'Produit', kind: 'text', placeholder: 'kubernetes' },
          { key: 'cycle', label: 'Version', kind: 'text', placeholder: '1.36', help: 'Vide pour kubernetes : version du cluster.' },
          { key: 'title', label: 'Nom affiché', kind: 'text' },
        ],
      },
      { key: 'certificates', label: 'Inclure les certificats cert-manager', kind: 'bool' },
      { key: 'warnDays', label: 'Signaler à moins de (jours)', kind: 'number' },
      context,
    ],
    component: DeadlinesWidget,
  },
  {
    type: 'versions', label: 'Retard de versions', group: 'Ops', refresh: 3600,
    description: 'Version en service de chaque composant face à la dernière publiée sur GitHub.',
    defaults: { items: [{ name: 'Kubernetes', repo: 'kubernetes/kubernetes', source: 'node:kubelet' }] },
    fields: [
      {
        key: 'items', label: 'Composants', kind: 'list', itemName: 'un composant',
        item: [
          { key: 'name', label: 'Nom', kind: 'text', placeholder: 'Argo CD' },
          { key: 'repo', label: 'Dépôt GitHub', kind: 'text', placeholder: 'argoproj/argo-cd' },
          { key: 'source', label: 'Version en service', kind: 'text', wide: true, placeholder: 'argocd-system/argocd-server', help: 'namespace/nom d’un workload (étiquette de son image), node:kubelet ou node:os.' },
        ],
      },
      { key: 'token', label: 'Jeton GitHub', kind: 'text', placeholder: '${PORTAL_VAR_GITHUB_TOKEN}', help: 'Facultatif. Sans jeton, GitHub limite à 60 requêtes par heure.' },
      context,
    ],
    component: VersionsWidget, badge: versionsBadge,
  },
  {
    type: 'forecast', label: 'Prévision de saturation', group: 'Ops', refresh: 300,
    description: 'Dans combien de jours chaque disque sera plein, au rythme actuel.',
    defaults: { url: '', hours: 72 },
    fields: [
      promUrl,
      { key: 'hours', label: 'Tendance calculée sur (heures)', kind: 'number' },
      { key: 'mountpoints', label: 'Points de montage', kind: 'lines', help: 'Un par ligne. Vide : tous les disques de plus de 2 Gio.' },
    ],
    component: ForecastWidget,
  },
  {
    type: 'gameserver', label: 'Serveur de jeu', group: 'Ops', refresh: 30,
    description: 'État, carte et joueurs connectés d’un serveur compatible Steam (Project Zomboid, Valheim…).',
    defaults: { address: '' },
    fields: [{ key: 'address', label: 'Adresse du serveur', kind: 'text', placeholder: 'pz.example.com:16261', help: 'Hôte et port UDP de requête, au format hôte:port.' }],
    component: GameServerWidget, badge: gameServerBadge,
  },
  {
    type: 'releases', label: 'Versions des projets', group: 'Informations', refresh: 3600,
    description: 'Dernière version publiée des projets GitHub que vous suivez.',
    defaults: { repos: ['siderolabs/talos', 'argoproj/argo-cd'] },
    fields: [
      { key: 'repos', label: 'Dépôts GitHub', kind: 'lines', help: 'Un par ligne, au format propriétaire/nom.' },
      { key: 'token', label: 'Jeton GitHub', kind: 'text', placeholder: '${PORTAL_VAR_GITHUB_TOKEN}', help: 'Facultatif. Sans jeton, GitHub limite à 60 requêtes par heure.' },
    ],
    component: ReleasesWidget,
  },
  {
    type: 'pulls', label: 'Pull requests', group: 'Ops', refresh: 600,
    description: 'Pull requests ouvertes d’un dépôt GitHub (Renovate, déploiements…).',
    defaults: { repo: '', limit: 8 },
    fields: [
      { key: 'repo', label: 'Dépôt GitHub', kind: 'text', placeholder: 'propriétaire/nom' },
      { key: 'token', label: 'Jeton GitHub', kind: 'text', placeholder: '${PORTAL_VAR_GITHUB_TOKEN}', help: 'Nécessaire pour un dépôt privé.' },
      limit('Nombre de pull requests affichées.'),
    ],
    component: PullsWidget, badge: pullsBadge,
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
    fields: [
      {
        key: 'groups', label: 'Groupes', kind: 'list', itemName: 'un groupe',
        item: [
          { key: 'title', label: 'Nom du groupe', kind: 'text', wide: true },
          {
            key: 'links', label: 'Liens', kind: 'list', itemName: 'un lien',
            item: [
              { key: 'title', label: 'Nom', kind: 'text' },
              { key: 'url', label: 'Adresse', kind: 'text', placeholder: 'https://' },
              { key: 'description', label: 'Description', kind: 'text' },
              { key: 'icon', label: 'Icône', kind: 'text', placeholder: 'si:grafana', help: 'si:nom (Simple Icons) ou adresse d’une image.' },
              { key: 'checkUrl', label: 'Adresse à vérifier', kind: 'text', wide: true, help: 'Facultatif. Interrogée par le serveur pour afficher l’état en ligne.' },
            ],
          },
        ],
      },
    ],
    component: BookmarksWidget,
  },
  {
    type: 'rss', label: 'Flux RSS', group: 'Informations', refresh: 600,
    description: 'Derniers articles de plusieurs flux RSS ou Atom, fusionnés par date.',
    defaults: { limit: 12, feeds: [{ title: 'Kubernetes', url: 'https://kubernetes.io/feed.xml' }] },
    fields: [{ key: 'feeds', label: 'Flux', kind: 'list', itemName: 'un flux', item: [{ key: 'title', label: 'Nom', kind: 'text' }, { key: 'url', label: 'Adresse', kind: 'text', placeholder: 'https://' }] }, limit('Nombre d’articles affichés.')],
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
    fields: [{ key: 'calendars', label: 'Agendas iCal', kind: 'list', itemName: 'un agenda', item: [{ key: 'title', label: 'Nom', kind: 'text' }, { key: 'url', label: 'Adresse', kind: 'text', placeholder: 'https://' }], help: 'Pour une adresse privée, utilisez une variable serveur : ${PORTAL_VAR_ICAL_URL}.' }],
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
      { key: 'bangs', label: 'Raccourcis', kind: 'list', itemName: 'un raccourci', help: '{q} est remplacé par la recherche.', item: [{ key: 'prefix', label: 'Préfixe', kind: 'text', placeholder: '!gh' }, { key: 'url', label: 'Adresse', kind: 'text', placeholder: 'https://github.com/search?q={q}' }] },
    ],
    component: SearchWidget as WidgetMeta['component'],
  },
  {
    type: 'clock', label: 'Horloges', group: 'Outils', refresh: 0,
    description: 'Heure locale et autres fuseaux horaires.',
    defaults: { zones: [{ label: 'New York', timezone: 'America/New_York' }] },
    fields: [{ key: 'zones', label: 'Autres fuseaux', kind: 'list', itemName: 'un fuseau', item: [{ key: 'label', label: 'Nom', kind: 'text', placeholder: 'Tokyo' }, { key: 'timezone', label: 'Fuseau', kind: 'text', placeholder: 'Asia/Tokyo' }] }],
    component: ClockWidget as WidgetMeta['component'],
  },
];

list.push({
  type: 'section', label: 'Section', group: 'Outils', refresh: 0,
  description: 'Un titre qui regroupe les widgets placés dessous, jusqu’à la section suivante, et permet de les replier.',
  defaults: { collapsed: false },
  fields: [{ key: 'collapsed', label: 'Repliée par défaut', kind: 'bool' }],
  component: () => null,
});

export const registry: Record<string, WidgetMeta> = Object.fromEntries(list.map((m) => [m.type, m]));
export const catalog = list;
