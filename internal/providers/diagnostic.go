package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// --- Top consumers ---------------------------------------------------------

type consumer struct {
	Namespace string  `json:"namespace"`
	Pod       string  `json:"pod"`
	Container string  `json:"container,omitempty"`
	Value     float64 `json:"value"`
}

func promConsumers(ctx context.Context, base, query string) ([]consumer, error) {
	var resp promVector
	if err := getJSON(ctx, base+"/api/v1/query?"+url.Values{"query": {query}}.Encode(), &resp); err != nil {
		return nil, err
	}
	out := []consumer{}
	for _, r := range resp.Data.Result {
		if len(r.Value) != 2 {
			continue
		}
		if v, ok := promFloat(r.Value[1]); ok {
			out = append(out, consumer{r.Metric["namespace"], r.Metric["pod"], r.Metric["container"], v})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Value > out[b].Value })
	return out, nil
}

func fetchTop(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		URL   string `json:"url"`
		Limit int    `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if o.URL == "" {
		return nil, errors.New("indiquez l'adresse de Prometheus dans les réglages du widget")
	}
	base, n := trimSlash(o.URL), clamp(o.Limit, 5, 1, 15)
	cpu, err := promConsumers(ctx, base, fmt.Sprintf(`topk(%d, sum by (namespace,pod) (rate(container_cpu_usage_seconds_total{container!=""}[5m])))`, n))
	if err != nil {
		return nil, err
	}
	mem, err := promConsumers(ctx, base, fmt.Sprintf(`topk(%d, sum by (namespace,pod) (container_memory_working_set_bytes{container!=""}))`, n))
	if err != nil {
		return nil, err
	}
	// Containers past 75 % of their memory limit are the next to be killed.
	tight, _ := promConsumers(ctx, base, fmt.Sprintf(`topk(%d, max by (namespace,pod,container) (container_memory_working_set_bytes{container!=""} / on(namespace,pod,container) kube_pod_container_resource_limits{resource="memory"}) > 0.75)`, n))
	if len(cpu) == 0 && len(mem) == 0 {
		return nil, errors.New("Prometheus ne rapporte aucune mesure de conteneur (cAdvisor absent ?)")
	}
	return map[string]any{"cpu": cpu, "memory": mem, "tight": tight}, nil
}

// --- PostgreSQL (CloudNativePG) ---------------------------------------------

type pgCluster struct {
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	Phase     string     `json:"phase"`
	Ready     int64      `json:"ready"`
	Instances int64      `json:"instances"`
	Primary   string     `json:"primary"`
	Archiving string     `json:"archiving"` // ok | down | unknown
	Storage   string     `json:"storage,omitempty"`
	Size      *float64   `json:"size,omitempty"`        // bytes, when metrics are scraped
	Backends  *float64   `json:"connections,omitempty"` // when metrics are scraped
	LastWAL   *float64   `json:"walAge,omitempty"`      // seconds since last archived WAL
	Backup    *time.Time `json:"lastBackup,omitempty"`
	State     string     `json:"state"`
}

func fetchPostgres(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context string `json:"context"`
		URL     string `json:"url"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	list, err := c.dynamic.Resource(cnpgClusters).List(ctx, metav1.ListOptions{})
	if err != nil {
		if absent(err) {
			return nil, errors.New("CloudNativePG n'est pas installé sur ce cluster")
		}
		return nil, kubeErr(err)
	}
	lastBackup := map[string]time.Time{}
	if runs, err := c.dynamic.Resource(cnpgBackups).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range runs.Items {
			u := &runs.Items[i]
			if phase, _, _ := unstructured.NestedString(u.Object, "status", "phase"); phase != "completed" {
				continue
			}
			owner, _, _ := unstructured.NestedString(u.Object, "spec", "cluster", "name")
			if t := nestedTime(u, "status", "stoppedAt"); t != nil && t.After(lastBackup[u.GetNamespace()+"/"+owner]) {
				lastBackup[u.GetNamespace()+"/"+owner] = *t
			}
		}
	}
	metric := func(query string) map[string]float64 {
		out := map[string]float64{}
		if o.URL == "" {
			return out
		}
		var resp promVector
		if getJSON(ctx, trimSlash(o.URL)+"/api/v1/query?"+url.Values{"query": {query}}.Encode(), &resp) != nil {
			return out
		}
		for _, r := range resp.Data.Result {
			if len(r.Value) == 2 {
				if v, ok := promFloat(r.Value[1]); ok {
					out[r.Metric["namespace"]+"/"+r.Metric["cluster"]] = v
				}
			}
		}
		return out
	}
	sizes := metric(`sum by (namespace, cluster) (label_replace(cnpg_pg_database_size_bytes, "cluster", "$1", "pod", "(.+)-[0-9]+"))`)
	backends := metric(`sum by (namespace, cluster) (label_replace(cnpg_backends_total, "cluster", "$1", "pod", "(.+)-[0-9]+"))`)
	walAge := metric(`max by (namespace, cluster) (label_replace(cnpg_pg_stat_archiver_seconds_since_last_archival, "cluster", "$1", "pod", "(.+)-[0-9]+"))`)

	clusters := []pgCluster{}
	for i := range list.Items {
		u := &list.Items[i]
		pg := pgCluster{Namespace: u.GetNamespace(), Name: u.GetName(), Archiving: "unknown", State: "ok"}
		pg.Phase, _, _ = unstructured.NestedString(u.Object, "status", "phase")
		pg.Ready, _, _ = unstructured.NestedInt64(u.Object, "status", "readyInstances")
		pg.Instances, _, _ = unstructured.NestedInt64(u.Object, "spec", "instances")
		pg.Primary, _, _ = unstructured.NestedString(u.Object, "status", "currentPrimary")
		pg.Storage, _, _ = unstructured.NestedString(u.Object, "spec", "storage", "size")
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, raw := range conds {
			cond, _ := raw.(map[string]any)
			if cond["type"] == "ContinuousArchiving" {
				pg.Archiving = "down"
				if cond["status"] == "True" {
					pg.Archiving = "ok"
				}
			}
		}
		key := pg.Namespace + "/" + pg.Name
		if v, ok := sizes[key]; ok {
			pg.Size = &v
		}
		if v, ok := backends[key]; ok {
			pg.Backends = &v
		}
		if v, ok := walAge[key]; ok {
			pg.LastWAL = &v
		}
		if t, ok := lastBackup[key]; ok {
			pg.Backup = &t
		}
		switch {
		case pg.Ready == 0:
			pg.State = "down"
		case pg.Ready < pg.Instances || pg.Archiving == "down":
			pg.State = "warn"
		}
		clusters = append(clusters, pg)
	}
	if len(clusters) == 0 {
		return nil, errors.New("aucun cluster PostgreSQL géré par CloudNativePG")
	}
	return map[string]any{"clusters": clusters, "metrics": len(sizes)+len(backends) > 0}, nil
}

