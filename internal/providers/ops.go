package providers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// --- Kubernetes clients ------------------------------------------------

type kubeClient struct {
	name    string
	typed   kubernetes.Interface
	dynamic dynamic.Interface
}

// KubeClients lazily builds one client per kubeconfig context. An empty
// context means the pod's ServiceAccount, or the current kubeconfig context
// when running outside a cluster.
type KubeClients struct {
	mu      sync.Mutex
	clients map[string]*kubeClient
}

func NewKubeClients() *KubeClients {
	return &KubeClients{clients: map[string]*kubeClient{}}
}

func (k *KubeClients) get(contextName string) (*kubeClient, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if c, ok := k.clients[contextName]; ok {
		return c, nil
	}
	name := contextName
	var cfg *rest.Config
	var err error
	if contextName == "" {
		if cfg, err = rest.InClusterConfig(); err == nil {
			if name = os.Getenv("PORTAL_CLUSTER_NAME"); name == "" {
				name = "cluster"
			}
		}
	}
	if cfg == nil {
		loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{CurrentContext: contextName},
		)
		if cfg, err = loader.ClientConfig(); err != nil {
			return nil, fmt.Errorf("accès Kubernetes indisponible: %w", err)
		}
		if name == "" {
			if raw, rerr := loader.RawConfig(); rerr == nil {
				name = raw.CurrentContext
			}
		}
	}
	cfg.Timeout = 10 * time.Second
	cfg.UserAgent = userAgent
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	c := &kubeClient{name: name, typed: typed, dynamic: dyn}
	k.clients[contextName] = c
	return c, nil
}

type kubeOpts struct {
	Context    string   `json:"context"`
	Namespaces []string `json:"namespaces"`
	Exclude    []string `json:"exclude"`
	Limit      int      `json:"limit"`
}

func (o kubeOpts) keep(ns string) bool {
	for _, e := range o.Exclude {
		if e == ns {
			return false
		}
	}
	if len(o.Namespaces) == 0 {
		return true
	}
	for _, n := range o.Namespaces {
		if n == ns {
			return true
		}
	}
	return false
}

func kubeFor(d *Deps, opts map[string]any) (*kubeClient, kubeOpts, error) {
	var o kubeOpts
	if err := decode(opts, &o); err != nil {
		return nil, o, err
	}
	c, err := d.Kube.get(o.Context)
	return c, o, err
}

func kubeErr(err error) error {
	return fmt.Errorf("API Kubernetes: %s", strings.TrimSpace(err.Error()))
}

// --- Cluster overview --------------------------------------------------

type nodeInfo struct {
	Name    string    `json:"name"`
	Ready   bool      `json:"ready"`
	Roles   []string  `json:"roles"`
	Version string    `json:"version"`
	OS      string    `json:"os"`
	Created time.Time `json:"created"`
}

type podProblem struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Reason    string `json:"reason"`
	Restarts  int32  `json:"restarts"`
}

type clusterResult struct {
	Name       string         `json:"name"`
	Version    string         `json:"version"`
	Nodes      []nodeInfo     `json:"nodes"`
	NodesReady int            `json:"nodesReady"`
	Namespaces int            `json:"namespaces"`
	Pods       map[string]int `json:"pods"`
	PodsTotal  int            `json:"podsTotal"`
	Problems   []podProblem   `json:"problems"`
}

func (r clusterResult) Summary() SummaryItem {
	it := SummaryItem{Label: "Cluster " + r.Name, State: "ok",
		Detail: fmt.Sprintf("%d/%s, %s", r.NodesReady, plural(len(r.Nodes), "nœud prêt", "nœuds prêts"), plural(r.Pods["Running"], "pod actif", "pods actifs"))}
	if len(r.Problems) > 0 {
		it.State = "warn"
		it.Detail = plural(len(r.Problems), "pod en difficulté", "pods en difficulté")
	}
	if r.NodesReady < len(r.Nodes) {
		it.State = "down"
		it.Detail = fmt.Sprintf("%d/%d nœuds prêts", r.NodesReady, len(r.Nodes))
	}
	return it
}

