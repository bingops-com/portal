# LabOps Portal

Portail ops du home lab : état du cluster, des applications Argo CD, des
endpoints Gatus et des métriques Prometheus, plus une page de veille (flux
RSS, vidéos, marchés crypto, météo, calendrier, Hacker News, Reddit). Les
pages se composent de colonnes et de widgets, à la manière de Glance, et se
réorganisent dans le navigateur.

Un seul binaire Go sert l'API et l'interface React embarquée.

## Démarrer en local

```sh
make run          # build complet, puis http://localhost:3000
make dev          # API sur :3000 et Vite (rechargement à chaud) sur :5173
make check        # tests Go, typecheck, go vet
make e2e          # tests de l'interface dans un navigateur sans écran
```

Prérequis : Go 1.25+, Node 20.19+. Hors cluster, les widgets Kubernetes
utilisent le contexte courant du kubeconfig (ou l'option `context` du widget).
Pour les sources internes au cluster :

```sh
kubectl --context labprod -n monitoring port-forward svc/monitoring-kube-prometheus-prometheus 9090 &
PORTAL_VAR_PROMETHEUS_URL=http://localhost:9090 make run
```

## Configuration

| Variable | Défaut | Rôle |
| --- | --- | --- |
| `PORTAL_CONFIG` | `config/portal.yaml` | Pages et widgets par défaut, relu à chaud |
| `PORTAL_DATA` | `data` | Dossier de `layout.json` (disposition enregistrée depuis l'interface) |
| `PORTAL_ADDR` | `:3000` | Adresse d'écoute |
| `PORTAL_READONLY` | vide | `true` désactive l'édition dans le navigateur |
| `PORTAL_CLUSTER_NAME` | `cluster` | Nom affiché pour le cluster hôte |
| `PORTAL_OIDC_ISSUER` | vide | Active la connexion OpenID Connect exigée pour modifier (ex. `https://auth.example.com/application/o/portal/`) |
| `PORTAL_OIDC_CLIENT_ID` | | Identifiant du client public (PKCE, sans secret) |
| `PORTAL_OIDC_GROUP` | vide | Groupe requis pour modifier ; vide : tout compte authentifié |
| `PORTAL_PUBLIC_URL` | | Adresse externe du portail ; le fournisseur doit autoriser `<adresse>/auth/callback` |
| `PORTAL_INTERNAL_HOSTS` | vide | Liste (séparée par des virgules) des seuls hôtes internes que le serveur peut interroger : nom exact ou suffixe commençant par un point (`.svc.cluster.local`). Vide : aucune restriction |
| `PORTAL_VAR_*` | | Valeurs référencées par `${PORTAL_VAR_NOM}` dans les options |

### YAML et édition dans le navigateur

`portal.yaml` est la disposition versionnée. Le bouton **Modifier** permet de
déplacer les widgets (souris ou clavier), d'en ajouter, de les régler et de
gérer pages et colonnes ; **Enregistrer** écrit `layout.json`, qui prend alors
le pas sur les pages du YAML. **Exporter en YAML** télécharge la disposition
courante pour la reporter dans Git, **Revenir au YAML** supprime `layout.json`.

```yaml
pages:
  - name: Ops
    group: Lab           # facultatif : regroupe des pages dans la navigation
    columns:
      - size: small        # small | full
        widgets:
          - type: kubernetes
            title: Cluster
          - type: alerts
            options:
              url: ${PORTAL_VAR_PROMETHEUS_URL}
```

Pour qu'une page reste lisible en grossissant : un widget `section` titre les
widgets placés dessous dans sa colonne, jusqu'à la section suivante, et permet
de les replier (choix mémorisé par navigateur, `collapsed: true` pour replier
par défaut). L'onglet d'une page porte une pastille quand l'un de ses widgets
alimente un indicateur du bandeau à surveiller ou en panne.

Les références `${PORTAL_VAR_*}` sont résolues côté serveur au moment de la
requête et ne sont jamais envoyées au navigateur : c'est l'endroit où mettre
une adresse iCal privée (`PORTAL_VAR_ICAL_URL`).

### Widgets

| Type | Source | Options principales |
| --- | --- | --- |
| `kubernetes` | API Kubernetes | `context` |
| `workloads` | API Kubernetes | `namespaces`, `exclude`, `limit`, `context` |
| `events` | API Kubernetes (avertissements) | `namespaces`, `limit`, `context` |
| `argocd` | Applications Argo CD via l'API Kubernetes | `url` (liens), `namespace`, `ignore`, `context` |
| `activity` | Fil des déploiements, alertes, sauvegardes et redémarrages | `url` (Prometheus, facultatif), `cronjobs`, `hours`, `limit`, `context` |
| `digest` | Résumé des 24 heures (incidents, activité, alertes) | `url`, `cronjobs`, `context` |
| `top` | Pods les plus gourmands et proches de leur limite (Prometheus, cAdvisor) | `url`, `limit` |
| `postgres` | Santé des clusters CloudNativePG ; taille et connexions si leurs métriques sont collectées | `url`, `context` |
| `drift` | Applications détachées de la branche, namespaces et workloads hors Argo CD | `branch`, `ignore`, `context` |
| `status` | Pages de statut des fournisseurs (format Statuspage) | `services: [{title, url}]` |
| `topology` | Carte du lab : états tirés des Applications Argo CD et des endpoints Gatus | `nodes: [{id, label, layer, app, endpoint, note}]`, `links: [{from, to}]`, `gatus`, `context` |
| `incidents` | Journal tenu par le serveur d'après le bandeau | `days`, `limit` |
| `deadlines` | Certificats, échéances saisies, fins de support (endoflife.date) | `items: [{title, date, note}]`, `eol: [{product, cycle, title}]`, `certificates`, `warnDays` |
| `versions` | Version en service face à la dernière release GitHub | `items: [{name, repo, source}]` (`source` : `namespace/workload`, `node:kubelet`, `node:os`), `token` |
| `forecast` | Jours avant saturation des disques (Prometheus, node-exporter) | `url`, `hours`, `mountpoints` |
| `gameserver` | Requête Steam (A2S) vers un serveur de jeu | `address` (`hôte:port` UDP) |
| `releases` | Dernières versions de dépôts GitHub | `repos`, `token` (facultatif) |
| `pulls` | Pull requests ouvertes d'un dépôt GitHub | `repo`, `token` (facultatif), `limit` |
| `certificates` | Certificats cert-manager via l'API Kubernetes | `warnDays`, `namespaces`, `exclude`, `context` |
| `backups` | Sauvegardes CloudNativePG (détectées) et CronJobs | `cronjobs` (`namespace/nom`), `maxAgeHours`, `context` |
| `gatus` | API Gatus | `url`, `publicUrl` |
| `prometheus` | PromQL | `url`, `stats: [{label, query, format, warn, danger, sparkline}]` |
| `alerts` | Alertes Prometheus en cours | `url`, `ignore` |
| `bookmarks` | Liens, sonde HTTP facultative | `groups: [{title, links: [{title, url, description, icon, checkUrl}]}]` ; `icon` vaut `si:<nom>` (Simple Icons, chargée depuis leur CDN par le navigateur) ou l'adresse d'une image |
| `rss` | RSS / Atom | `feeds: [{title, url}]`, `limit` |
| `videos` | Flux YouTube | `channels` (identifiants `UC…`), `limit` |
| `markets` | CoinGecko | `coins`, `currency` |
| `weather` | Open-Meteo | `location` ou `latitude` + `longitude` |
| `calendar` | iCal | `calendars: [{title, url}]`, `days` |
| `hackernews` | Algolia HN | `limit` |
| `reddit` | Flux Atom Reddit (sans scores) | `subreddits`, `sort`, `limit` |
| `search` | Navigateur | `engine`, `bangs: [{prefix, url}]` |
| `clock` | Navigateur | `zones: [{label, timezone}]` |

Le bandeau résume en continu tous les widgets `kubernetes`, `argocd`, `gatus`,
`alerts`, `certificates` et `backups` de la configuration, quelle que soit la
page affichée. Les alertes de sévérité `info` n'y comptent pas. Un indicateur
qui change d'état s'anime une fois et affiche depuis quand ; un clic amène au
widget concerné. Le titre de l'onglet indique le nombre d'indicateurs dégradés ;
le bandeau et l'icône restent bleus quel que soit l'état. `Ctrl+K` ouvre la palette de commandes (pages, liens
des widgets `bookmarks`, thème, édition).

Un clic sur une ligne ouvre un panneau de détail : pods et événements d'un
workload, ressources à traiter d'une application Argo CD, étiquettes d'une
alerte, historique d'une métrique sur 3 heures, 24 heures ou 7 jours. Dans le
mode édition, les listes (flux, liens, métriques, fuseaux) se règlent par
formulaire.

Mouvement et confort (tout se fige si le système demande de réduire les
animations) :

- un point bat dans le bandeau à chaque relevé et se vide si le serveur ne
  répond plus ; un indicateur qui revient au vert le signale, avec la durée ;
- les lignes glissent à leur nouvelle place quand un tri change, les valeurs
  clignotent dans le sens de leur variation, les courbes se tracent et les
  pourcentages s'affichent en anneaux ;
- les pages glissent dans le sens de la navigation ; chaque widget s'agrandit
  en plein cadre (bouton au survol de son titre) et montre alors toutes ses
  lignes ;
- la palette de commandes propose aussi : couleur d'accent, mode présentation
  (`?present=15`, un widget à la fois), signal sonore quand un indicateur
  passe au rouge. Le thème a un quatrième mode, « selon le soleil » ;
- un widget retiré en édition peut être rétabli pendant quelques secondes.

Autres comportements :

- un brouillon non enregistré est gardé dans le navigateur et proposé à la
  reprise de l'édition, y compris après une session expirée ;
- `?kiosk` (ou `?kiosk=45`) masque les commandes et fait tourner les pages
  toutes les 30 secondes (ou le nombre indiqué), pour un écran mural ;
- la palette de commandes trouve aussi les workloads et les applications
  Argo CD, et peut activer une notification du navigateur quand un indicateur
  passe au rouge ;
- `/metrics` expose, au format Prometheus, les appels aux sources par type de
  widget (`portal_fetch_total`, `portal_fetch_duration_seconds_sum`) et l'état
  de chaque indicateur du bandeau (`portal_readout_state`) ;
- l'état des indicateurs et la date de leur dernier changement sont conservés
  dans `PORTAL_DATA/readouts.json` ;
- le serveur relève le bandeau chaque minute, même sans visiteur : un
  indicateur dégradé ouvre un incident, son retour au vert le clôt. Le journal
  (`PORTAL_DATA/incidents.json`, 300 entrées) garde la durée et les
  déploiements, redémarrages et sauvegardes vus dans la demi-heure précédente.

## Sécurité

La lecture est ouverte à qui atteint le portail (LAN et tailnet, via
l'allowlist Traefik). Pour l'édition, deux modes :

- avec `PORTAL_OIDC_ISSUER`, modifier la disposition exige une connexion
  OpenID Connect (flux « authorization code » avec PKCE, client public), et
  éventuellement l'appartenance à `PORTAL_OIDC_GROUP`. La session dure 12 h ;
  sa clé de signature est créée dans `PORTAL_DATA/session.key` ;
- sans cette variable, quiconque atteint le portail peut modifier la
  disposition, donc faire interroger au serveur des adresses HTTP de son
  choix. `PORTAL_READONLY=true` fige alors la disposition sur le YAML.

Avec `PORTAL_INTERNAL_HOSTS`, le serveur refuse de se connecter à une adresse
privée (réseau local, cluster, boucle locale) dont l'hôte n'est pas dans la
liste, redirections comprises : un éditeur ne peut plus viser un service
interne arbitraire. Les adresses publiques restent libres.

Dans tous les cas, les requêtes d'écriture venant d'un autre site sont
refusées, et le compte de service ne lit ni Secrets ni ConfigMaps.

## Publication et déploiement

Ce dépôt ne contient que la source. L'image, les manifests et la configuration
déployée vivent dans `bingops-com/labops` :

- `docker/portal` construit `ghcr.io/bingops-com/portal` en clonant la branche
  `master` de ce dépôt ;
- `apps/workloads/portal` déploie le portail sur `labprod` (`https://lab.bingo`)
  et porte `base/portal.yaml`, la disposition réellement servie ;
- `apps/workloads/gatus` fournit les données de disponibilité.

`config/portal.yaml` n'est ici qu'un exemple pour le développement local ;
l'image n'embarque aucune configuration.

Chaque push sur `master` qui touche au code déclenche
`.github/workflows/trigger-rebuild.yml`, qui demande à `labops` de
reconstruire et publier l'image (secret `REPO_INFRA_TOKEN`, comme pour
`www.bingops.com`). Une pull request déclenche le même build sans publication.
Pour déployer l'image obtenue, reportez son tag `sha-…` dans le Deployment de
`labops` ; voir `docker/portal/README.md`.
