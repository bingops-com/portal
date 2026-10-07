package providers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// --- Deadlines: everything that expires, on one countdown ----------------

type deadline struct {
	Title string    `json:"title"`
	Kind  string    `json:"kind"` // certificate | eol | manual
	Note  string    `json:"note,omitempty"`
	Date  time.Time `json:"date"`
	Days  int       `json:"days"`
	State string    `json:"state"`
}

type deadlinesResult struct {
	Items  []deadline `json:"items"`
	Failed []string   `json:"failed,omitempty"`
}

func (r deadlinesResult) Summary() SummaryItem {
	it := SummaryItem{Label: "Échéances", State: "ok", Detail: "aucune échéance suivie"}
	if len(r.Items) == 0 {
		return it
	}
	next := r.Items[0]
	it.State = next.State
	switch {
	case next.Days < 0:
		it.Detail = next.Title + " : dépassée"
	case next.Days == 0:
		it.Detail = next.Title + " : aujourd'hui"
	default:
		it.Detail = fmt.Sprintf("%s dans %d j", next.Title, next.Days)
	}
	return it
}

var (
	eolProductRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	eolCycleRe   = regexp.MustCompile(`^[0-9a-z.]{1,20}$`)
	minorRe      = regexp.MustCompile(`^v?(\d+\.\d+)`)
)

