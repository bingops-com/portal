package providers

import (
	"context"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ActivityItem is one dated fact of the lab's recent life.
type ActivityItem struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"` // deploy | alert | backup | restart
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
	State  string    `json:"state"`
}

// fetchActivity merges recent deployments, alerts, backups and container
// restarts into one timeline. Each source is optional: one that cannot be
// read is skipped so the others still show.
func fetchActivity(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context  string   `json:"context"`
		URL      string   `json:"url"`
		CronJobs []string `json:"cronjobs"`
		Hours    int      `json:"hours"`
		Limit    int      `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-time.Duration(clamp(o.Hours, 72, 1, 24*30)) * time.Hour)
	items := []ActivityItem{}
	add := func(t time.Time, kind, title, detail, state string) {
		if t.After(since) {
			items = append(items, ActivityItem{t, kind, title, detail, state})
		}
	}

	if apps, err := c.dynamic.Resource(argoApps).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range apps.Items {
			u := &apps.Items[i]
			history, _, _ := unstructured.NestedSlice(u.Object, "status", "history")
			if len(history) > 3 {
				history = history[len(history)-3:]
			}
			for _, raw := range history {
				h, _ := raw.(map[string]any)
				at, _ := h["deployedAt"].(string)
				t, err := time.Parse(time.RFC3339, at)
				if err != nil {
					continue
				}
				// Multi-source applications record no single revision.
				detail := "déployée"
				if revision, _ := h["revision"].(string); revision != "" {
					if len(revision) == 40 {
						revision = revision[:7]
					}
					detail += " en " + revision
				}
				add(t, "deploy", u.GetName(), detail, "ok")
			}
		}
	}

	if runs, err := c.dynamic.Resource(cnpgBackups).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range runs.Items {
			u := &runs.Items[i]
			cluster, _, _ := unstructured.NestedString(u.Object, "spec", "cluster", "name")
			switch phase, _, _ := unstructured.NestedString(u.Object, "status", "phase"); phase {
			case "completed":
				if t := nestedTime(u, "status", "stoppedAt"); t != nil {
					add(*t, "backup", cluster, "sauvegarde terminée", "ok")
				}
			case "failed":
				add(u.GetCreationTimestamp().Time, "backup", cluster, "sauvegarde en échec", "down")
			}
		}
	}
	for _, ref := range o.CronJobs {
		ns, name, ok := strings.Cut(strings.TrimSpace(ref), "/")
		if !ok {
			continue
		}
		if job, err := c.typed.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{}); err == nil && job.Status.LastSuccessfulTime != nil {
			add(job.Status.LastSuccessfulTime.Time, "backup", name, "sauvegarde terminée", "ok")
		}
	}

	if pods, err := c.typed.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range pods.Items {
			p := &pods.Items[i]
			for _, cs := range p.Status.ContainerStatuses {
				if t := cs.LastTerminationState.Terminated; t != nil && cs.RestartCount > 0 {
					reason, state := t.Reason, "warn"
					if reason == "" {
						reason = "arrêt inattendu"
					}
					// A clean exit followed by a restart is routine, not a fault.
					if t.ExitCode == 0 {
						state = "ok"
					}
					add(t.FinishedAt.Time, "restart", p.Name, "redémarré dans "+p.Namespace+" ("+reason+")", state)
				}
			}
		}
	}

	if o.URL != "" {
		if out, err := fetchAlerts(ctx, d, map[string]any{"url": o.URL}); err == nil {
			for _, a := range out.(alertsResult).Alerts {
				state := "warn"
				if a.Severity == "critical" {
					state = "down"
				} else if a.Severity == "info" {
					state = "unknown"
				}
				detail := a.Summary
				if detail == "" {
					detail = a.Namespace
				}
				add(a.Since, "alert", a.Name, detail, state)
			}
		}
	}

	sort.SliceStable(items, func(a, b int) bool { return items[a].Time.After(items[b].Time) })
	if limit := clamp(o.Limit, 12, 1, 60); len(items) > limit {
		items = items[:limit]
	}
	return map[string]any{"items": items}, nil
}