func podIssue(p *corev1.Pod, now time.Time) (string, int32) {
	var restarts int32
	reason := ""
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		restarts += cs.RestartCount
		if w := cs.State.Waiting; w != nil && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" && reason == "" {
			reason = w.Reason
		}
		if t := cs.State.Terminated; t != nil && t.ExitCode != 0 && reason == "" {
			reason = t.Reason
		}
	}
	if p.Status.Phase == corev1.PodSucceeded {
		return "", restarts
	}
	if reason != "" {
		return reason, restarts
	}
	old := now.Sub(p.CreationTimestamp.Time) > 5*time.Minute
	switch p.Status.Phase {
	case corev1.PodFailed:
		if p.Status.Reason != "" {
			return p.Status.Reason, restarts
		}
		return "Failed", restarts
	case corev1.PodPending:
		if old {
			return "Pending", restarts
		}
	case corev1.PodRunning:
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status != corev1.ConditionTrue && old && p.DeletionTimestamp == nil {
				return "NotReady", restarts
			}
		}
	}
	return "", restarts
}

func fetchCluster(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	c, _, err := kubeFor(d, opts)
	if err != nil {
		return nil, err
	}
	res := clusterResult{Name: c.name, Nodes: []nodeInfo{}, Pods: map[string]int{}, Problems: []podProblem{}}

	nodes, err := c.typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, kubeErr(err)
	}
	for _, n := range nodes.Items {
		info := nodeInfo{Name: n.Name, Version: n.Status.NodeInfo.KubeletVersion, OS: n.Status.NodeInfo.OSImage, Created: n.CreationTimestamp.Time, Roles: []string{}}
		for label := range n.Labels {
			if role, ok := strings.CutPrefix(label, "node-role.kubernetes.io/"); ok && role != "" {
				info.Roles = append(info.Roles, role)
			}
		}
		sort.Strings(info.Roles)
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				info.Ready = cond.Status == corev1.ConditionTrue
			}
		}
		if info.Ready {
			res.NodesReady++
		}
		res.Nodes = append(res.Nodes, info)
		if res.Version == "" {
			res.Version = info.Version
		}
	}

	if ns, err := c.typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		res.Namespaces = len(ns.Items)
	}

	pods, err := c.typed.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, kubeErr(err)
	}
	now := time.Now()
	for i := range pods.Items {
		p := &pods.Items[i]
		res.Pods[string(p.Status.Phase)]++
		res.PodsTotal++
		if reason, restarts := podIssue(p, now); reason != "" {
			res.Problems = append(res.Problems, podProblem{p.Namespace, p.Name, reason, restarts})
		}
	}
	return res, nil
}

// --- Workloads ---------------------------------------------------------

type workload struct {
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Ready     int32     `json:"ready"`
	Desired   int32     `json:"desired"`
	Healthy   bool      `json:"healthy"`
	Image     string    `json:"image"`
	Created   time.Time `json:"created"`
}

func firstImage(spec corev1.PodSpec) string {
	if len(spec.Containers) == 0 {
		return ""
	}
	img := spec.Containers[0].Image
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	if i := strings.Index(img, "@"); i >= 0 {
		img = img[:i]
	}
	return img
}

func fetchWorkloads(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	c, o, err := kubeFor(d, opts)
	if err != nil {
		return nil, err
	}
	items := []workload{}
	add := func(kind string, meta metav1.ObjectMeta, ready, desired int32, spec corev1.PodSpec) {
		if o.keep(meta.Namespace) {
			items = append(items, workload{kind, meta.Namespace, meta.Name, ready, desired, ready >= desired, firstImage(spec), meta.CreationTimestamp.Time})
		}
	}
	var deps *appsv1.DeploymentList
	if deps, err = c.typed.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err != nil {
		return nil, kubeErr(err)
	}
	for _, w := range deps.Items {
		desired := int32(1)
		if w.Spec.Replicas != nil {
			desired = *w.Spec.Replicas
		}
		add("Deployment", w.ObjectMeta, w.Status.ReadyReplicas, desired, w.Spec.Template.Spec)
	}
	if sts, err := c.typed.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, w := range sts.Items {
			desired := int32(1)
			if w.Spec.Replicas != nil {
				desired = *w.Spec.Replicas
			}
			add("StatefulSet", w.ObjectMeta, w.Status.ReadyReplicas, desired, w.Spec.Template.Spec)
		}
	}
	if dss, err := c.typed.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, w := range dss.Items {
			add("DaemonSet", w.ObjectMeta, w.Status.NumberReady, w.Status.DesiredNumberScheduled, w.Spec.Template.Spec)
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].Healthy != items[b].Healthy {
			return !items[a].Healthy
		}
		if items[a].Namespace != items[b].Namespace {
			return items[a].Namespace < items[b].Namespace
		}
		return items[a].Name < items[b].Name
	})
	healthy := 0
	for _, w := range items {
		if w.Healthy {
			healthy++
		}
	}
	return map[string]any{"cluster": c.name, "workloads": items, "healthy": healthy}, nil
}

