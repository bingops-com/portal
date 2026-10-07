package providers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func nestedTime(u *unstructured.Unstructured, path ...string) *time.Time {
	s, _, _ := unstructured.NestedString(u.Object, path...)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	return nil
}

// absent reports that a custom resource kind is not installed on the cluster.
func absent(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

// --- cert-manager certificates -----------------------------------------

var certificates = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}

type certificate struct {
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	Host      string     `json:"host"`
	Ready     bool       `json:"ready"`
	Reason    string     `json:"reason,omitempty"`
	NotAfter  *time.Time `json:"notAfter,omitempty"`
	State     string     `json:"state"`
}

type certificatesResult struct {
	Certificates []certificate `json:"certificates"`
	Valid        int           `json:"valid"`
	WarnDays     int           `json:"warnDays"`
}

func (r certificatesResult) Summary() SummaryItem {
	it := SummaryItem{Label: "Certificats", State: "ok", Detail: fmt.Sprintf("%d/%s", r.Valid, plural(len(r.Certificates), "valide", "valides"))}
	for _, c := range r.Certificates {
		if c.State == "down" {
			it.State = "down"
			break
		}
		if c.State == "warn" {
			it.State = "warn"
		}
	}
	return it
}

func fetchCertificates(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		kubeOpts
		WarnDays int `json:"warnDays"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	list, err := c.dynamic.Resource(certificates).List(ctx, metav1.ListOptions{})
	if err != nil {
		if absent(err) {
			return nil, errors.New("cert-manager n'est pas installé sur ce cluster")
		}
		return nil, kubeErr(err)
	}
	res := certificatesResult{Certificates: []certificate{}, WarnDays: clamp(o.WarnDays, 14, 1, 90)}
	soon := time.Now().Add(time.Duration(res.WarnDays) * 24 * time.Hour)
	for i := range list.Items {
		u := &list.Items[i]
		if !o.keep(u.GetNamespace()) {
			continue
		}
		cert := certificate{Namespace: u.GetNamespace(), Name: u.GetName(), NotAfter: nestedTime(u, "status", "notAfter"), State: "ok"}
		if names, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "dnsNames"); len(names) > 0 {
			cert.Host = names[0]
		}
		conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
		for _, raw := range conds {
			cond, _ := raw.(map[string]any)
			if cond["type"] == "Ready" {
				cert.Ready = cond["status"] == "True"
				if !cert.Ready {
					cert.Reason, _ = cond["reason"].(string)
				}
			}
		}
		switch {
		case !cert.Ready, cert.NotAfter != nil && cert.NotAfter.Before(time.Now()):
			cert.State = "down"
		case cert.NotAfter != nil && cert.NotAfter.Before(soon):
			cert.State = "warn"
		}
		if cert.State == "ok" {
			res.Valid++
		}
		res.Certificates = append(res.Certificates, cert)
	}
	rank := map[string]int{"down": 0, "warn": 1, "ok": 2}
	sort.SliceStable(res.Certificates, func(a, b int) bool {
		ca, cb := res.Certificates[a], res.Certificates[b]
		if rank[ca.State] != rank[cb.State] {
			return rank[ca.State] < rank[cb.State]
		}
		if ca.NotAfter == nil || cb.NotAfter == nil {
			return ca.NotAfter != nil
		}
		return ca.NotAfter.Before(*cb.NotAfter)
	})
	return res, nil
}

// --- Backups: CloudNativePG and CronJobs --------------------------------

var (
	cnpgClusters = schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}
	cnpgBackups  = schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "backups"}
)

type backup struct {
	Kind        string     `json:"kind"`
	Namespace   string     `json:"namespace"`
	Name        string     `json:"name"`
	LastSuccess *time.Time `json:"lastSuccess,omitempty"`
	// Detail explains a state that is not "ok" in the operator's words.
	Detail string `json:"detail,omitempty"`
	State  string `json:"state"`
}

type backupsResult struct {
	Backups     []backup `json:"backups"`
	Healthy     int      `json:"healthy"`
	MaxAgeHours int      `json:"maxAgeHours"`
}

func (r backupsResult) Summary() SummaryItem {
	it := SummaryItem{Label: "Sauvegardes", State: "ok", Detail: fmt.Sprintf("%d/%s", r.Healthy, plural(len(r.Backups), "à jour", "à jour"))}
	for _, b := range r.Backups {
		if b.State == "down" {
			it.State = "down"
			break
		}
		if b.State == "unknown" {
			it.State = "unknown"
		}
	}
	return it
}

// grade rates a backup source: recent success is ok, a source too young to
// have run once is unknown, anything else is down.
func grade(b *backup, created time.Time, maxAge time.Duration, pending string) {
	now := time.Now()
	switch {
	case b.LastSuccess != nil && now.Sub(*b.LastSuccess) <= maxAge:
		b.State = "ok"
	case b.LastSuccess == nil && now.Sub(created) <= maxAge:
		b.State = "unknown"
		b.Detail = "pas encore exécutée"
	case b.LastSuccess == nil:
		b.State = "down"
		b.Detail = "aucune sauvegarde réussie"
		if pending != "" {
			b.Detail += ", " + pending
		}
	default:
		b.State = "down"
		b.Detail = "dernière réussite trop ancienne"
		if pending != "" {
			b.Detail += ", " + pending
		}
	}
}

func fetchBackups(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context     string   `json:"context"`
		CronJobs    []string `json:"cronjobs"`
		MaxAgeHours int      `json:"maxAgeHours"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	res := backupsResult{Backups: []backup{}, MaxAgeHours: clamp(o.MaxAgeHours, 26, 1, 24*31)}
	maxAge := time.Duration(res.MaxAgeHours) * time.Hour

	clusters, err := c.dynamic.Resource(cnpgClusters).List(ctx, metav1.ListOptions{})
	if err != nil && !absent(err) {
		return nil, kubeErr(err)
	}
	if err == nil && len(clusters.Items) > 0 {
		runs, err := c.dynamic.Resource(cnpgBackups).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, kubeErr(err)
		}
		type latest struct {
			success *time.Time
			started *time.Time
			phase   string
		}
		byCluster := map[string]*latest{}
		for i := range runs.Items {
			u := &runs.Items[i]
			cluster, _, _ := unstructured.NestedString(u.Object, "spec", "cluster", "name")
			key := u.GetNamespace() + "/" + cluster
			l := byCluster[key]
			if l == nil {
				l = &latest{}
				byCluster[key] = l
			}
			phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
			if phase == "completed" {
				if t := nestedTime(u, "status", "stoppedAt"); t != nil && (l.success == nil || t.After(*l.success)) {
					l.success = t
				}
			}
			started := u.GetCreationTimestamp().Time
			if l.started == nil || started.After(*l.started) {
				l.started, l.phase = &started, phase
			}
		}
		for i := range clusters.Items {
			u := &clusters.Items[i]
			b := backup{Kind: "PostgreSQL", Namespace: u.GetNamespace(), Name: u.GetName()}
			pending := ""
			if l := byCluster[b.Namespace+"/"+b.Name]; l != nil {
				b.LastSuccess = l.success
				if l.phase != "" && l.phase != "completed" {
					pending = fmt.Sprintf("dernière tentative « %s »", l.phase)
				}
			}
			grade(&b, u.GetCreationTimestamp().Time, maxAge, pending)
			res.Backups = append(res.Backups, b)
		}
	}

	for _, ref := range o.CronJobs {
		ns, name, ok := strings.Cut(strings.TrimSpace(ref), "/")
		if !ok || ns == "" || name == "" {
			continue
		}
		b := backup{Kind: "CronJob", Namespace: ns, Name: name}
		job, err := c.typed.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			b.State, b.Detail = "down", "CronJob introuvable"
		case err != nil:
			return nil, kubeErr(err)
		default:
			if job.Status.LastSuccessfulTime != nil {
				b.LastSuccess = &job.Status.LastSuccessfulTime.Time
			}
			pending := ""
			if job.Spec.Suspend != nil && *job.Spec.Suspend {
				pending = "planification suspendue"
			}
			grade(&b, job.CreationTimestamp.Time, maxAge, pending)
		}
		res.Backups = append(res.Backups, b)
	}

	if len(res.Backups) == 0 {
		return nil, errors.New("aucune sauvegarde à suivre : pas de cluster CloudNativePG, et aucun CronJob indiqué dans les réglages")
	}
	for _, b := range res.Backups {
		if b.State == "ok" {
			res.Healthy++
		}
	}
	rank := map[string]int{"down": 0, "unknown": 1, "ok": 2}
	sort.SliceStable(res.Backups, func(a, b int) bool { return rank[res.Backups[a].State] < rank[res.Backups[b].State] })
	return res, nil
}
