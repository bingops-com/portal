package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Detail is the generic content of the side panel opened from a widget row.
type Detail struct {
	Facts    []Fact    `json:"facts,omitempty"`
	Sections []Section `json:"sections,omitempty"`
	Chart    *Chart    `json:"chart,omitempty"`
	URL      string    `json:"url,omitempty"`
}

type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Section struct {
	Title string      `json:"title"`
	Empty string      `json:"empty,omitempty"`
	Rows  []DetailRow `json:"rows"`
}

type DetailRow struct {
	State string     `json:"state,omitempty"`
	Title string     `json:"title"`
	Sub   string     `json:"sub,omitempty"`
	Aside string     `json:"aside,omitempty"`
	Text  string     `json:"text,omitempty"`
	Time  *time.Time `json:"time,omitempty"`
}

type Chart struct {
	Label  string       `json:"label"`
	Format string       `json:"format"`
	Points [][2]float64 `json:"points"` // [unix seconds, value]
}

// Details maps widget types to the loader of their side panel. The query
// carries what the clicked row identifies (never addresses to fetch).
var Details = map[string]func(ctx context.Context, d *Deps, opts map[string]any, q url.Values) (*Detail, error){
	"workloads":  workloadDetail,
	"argocd":     argoDetail,
	"prometheus": prometheusDetail,
}

func workloadDetail(ctx context.Context, d *Deps, opts map[string]any, q url.Values) (*Detail, error) {
	c, o, err := kubeFor(d, opts)
	if err != nil {
		return nil, err
	}
	ns, name, kind := q.Get("namespace"), q.Get("name"), q.Get("kind")
	if ns == "" || name == "" || !o.keep(ns) {
		return nil, errors.New("workload introuvable")
	}
	var selector *metav1.LabelSelector
	var spec corev1.PodSpec
	var ready, desired int32
	var created time.Time
	switch kind {
	case "Deployment":
		w, err := c.typed.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, kubeErr(err)
		}
		selector, spec, ready, created = w.Spec.Selector, w.Spec.Template.Spec, w.Status.ReadyReplicas, w.CreationTimestamp.Time
		desired = 1
		if w.Spec.Replicas != nil {
			desired = *w.Spec.Replicas
		}
	case "StatefulSet":
		w, err := c.typed.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, kubeErr(err)
		}
		selector, spec, ready, created = w.Spec.Selector, w.Spec.Template.Spec, w.Status.ReadyReplicas, w.CreationTimestamp.Time
		desired = 1
		if w.Spec.Replicas != nil {
			desired = *w.Spec.Replicas
		}
	case "DaemonSet":
		w, err := c.typed.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, kubeErr(err)
		}
		selector, spec, ready, desired, created = w.Spec.Selector, w.Spec.Template.Spec, w.Status.NumberReady, w.Status.DesiredNumberScheduled, w.CreationTimestamp.Time
	default:
		return nil, errors.New("type de workload inconnu")
	}

	out := &Detail{Facts: []Fact{
		{"Type", kind}, {"Namespace", ns}, {"Prêts", fmt.Sprintf("%d/%d", ready, desired)},
		{"Image", firstImage(spec)}, {"Créé le", created.Local().Format("02/01/2006 15:04")},
	}}

	pods := Section{Title: "Pods", Empty: "Aucun pod ne correspond à ce workload.", Rows: []DetailRow{}}
	podNames := map[string]bool{}
	if sel, err := metav1.LabelSelectorAsSelector(selector); err == nil && !sel.Empty() {
		list, err := c.typed.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
		if err != nil {
			return nil, kubeErr(err)
		}
		now := time.Now()
		for i := range list.Items {
			p := &list.Items[i]
			podNames[p.Name] = true
			reason, restarts := podIssue(p, now)
			row := DetailRow{State: "ok", Title: p.Name, Sub: p.Spec.NodeName, Text: reason, Time: &p.CreationTimestamp.Time}
			if reason != "" {
				row.State = "warn"
			} else if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodSucceeded {
				row.State, row.Text = "unknown", string(p.Status.Phase)
			}
			if restarts > 0 {
				row.Aside = plural(int(restarts), "redémarrage", "redémarrages")
			}
			pods.Rows = append(pods.Rows, row)
		}
	}
	out.Sections = append(out.Sections, pods)

	events := Section{Title: "Événements récents", Empty: "Aucun événement récent pour ce workload.", Rows: []DetailRow{}}
	if list, err := c.typed.CoreV1().Events(ns).List(ctx, metav1.ListOptions{Limit: 500}); err == nil {
		for _, e := range list.Items {
			obj := e.InvolvedObject.Name
			if obj != name && !podNames[obj] && !strings.HasPrefix(obj, name+"-") {
				continue
			}
			seen := e.LastTimestamp.Time
			if seen.IsZero() {
				seen = e.EventTime.Time
			}
			if seen.IsZero() {
				seen = e.CreationTimestamp.Time
			}
			state := "ok"
			if e.Type == corev1.EventTypeWarning {
				state = "warn"
			}
			events.Rows = append(events.Rows, DetailRow{State: state, Title: e.Reason, Sub: e.InvolvedObject.Kind + "/" + obj, Text: e.Message, Time: &seen})
		}
		sort.SliceStable(events.Rows, func(a, b int) bool { return events.Rows[a].Time.After(*events.Rows[b].Time) })
		if len(events.Rows) > 10 {
			events.Rows = events.Rows[:10]
		}
	}
	out.Sections = append(out.Sections, events)
	return out, nil
}

