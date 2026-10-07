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
    columns:
      - size: small        # small | full
        widgets:
          - type: kubernetes
            title: Cluster
          - type: alerts
            options:
              url: ${PORTAL_VAR_PROMETHEUS_URL}
```

Les références `${PORTAL_VAR_*}` sont résolues côté serveur au moment de la
requête et ne sont jamais envoyées au navigateur : c'est l'endroit où mettre
une adresse iCal privée (`PORTAL_VAR_ICAL_URL`).

### Widgets

| Type | Source | Options principales |
| --- | --- | --- |
| `kubernetes` | API Kubernetes | `context` |
| `workloads` | API Kubernetes | `namespaces`, `exclude`, `limit`, `context` |
| `events` | API Kubernetes (avertissements) | `namespaces`, `limit`, `context` |
| `argocd` | Applications Argo CD via l'API Kubernetes | `url` (liens), `namespace`, `context` |
| `gatus` | API Gatus | `url`, `publicUrl` |
| `prometheus` | PromQL | `url`, `stats: [{label, query, format, warn, danger, sparkline}]` |
| `alerts` | Alertes Prometheus en cours | `url`, `ignore` |
| `bookmarks` | Liens, sonde HTTP facultative | `groups: [{title, links: [{title, url, description, checkUrl}]}]` |
| `rss` | RSS / Atom | `feeds: [{title, url}]`, `limit` |
| `videos` | Flux YouTube | `channels` (identifiants `UC…`), `limit` |
| `markets` | CoinGecko | `coins`, `currency` |
| `weather` | Open-Meteo | `location` ou `latitude` + `longitude` |
| `calendar` | iCal | `calendars: [{title, url}]`, `days` |
| `hackernews` | Algolia HN | `limit` |
| `reddit` | Flux Atom Reddit (sans scores) | `subreddits`, `sort`, `limit` |
| `search` | Navigateur | `engine`, `bangs: [{prefix, url}]` |
| `clock` | Navigateur | `zones: [{label, timezone}]` |

Le bandeau résume en continu tous les widgets `kubernetes`, `argocd`, `gatus`
et `alerts` de la configuration, quelle que soit la page affichée.

## Sécurité

Le portail n'a pas d'authentification : son accès repose sur le réseau (LAN et
tailnet, via l'allowlist Traefik). Quiconque l'atteint peut modifier la
disposition, donc faire interroger au serveur des adresses HTTP de son choix.
Passez `PORTAL_READONLY=true` pour figer la disposition sur le YAML. Les
requêtes d'écriture venant d'un autre site sont refusées. Le compte de service
ne lit ni Secrets ni ConfigMaps.

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