func fetchDeadlines(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context      string `json:"context"`
		Certificates *bool  `json:"certificates"`
		WarnDays     int    `json:"warnDays"`
		Items        []struct {
			Title string `json:"title"`
			Date  string `json:"date"`
			Note  string `json:"note"`
		} `json:"items"`
		EOL []struct {
			Title   string `json:"title"`
			Product string `json:"product"`
			Cycle   string `json:"cycle"`
		} `json:"eol"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	warn := clamp(o.WarnDays, 30, 1, 365)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	res := deadlinesResult{Items: []deadline{}}
	add := func(title, kind, note string, date time.Time) {
		days := int(math.Floor(date.Sub(today).Hours() / 24))
		state := "ok"
		switch {
		case days <= 7:
			state = "down"
		case days <= warn:
			state = "warn"
		}
		res.Items = append(res.Items, deadline{title, kind, note, date, days, state})
	}

	for _, it := range o.Items {
		date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(it.Date), now.Location())
		if err != nil || strings.TrimSpace(it.Title) == "" {
			res.Failed = append(res.Failed, fmt.Sprintf("« %s » : date attendue au format AAAA-MM-JJ", it.Title))
			continue
		}
		add(it.Title, "manual", it.Note, date)
	}

	needKube := (o.Certificates == nil || *o.Certificates) || len(o.EOL) > 0
	var kube *kubeClient
	if needKube {
		kube, _ = d.Kube.get(o.Context)
	}
	if kube != nil && (o.Certificates == nil || *o.Certificates) {
		if list, err := kube.dynamic.Resource(certificates).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range list.Items {
				u := &list.Items[i]
				if t := nestedTime(u, "status", "notAfter"); t != nil {
					title := u.GetName()
					if names, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "dnsNames"); len(names) > 0 {
						title = names[0]
					}
					// cert-manager renews by itself; the date matters when it fails to.
					add(title, "certificate", "certificat, renouvellement automatique", *t)
				}
			}
		}
	}

	type eolDate struct {
		date time.Time
		err  string
	}
	found := make([]eolDate, len(o.EOL))
	clusterMinor := ""
	if kube != nil {
		if nodes, err := kube.typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1}); err == nil && len(nodes.Items) > 0 {
			if m := minorRe.FindStringSubmatch(nodes.Items[0].Status.NodeInfo.KubeletVersion); m != nil {
				clusterMinor = m[1]
			}
		}
	}
	parallel(len(o.EOL), 4, func(i int) {
		e := o.EOL[i]
		cycle := strings.TrimSpace(e.Cycle)
		if cycle == "" && e.Product == "kubernetes" {
			cycle = clusterMinor
		}
		if !eolProductRe.MatchString(e.Product) || !eolCycleRe.MatchString(cycle) {
			found[i].err = fmt.Sprintf("fin de support de « %s » : produit ou version invalide", e.Product)
			return
		}
		var raw struct {
			EOL any `json:"eol"`
		}
		if err := getJSON(ctx, "https://endoflife.date/api/"+e.Product+"/"+url.PathEscape(cycle)+".json", &raw); err != nil {
			found[i].err = fmt.Sprintf("fin de support de %s %s inconnue d'endoflife.date", e.Product, cycle)
			return
		}
		s, _ := raw.EOL.(string)
		date, err := time.ParseInLocation("2006-01-02", s, now.Location())
		if err != nil {
			found[i].err = fmt.Sprintf("%s %s n'a pas de date de fin de support annoncée", e.Product, cycle)
			return
		}
		found[i].date = date
		o.EOL[i].Cycle = cycle
	})
	for i, f := range found {
		if f.err != "" {
			res.Failed = append(res.Failed, f.err)
			continue
		}
		title := o.EOL[i].Title
		if title == "" {
			title = o.EOL[i].Product + " " + o.EOL[i].Cycle
		}
		add(title, "eol", "fin de support", f.date)
	}

	sort.SliceStable(res.Items, func(a, b int) bool { return res.Items[a].Date.Before(res.Items[b].Date) })
	if len(res.Items) == 0 && len(res.Failed) > 0 {
		return nil, errors.New(res.Failed[0])
	}
	return res, nil
}

// --- Version lag: what runs here against what is published ----------------

type versionLag struct {
	Name    string `json:"name"`
	Running string `json:"running"`
	Latest  string `json:"latest"`
	Lag     string `json:"lag"`
	State   string `json:"state"`
	URL     string `json:"url,omitempty"`
}

var (
	numbersRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)
	osVerRe   = regexp.MustCompile(`\(?v?(\d+\.\d+\.\d+)\)?`)
)

func triple(v string) ([3]int, bool) {
	m := numbersRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// lagOf describes how far running is behind latest.
func lagOf(running, latest string) (string, string) {
	r, ok1 := triple(running)
	l, ok2 := triple(latest)
	switch {
	case !ok1 || !ok2:
		return "comparaison impossible", "unknown"
	case l[0] > r[0]:
		return plural(l[0]-r[0], "version majeure de retard", "versions majeures de retard"), "warn"
	case l[0] == r[0] && l[1] > r[1]:
		state := "none"
		if l[1]-r[1] >= 3 {
			state = "warn"
		}
		return plural(l[1]-r[1], "version mineure de retard", "versions mineures de retard"), state
	case l[0] == r[0] && l[1] == r[1] && l[2] > r[2]:
		return "correctifs disponibles", "none"
	}
	return "à jour", "ok"
}

func imageTag(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		return image[i+1:]
	}
	return ""
}

func fetchVersions(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context string `json:"context"`
		Token   string `json:"token"`
		Items   []struct {
			Name   string `json:"name"`
			Repo   string `json:"repo"`
			Source string `json:"source"`
		} `json:"items"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Items) == 0 {
		return nil, errors.New("ajoutez au moins un composant à suivre dans les réglages du widget")
	}
	if len(o.Items) > 30 {
		o.Items = o.Items[:30]
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	nodes, nodeErr := c.typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})

	rows := make([]versionLag, len(o.Items))
	parallel(len(o.Items), 4, func(i int) {
		it := o.Items[i]
		row := versionLag{Name: it.Name, State: "unknown"}
		defer func() { rows[i] = row }()
		if row.Name == "" {
			row.Name = it.Repo
		}

		switch src := strings.TrimSpace(it.Source); {
		case src == "node:kubelet" || src == "node:os":
			if nodeErr != nil || len(nodes.Items) == 0 {
				row.Lag = "nœud illisible"
				return
			}
			info := nodes.Items[0].Status.NodeInfo
			if src == "node:kubelet" {
				row.Running = info.KubeletVersion
			} else if m := osVerRe.FindStringSubmatch(info.OSImage); m != nil {
				row.Running = "v" + m[1]
			}
		default:
			ns, name, ok := strings.Cut(src, "/")
			if !ok {
				row.Lag = "source attendue : namespace/nom, node:kubelet ou node:os"
				return
			}
			if w, err := c.typed.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{}); err == nil && len(w.Spec.Template.Spec.Containers) > 0 {
				row.Running = imageTag(w.Spec.Template.Spec.Containers[0].Image)
			} else if w, err := c.typed.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{}); err == nil && len(w.Spec.Template.Spec.Containers) > 0 {
				row.Running = imageTag(w.Spec.Template.Spec.Containers[0].Image)
			} else if w, err := c.typed.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{}); err == nil && len(w.Spec.Template.Spec.Containers) > 0 {
				row.Running = imageTag(w.Spec.Template.Spec.Containers[0].Image)
			}
		}
		if row.Running == "" {
			row.Lag = "version en service introuvable"
			return
		}
		if !repoRe.MatchString(strings.TrimSpace(it.Repo)) {
			row.Lag = "dépôt attendu au format propriétaire/nom"
			return
		}
		var raw struct {
			Tag string `json:"tag_name"`
			URL string `json:"html_url"`
		}
		if err := getJSON(ctx, "https://api.github.com/repos/"+strings.TrimSpace(it.Repo)+"/releases/latest", &raw, githubHeaders(o.Token)...); err != nil {
			row.Lag = "dernière version illisible sur GitHub"
			return
		}
		// Some projects prefix their tags ("version/2026.8.3").
		row.Latest, row.URL = raw.Tag[strings.LastIndex(raw.Tag, "/")+1:], raw.URL
		row.Lag, row.State = lagOf(row.Running, row.Latest)
	})
	rank := map[string]int{"warn": 0, "none": 1, "unknown": 2, "ok": 3}
	sort.SliceStable(rows, func(a, b int) bool { return rank[rows[a].State] < rank[rows[b].State] })
	current := 0
	for _, r := range rows {
		if r.State == "ok" {
			current++
		}
	}
	return map[string]any{"items": rows, "current": current}, nil
}