// --- Events ------------------------------------------------------------

type event struct {
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	Object    string    `json:"object"`
	Namespace string    `json:"namespace"`
	Count     int32     `json:"count"`
	LastSeen  time.Time `json:"lastSeen"`
}

func fetchEvents(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	c, o, err := kubeFor(d, opts)
	if err != nil {
		return nil, err
	}
	list, err := c.typed.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 500})
	if err != nil {
		return nil, kubeErr(err)
	}
	events := []event{}
	for _, e := range list.Items {
		if !o.keep(e.Namespace) {
			continue
		}
		seen := e.LastTimestamp.Time
		if seen.IsZero() {
			seen = e.EventTime.Time
		}
		if seen.IsZero() {
			seen = e.CreationTimestamp.Time
		}
		count := e.Count
		if e.Series != nil && e.Series.Count > count {
			count = e.Series.Count
		}
		events = append(events, event{e.Reason, e.Message, e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name, e.Namespace, count, seen})
	}
	sort.SliceStable(events, func(a, b int) bool { return events[a].LastSeen.After(events[b].LastSeen) })
	if limit := clamp(o.Limit, 8, 1, 50); len(events) > limit {
		events = events[:limit]
	}
	return map[string]any{"events": events}, nil
}

// --- Argo CD (Application resources read through the Kubernetes API) ----

var argoApps = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

type argoApp struct {
	Name     string     `json:"name"`
	Project  string     `json:"project"`
	Sync     string     `json:"sync"`
	Health   string     `json:"health"`
	Revision string     `json:"revision"`
	Path     string     `json:"path"`
	SyncedAt *time.Time `json:"syncedAt,omitempty"`
	URL      string     `json:"url,omitempty"`
}

type argoResult struct {
	Apps    []argoApp `json:"apps"`
	Healthy int       `json:"healthy"`
	URL     string    `json:"url,omitempty"`
}

func (r argoResult) Summary() SummaryItem {
	it := SummaryItem{Label: "Argo CD", State: "ok", Detail: fmt.Sprintf("%d/%s", r.Healthy, plural(len(r.Apps), "application à jour", "applications à jour"))}
	for _, a := range r.Apps {
		if a.Health == "Degraded" || a.Health == "Missing" {
			it.State = "down"
			break
		}
		if a.Sync != "Synced" || a.Health != "Healthy" {
			it.State = "warn"
		}
	}
	return it
}