// --- Third-party status pages (Statuspage format) ---------------------------

type upstream struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Detail    string `json:"detail"`
	URL       string `json:"url"`
	Incidents int    `json:"incidents"`
}

func fetchStatus(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Services []namedURL `json:"services"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Services) == 0 {
		return nil, errors.New("ajoutez au moins une page de statut dans les réglages du widget")
	}
	if len(o.Services) > 20 {
		o.Services = o.Services[:20]
	}
	rows := make([]upstream, len(o.Services))
	parallel(len(o.Services), 6, func(i int) {
		s := o.Services[i]
		base := trimSlash(s.URL)
		row := upstream{Name: s.Title, State: "unknown", Detail: "page de statut illisible", URL: base}
		defer func() { rows[i] = row }()
		var raw struct {
			Page struct {
				Name string `json:"name"`
			} `json:"page"`
			Status struct {
				Indicator   string `json:"indicator"`
				Description string `json:"description"`
			} `json:"status"`
		}
		if getJSON(ctx, base+"/api/v2/status.json", &raw) != nil {
			return
		}
		if row.Name == "" {
			row.Name = raw.Page.Name
		}
		row.Detail = raw.Status.Description
		switch raw.Status.Indicator {
		case "none":
			row.State = "ok"
		case "minor", "maintenance":
			row.State = "warn"
		default:
			row.State = "down"
		}
		var open struct {
			Incidents []struct {
				Name string `json:"name"`
			} `json:"incidents"`
		}
		if getJSON(ctx, base+"/api/v2/incidents/unresolved.json", &open) == nil && len(open.Incidents) > 0 {
			row.Incidents = len(open.Incidents)
			row.Detail = open.Incidents[0].Name
			// A provider may still call itself operational during an incident.
			if row.State == "ok" {
				row.State = "warn"
			}
		}
	})
	rank := map[string]int{"down": 0, "warn": 1, "unknown": 2, "ok": 3}
	sort.SliceStable(rows, func(a, b int) bool { return rank[rows[a].State] < rank[rows[b].State] })
	healthy := 0
	for _, r := range rows {
		if r.State == "ok" {
			healthy++
		}
	}
	return map[string]any{"services": rows, "healthy": healthy}, nil
}

// --- Daily digest: the last 24 hours in a few sentences ---------------------

func duration(d time.Duration) string {
	m := int(d.Minutes())
	switch {
	case m < 1:
		return "moins d'une minute"
	case m < 60:
		return fmt.Sprintf("%d min", m)
	case m < 48*60:
		return fmt.Sprintf("%d h %02d", m/60, m%60)
	}
	return fmt.Sprintf("%d j", m/1440)
}

func fetchDigest(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	lines := []map[string]string{}
	add := func(state, text string) { lines = append(lines, map[string]string{"state": state, "text": text}) }

	var total time.Duration
	var open []string
	count := 0
	for _, it := range Incidents.snapshot() {
		end := now
		if it.Closed != nil {
			end = *it.Closed
		}
		if end.Before(since) {
			continue
		}
		count++
		start := it.Opened
		if start.Before(since) {
			start = since
		}
		total += end.Sub(start)
		if it.Closed == nil {
			open = append(open, it.Label)
		}
	}
	switch {
	case count == 0:
		add("ok", "Aucun incident sur les dernières 24 heures.")
	case len(open) == 0:
		add("ok", fmt.Sprintf("%s sur 24 heures, tous clos (%s au total).", plural(count, "incident", "incidents"), duration(total)))
	default:
		add("warn", fmt.Sprintf("%s sur 24 heures (%s au total), encore en cours : %s.", plural(count, "incident", "incidents"), duration(total), strings.Join(open, ", ")))
	}

	scoped := map[string]any{"hours": 24, "limit": 60}
	for _, k := range []string{"context", "cronjobs", "url"} {
		if v, ok := opts[k]; ok {
			scoped[k] = v
		}
	}
	if out, err := fetchActivity(ctx, d, scoped); err == nil {
		kinds := map[string]int{}
		crashes := 0
		for _, it := range out.(map[string]any)["items"].([]ActivityItem) {
			kinds[it.Kind]++
			if it.Kind == "restart" && it.State == "warn" {
				crashes++
			}
		}
		var parts []string
		if n := kinds["deploy"]; n > 0 {
			parts = append(parts, plural(n, "déploiement", "déploiements"))
		}
		if n := kinds["backup"]; n > 0 {
			parts = append(parts, plural(n, "sauvegarde terminée", "sauvegardes terminées"))
		}
		if len(parts) > 0 {
			text := strings.Join(parts, ", ") + "."
			add("ok", strings.ToUpper(text[:1])+text[1:])
		} else {
			add("none", "Ni déploiement ni sauvegarde relevés.")
		}
		if crashes > 0 {
			add("warn", plural(crashes, "conteneur a redémarré sur une erreur.", "conteneurs ont redémarré sur une erreur."))
		}
		if n := kinds["alert"]; n > 0 {
			add("warn", plural(n, "alerte est active.", "alertes sont actives."))
		}
	}
	return map[string]any{"lines": lines, "generated": now}, nil
}

// --- Configuration drift: what lives in the cluster outside GitOps ----------

type driftItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
	State  string `json:"state"`
}

var systemNamespaces = map[string]bool{"default": true, "kube-system": true, "kube-public": true, "kube-node-lease": true}

func fetchDrift(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context string   `json:"context"`
		Branch  string   `json:"branch"`
		Ignore  []string `json:"ignore"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	branch := o.Branch
	if branch == "" {
		branch = "master"
	}
	ignored := map[string]bool{}
	for _, n := range o.Ignore {
		ignored[strings.TrimSpace(n)] = true
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	apps, err := c.dynamic.Resource(argoApps).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, kubeErr(err)
	}
	items := []driftItem{}
	managed := map[string]bool{}
	for i := range apps.Items {
		u := &apps.Items[i]
		managed[u.GetNamespace()] = true
		if ns, _, _ := unstructured.NestedString(u.Object, "spec", "destination", "namespace"); ns != "" {
			managed[ns] = true
		}
		// Resources an Application deploys elsewhere also make a namespace managed.
		resources, _, _ := unstructured.NestedSlice(u.Object, "status", "resources")
		for _, raw := range resources {
			if r, ok := raw.(map[string]any); ok {
				if ns, _ := r["namespace"].(string); ns != "" {
					managed[ns] = true
				}
			}
		}
		chart, _, _ := unstructured.NestedString(u.Object, "spec", "source", "chart")
		rev, _, _ := unstructured.NestedString(u.Object, "spec", "source", "targetRevision")
		repo, _, _ := unstructured.NestedString(u.Object, "spec", "source", "repoURL")
		// Only Git sources of this lab follow a branch; charts pin a version.
		if chart == "" && rev != "" && rev != branch && rev != "HEAD" && strings.Contains(repo, "github.com") && !strings.HasPrefix(rev, "v") && len(rev) != 40 {
			items = append(items, driftItem{"Application", u.GetName(), fmt.Sprintf("suit « %s » au lieu de « %s »", rev, branch), "down"})
		}
	}

	if list, err := c.typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		for _, ns := range list.Items {
			if !managed[ns.Name] && !systemNamespaces[ns.Name] && !ignored[ns.Name] {
				items = append(items, driftItem{"Namespace", ns.Name, "aucune Application Argo CD n'y déploie", "warn"})
			}
		}
	}

	tracked := func(meta metav1.ObjectMeta) bool {
		_, label := meta.Labels["argocd.argoproj.io/instance"]
		_, ann := meta.Annotations["argocd.argoproj.io/tracking-id"]
		return label || ann || len(meta.OwnerReferences) > 0
	}
	flag := func(kind string, meta metav1.ObjectMeta) {
		if systemNamespaces[meta.Namespace] || ignored[meta.Namespace] || !managed[meta.Namespace] || tracked(meta) {
			return
		}
		items = append(items, driftItem{kind, meta.Namespace + "/" + meta.Name, "créé hors d'Argo CD", "warn"})
	}
	if list, err := c.typed.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, w := range list.Items {
			flag("Deployment", w.ObjectMeta)
		}
	}
	if list, err := c.typed.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, w := range list.Items {
			flag("StatefulSet", w.ObjectMeta)
		}
	}
	if list, err := c.typed.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, w := range list.Items {
			flag("DaemonSet", w.ObjectMeta)
		}
	}
	rank := map[string]int{"down": 0, "warn": 1}
	sort.SliceStable(items, func(a, b int) bool { return rank[items[a].State] < rank[items[b].State] })
	return map[string]any{"items": items, "applications": len(apps.Items), "branch": branch}, nil
}

func init() {
	Registry["top"] = Provider{TTL: 30 * time.Second, Fetch: fetchTop}
	Registry["postgres"] = Provider{TTL: time.Minute, Fetch: fetchPostgres}
	Registry["status"] = Provider{TTL: 5 * time.Minute, Fetch: fetchStatus}
	Registry["digest"] = Provider{TTL: 5 * time.Minute, Fetch: fetchDigest}
	Registry["drift"] = Provider{TTL: 2 * time.Minute, Fetch: fetchDrift}
}