// --- Saturation forecast: when each disk fills up at the current pace -----

type forecastRow struct {
	Name  string   `json:"name"`
	Used  float64  `json:"used"` // percent
	Free  float64  `json:"free"` // bytes
	Days  *float64 `json:"days"` // nil when not filling
	State string   `json:"state"`
}

type promVector struct {
	Data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func promMap(ctx context.Context, base, query string) (map[string]float64, error) {
	var resp promVector
	if err := getJSON(ctx, base+"/api/v1/query?"+url.Values{"query": {query}}.Encode(), &resp); err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, r := range resp.Data.Result {
		if len(r.Value) == 2 {
			if v, ok := promFloat(r.Value[1]); ok {
				out[r.Metric["instance"]+"|"+r.Metric["mountpoint"]] = v
			}
		}
	}
	return out, nil
}

func fetchForecast(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		URL         string   `json:"url"`
		Hours       int      `json:"hours"`
		Mountpoints []string `json:"mountpoints"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if o.URL == "" {
		return nil, errors.New("indiquez l'adresse de Prometheus dans les réglages du widget")
	}
	base := trimSlash(o.URL)
	// Three days smooth out a busy day (an install, a log burst) that a
	// 24-hour slope would extrapolate into a false alarm.
	hours := clamp(o.Hours, 72, 1, 24*14)
	const sel = `{fstype!~"tmpfs|overlay|squashfs|ramfs|vfat|"}`
	size, err := promMap(ctx, base, "node_filesystem_size_bytes"+sel)
	if err != nil {
		return nil, err
	}
	free, err := promMap(ctx, base, "node_filesystem_avail_bytes"+sel)
	if err != nil {
		return nil, err
	}
	slope, _ := promMap(ctx, base, fmt.Sprintf("deriv(node_filesystem_avail_bytes%s[%dh])", sel, hours))

	wanted := map[string]bool{}
	for _, m := range o.Mountpoints {
		wanted[strings.TrimSpace(m)] = true
	}
	instances := map[string]bool{}
	for key := range size {
		instances[strings.SplitN(key, "|", 2)[0]] = true
	}
	rows := []forecastRow{}
	for key, total := range size {
		parts := strings.SplitN(key, "|", 2)
		mount := parts[1]
		avail, ok := free[key]
		// Small or pseudo filesystems (files bind-mounted from the root) are noise.
		pseudo := strings.HasPrefix(mount, "/etc/") || strings.HasPrefix(mount, "/run") || strings.HasPrefix(mount, "/sys") || strings.HasPrefix(mount, "/proc")
		if !ok || total < 2<<30 || (len(wanted) == 0 && pseudo) || (len(wanted) > 0 && !wanted[mount]) {
			continue
		}
		row := forecastRow{Name: mount, Used: 100 * (1 - avail/total), Free: avail, State: "ok"}
		if len(instances) > 1 {
			row.Name = mount + " (" + parts[0] + ")"
		}
		if s, ok := slope[key]; ok && s < -1 {
			days := avail / -s / 86400
			if days < 3650 {
				row.Days = &days
				switch {
				case days < 3:
					row.State = "down"
				case days < 14:
					row.State = "warn"
				}
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("Prometheus ne rapporte aucun système de fichiers (node-exporter absent ?)")
	}
	sort.SliceStable(rows, func(a, b int) bool {
		da, db := rows[a].Days, rows[b].Days
		if (da == nil) != (db == nil) {
			return da != nil
		}
		if da != nil {
			return *da < *db
		}
		return rows[a].Used > rows[b].Used
	})
	return map[string]any{"items": rows, "hours": hours}, nil
}

func init() {
	Registry["deadlines"] = Provider{TTL: 30 * time.Minute, Fetch: fetchDeadlines}
	Registry["versions"] = Provider{TTL: time.Hour, Fetch: fetchVersions}
	Registry["forecast"] = Provider{TTL: 5 * time.Minute, Fetch: fetchForecast}
	SummaryLabels["deadlines"] = "Échéances"
}