func fetchArgo(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context   string `json:"context"`
		Namespace string `json:"namespace"`
		URL       string `json:"url"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	list, err := c.dynamic.Resource(argoApps).Namespace(o.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, kubeErr(err)
	}
	res := argoResult{Apps: []argoApp{}, URL: trimSlash(o.URL)}
	str := func(u *unstructured.Unstructured, path ...string) string {
		s, _, _ := unstructured.NestedString(u.Object, path...)
		return s
	}
	for i := range list.Items {
		u := &list.Items[i]
		a := argoApp{
			Name: u.GetName(), Project: str(u, "spec", "project"),
			Sync: str(u, "status", "sync", "status"), Health: str(u, "status", "health", "status"),
			Revision: str(u, "status", "sync", "revision"), Path: str(u, "spec", "source", "path"),
		}
		if a.Path == "" {
			a.Path = str(u, "spec", "source", "chart")
		}
		if len(a.Revision) == 40 {
			a.Revision = a.Revision[:7]
		}
		if a.Sync == "" {
			a.Sync = "Unknown"
		}
		if a.Health == "" {
			a.Health = "Unknown"
		}
		if t, err := time.Parse(time.RFC3339, str(u, "status", "operationState", "finishedAt")); err == nil {
			a.SyncedAt = &t
		}
		if res.URL != "" {
			a.URL = res.URL + "/applications/" + url.PathEscape(u.GetNamespace()) + "/" + url.PathEscape(a.Name)
		}
		if a.Sync == "Synced" && a.Health == "Healthy" {
			res.Healthy++
		}
		res.Apps = append(res.Apps, a)
	}
	sort.SliceStable(res.Apps, func(a, b int) bool {
		ha := res.Apps[a].Sync == "Synced" && res.Apps[a].Health == "Healthy"
		hb := res.Apps[b].Sync == "Synced" && res.Apps[b].Health == "Healthy"
		if ha != hb {
			return !ha
		}
		return res.Apps[a].Name < res.Apps[b].Name
	})
	return res, nil
}

// --- Gatus -------------------------------------------------------------

type gatusCheck struct {
	OK     bool      `json:"ok"`
	Millis float64   `json:"ms"`
	Time   time.Time `json:"t"`
}

type gatusEndpoint struct {
	Name    string       `json:"name"`
	Group   string       `json:"group"`
	Up      bool         `json:"up"`
	Uptime  float64      `json:"uptime"`
	Millis  float64      `json:"ms"`
	Results []gatusCheck `json:"results"`
	URL     string       `json:"url,omitempty"`
}

type gatusResult struct {
	Endpoints []gatusEndpoint `json:"endpoints"`
	Up        int             `json:"up"`
	URL       string          `json:"url,omitempty"`
}

func (r gatusResult) Summary() SummaryItem {
	it := SummaryItem{Label: "Endpoints", State: "ok", Detail: fmt.Sprintf("%d/%d en ligne", r.Up, len(r.Endpoints))}
	if r.Up < len(r.Endpoints) {
		it.State = "down"
	}
	return it
}

func fetchGatus(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		URL       string `json:"url"`
		PublicURL string `json:"publicUrl"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if o.URL == "" {
		return nil, errors.New("indiquez l'adresse de Gatus dans les réglages du widget")
	}
	var raw []struct {
		Name    string `json:"name"`
		Group   string `json:"group"`
		Key     string `json:"key"`
		Results []struct {
			Success   bool      `json:"success"`
			Duration  int64     `json:"duration"`
			Timestamp time.Time `json:"timestamp"`
		} `json:"results"`
	}
	if err := getJSON(ctx, trimSlash(o.URL)+"/api/v1/endpoints/statuses", &raw); err != nil {
		return nil, err
	}
	public := trimSlash(o.PublicURL)
	res := gatusResult{Endpoints: []gatusEndpoint{}, URL: public}
	for _, r := range raw {
		ep := gatusEndpoint{Name: r.Name, Group: r.Group, Results: []gatusCheck{}}
		if public != "" {
			ep.URL = public + "/endpoints/" + url.PathEscape(r.Key)
		}
		results := r.Results
		if len(results) > 40 {
			results = results[len(results)-40:]
		}
		okCount := 0
		for _, c := range results {
			if c.Success {
				okCount++
			}
			ep.Results = append(ep.Results, gatusCheck{c.Success, float64(c.Duration) / 1e6, c.Timestamp})
		}
		if n := len(results); n > 0 {
			ep.Up = results[n-1].Success
			ep.Millis = float64(results[n-1].Duration) / 1e6
			ep.Uptime = 100 * float64(okCount) / float64(n)
		}
		if ep.Up {
			res.Up++
		}
		res.Endpoints = append(res.Endpoints, ep)
	}
	sort.SliceStable(res.Endpoints, func(a, b int) bool {
		if res.Endpoints[a].Group != res.Endpoints[b].Group {
			return res.Endpoints[a].Group < res.Endpoints[b].Group
		}
		return res.Endpoints[a].Name < res.Endpoints[b].Name
	})
	return res, nil
}

// --- Prometheus stats and alerts ---------------------------------------

type promStat struct {
	Label  string    `json:"label"`
	Value  *float64  `json:"value"`
	Format string    `json:"format"`
	State  string    `json:"state"`
	Series []float64 `json:"series,omitempty"`
	Error  string    `json:"error,omitempty"`
}