func argoDetail(ctx context.Context, d *Deps, opts map[string]any, q url.Values) (*Detail, error) {
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
	name := q.Get("name")
	list, err := c.dynamic.Resource(argoApps).Namespace(o.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, kubeErr(err)
	}
	var u *unstructured.Unstructured
	for i := range list.Items {
		if list.Items[i].GetName() == name {
			u = &list.Items[i]
		}
	}
	if u == nil {
		return nil, errors.New("application introuvable")
	}
	str := func(path ...string) string {
		s, _, _ := unstructured.NestedString(u.Object, path...)
		return s
	}
	revision := str("status", "sync", "revision")
	if len(revision) == 40 {
		revision = revision[:7]
	}
	out := &Detail{Facts: []Fact{
		{"Projet", str("spec", "project")}, {"Synchronisation", str("status", "sync", "status")},
		{"Santé", str("status", "health", "status")}, {"Révision", revision},
		{"Namespace cible", str("spec", "destination", "namespace")},
	}}
	if base := trimSlash(o.URL); base != "" {
		out.URL = base + "/applications/" + url.PathEscape(u.GetNamespace()) + "/" + url.PathEscape(name)
	}

	if phase := str("status", "operationState", "phase"); phase != "" && phase != "Succeeded" {
		row := DetailRow{State: "warn", Title: "Opération " + phase, Text: str("status", "operationState", "message")}
		if phase == "Failed" || phase == "Error" {
			row.State = "down"
		}
		row.Time = nestedTime(u, "status", "operationState", "startedAt")
		out.Sections = append(out.Sections, Section{Title: "Dernière synchronisation", Rows: []DetailRow{row}})
	}
	if conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions"); len(conds) > 0 {
		sec := Section{Title: "Conditions", Rows: []DetailRow{}}
		for _, raw := range conds {
			cond, _ := raw.(map[string]any)
			typ, _ := cond["type"].(string)
			msg, _ := cond["message"].(string)
			sec.Rows = append(sec.Rows, DetailRow{State: "warn", Title: typ, Text: msg})
		}
		out.Sections = append(out.Sections, sec)
	}

	resources, _, _ := unstructured.NestedSlice(u.Object, "status", "resources")
	sec := Section{Title: "Ressources à traiter", Empty: fmt.Sprintf("Les %d ressources sont synchronisées et saines.", len(resources)), Rows: []DetailRow{}}
	for _, raw := range resources {
		r, _ := raw.(map[string]any)
		kind, _ := r["kind"].(string)
		rname, _ := r["name"].(string)
		rns, _ := r["namespace"].(string)
		sync, _ := r["status"].(string)
		health, _ := r["health"].(map[string]any)
		hs, _ := health["status"].(string)
		hm, _ := health["message"].(string)
		if sync == "Synced" && (hs == "" || hs == "Healthy") {
			continue
		}
		row := DetailRow{State: "warn", Title: kind + "/" + rname, Sub: rns, Text: hm, Aside: sync}
		if hs != "" && hs != "Healthy" {
			row.Aside = hs
		}
		if hs == "Degraded" || hs == "Missing" {
			row.State = "down"
		}
		sec.Rows = append(sec.Rows, row)
	}
	out.Sections = append(out.Sections, sec)
	return out, nil
}

var chartRanges = map[string]struct {
	span time.Duration
	step int
}{"3h": {3 * time.Hour, 60}, "24h": {24 * time.Hour, 480}, "7d": {7 * 24 * time.Hour, 3600}}

func prometheusDetail(ctx context.Context, _ *Deps, opts map[string]any, q url.Values) (*Detail, error) {
	var o struct {
		URL   string `json:"url"`
		Stats []struct {
			Label  string `json:"label"`
			Query  string `json:"query"`
			Format string `json:"format"`
		} `json:"stats"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	i, err := strconv.Atoi(q.Get("stat"))
	if err != nil || i < 0 || i >= len(o.Stats) || o.URL == "" {
		return nil, errors.New("métrique introuvable")
	}
	r, ok := chartRanges[q.Get("range")]
	if !ok {
		r = chartRanges["24h"]
	}
	now := time.Now()
	var resp promResponse
	params := url.Values{"query": {o.Stats[i].Query}, "start": {strconv.FormatInt(now.Add(-r.span).Unix(), 10)}, "end": {strconv.FormatInt(now.Unix(), 10)}, "step": {strconv.Itoa(r.step)}}
	if err := getJSON(ctx, trimSlash(o.URL)+"/api/v1/query_range?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	chart := &Chart{Label: o.Stats[i].Label, Format: o.Stats[i].Format, Points: [][2]float64{}}
	if len(resp.Data.Result) > 0 {
		for _, p := range resp.Data.Result[0].Values {
			if len(p) != 2 {
				continue
			}
			t, tok := p[0].(float64)
			v, vok := promFloat(p[1])
			if tok && vok {
				chart.Points = append(chart.Points, [2]float64{t, v})
			}
		}
	}
	return &Detail{Chart: chart}, nil
}