type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		Result []struct {
			Value  []any   `json:"value"`
			Values [][]any `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func promFloat(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func fetchPrometheus(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		URL   string `json:"url"`
		Stats []struct {
			Label     string   `json:"label"`
			Query     string   `json:"query"`
			Format    string   `json:"format"`
			Warn      *float64 `json:"warn"`
			Danger    *float64 `json:"danger"`
			Sparkline *bool    `json:"sparkline"`
		} `json:"stats"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if o.URL == "" {
		return nil, errors.New("indiquez l'adresse de Prometheus dans les réglages du widget")
	}
	if len(o.Stats) == 0 {
		return nil, errors.New("ajoutez au moins une requête PromQL")
	}
	if len(o.Stats) > 24 {
		o.Stats = o.Stats[:24]
	}
	base := trimSlash(o.URL)
	stats := make([]promStat, len(o.Stats))
	errs := make([]error, len(o.Stats))
	now := time.Now()
	parallel(len(o.Stats), 6, func(i int) {
		s := o.Stats[i]
		st := promStat{Label: s.Label, Format: s.Format, State: "none"}
		defer func() { stats[i] = st }()
		var resp promResponse
		if err := getJSON(ctx, base+"/api/v1/query?"+url.Values{"query": {s.Query}}.Encode(), &resp); err != nil {
			errs[i] = err
			st.Error = "requête refusée"
			return
		}
		if len(resp.Data.Result) == 0 || len(resp.Data.Result[0].Value) < 2 {
			st.Error = "aucune donnée"
			return
		}
		v, ok := promFloat(resp.Data.Result[0].Value[1])
		if !ok {
			st.Error = "aucune donnée"
			return
		}
		st.Value = &v
		switch {
		case s.Danger != nil && v >= *s.Danger:
			st.State = "down"
		case s.Warn != nil && v >= *s.Warn:
			st.State = "warn"
		case s.Warn != nil || s.Danger != nil:
			st.State = "ok"
		}
		if s.Sparkline != nil && !*s.Sparkline {
			return
		}
		var rng promResponse
		q := url.Values{"query": {s.Query}, "start": {strconv.FormatInt(now.Add(-3*time.Hour).Unix(), 10)}, "end": {strconv.FormatInt(now.Unix(), 10)}, "step": {"300"}}
		if getJSON(ctx, base+"/api/v1/query_range?"+q.Encode(), &rng) == nil && len(rng.Data.Result) > 0 {
			for _, p := range rng.Data.Result[0].Values {
				if len(p) == 2 {
					if f, ok := promFloat(p[1]); ok {
						st.Series = append(st.Series, f)
					}
				}
			}
		}
	})
	failed := 0
	for _, e := range errs {
		if e != nil {
			failed++
		}
	}
	if failed == len(stats) {
		return nil, errs[0]
	}
	return map[string]any{"stats": stats}, nil
}

type alert struct {
	Name      string    `json:"name"`
	Severity  string    `json:"severity"`
	Namespace string    `json:"namespace,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Since     time.Time `json:"since"`
}

type alertsResult struct {
	Alerts []alert `json:"alerts"`
}

func (r alertsResult) Summary() SummaryItem {
	if len(r.Alerts) == 0 {
		return SummaryItem{Label: "Alertes", State: "ok", Detail: "aucune alerte active"}
	}
	it := SummaryItem{Label: "Alertes", State: "warn", Detail: plural(len(r.Alerts), "alerte active", "alertes actives")}
	for _, a := range r.Alerts {
		if a.Severity == "critical" {
			it.State = "down"
		}
	}
	return it
}

var severityRank = map[string]int{"critical": 0, "warning": 1, "info": 2}

func fetchAlerts(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		URL    string   `json:"url"`
		Ignore []string `json:"ignore"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if o.URL == "" {
		return nil, errors.New("indiquez l'adresse de Prometheus dans les réglages du widget")
	}
	ignore := map[string]bool{"Watchdog": true, "InfoInhibitor": true}
	for _, n := range o.Ignore {
		ignore[n] = true
	}
	var raw struct {
		Data struct {
			Alerts []struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
				State       string            `json:"state"`
				ActiveAt    time.Time         `json:"activeAt"`
			} `json:"alerts"`
		} `json:"data"`
	}
	if err := getJSON(ctx, trimSlash(o.URL)+"/api/v1/alerts", &raw); err != nil {
		return nil, err
	}
	res := alertsResult{Alerts: []alert{}}
	for _, a := range raw.Data.Alerts {
		name := a.Labels["alertname"]
		if a.State != "firing" || ignore[name] {
			continue
		}
		summary := a.Annotations["summary"]
		if summary == "" {
			summary = a.Annotations["description"]
		}
		res.Alerts = append(res.Alerts, alert{name, a.Labels["severity"], a.Labels["namespace"], summary, a.ActiveAt})
	}
	sort.SliceStable(res.Alerts, func(a, b int) bool {
		ra, oka := severityRank[res.Alerts[a].Severity]
		rb, okb := severityRank[res.Alerts[b].Severity]
		if !oka {
			ra = 3
		}
		if !okb {
			rb = 3
		}
		if ra != rb {
			return ra < rb
		}
		return res.Alerts[a].Since.After(res.Alerts[b].Since)
	})
	return res, nil
}
