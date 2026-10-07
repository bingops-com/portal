package providers

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// --- Game server (Steam query protocol, A2S) ----------------------------

type gameServer struct {
	Online  bool     `json:"online"`
	Name    string   `json:"name,omitempty"`
	Map     string   `json:"map,omitempty"`
	Game    string   `json:"game,omitempty"`
	Players int      `json:"players"`
	Max     int      `json:"max"`
	Names   []string `json:"names"`
}

var a2sHeader = []byte{0xff, 0xff, 0xff, 0xff}

// a2s sends one query and answers the server's challenge when asked to.
func a2s(conn net.Conn, kind byte, payload []byte, reply byte) ([]byte, error) {
	buf := make([]byte, 4096)
	challenge := []byte{0xff, 0xff, 0xff, 0xff}
	if kind == 'T' {
		challenge = nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		req := append(append(append([]byte{}, a2sHeader...), kind), payload...)
		req = append(req, challenge...)
		if _, err := conn.Write(req); err != nil {
			return nil, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if n < 5 || !bytes.Equal(buf[:4], a2sHeader) {
			return nil, errors.New("réponse inattendue")
		}
		if buf[4] == 'A' && n >= 9 {
			challenge = append([]byte{}, buf[5:9]...)
			continue
		}
		if buf[4] != reply {
			return nil, errors.New("réponse inattendue")
		}
		return append([]byte{}, buf[5:n]...), nil
	}
	return nil, errors.New("le serveur ne valide pas la requête")
}

func cstring(b []byte) (string, []byte) {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return string(b), nil
	}
	return string(b[:i]), b[i+1:]
}

func fetchGameServer(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Address string `json:"address"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(o.Address))
	if err != nil || host == "" || port == "" {
		return nil, errors.New("indiquez l'adresse du serveur au format hôte:port")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("hôte introuvable: %s", host)
	}
	target := net.JoinHostPort(ips[0].String(), port)
	if err := guard(forHost(ctx, host), target); err != nil {
		return nil, err
	}
	res := gameServer{Names: []string{}}
	conn, err := net.DialTimeout("udp", target, 3*time.Second)
	if err != nil {
		return res, nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))

	// An unanswered query means the server is down: that is a state to show,
	// not a failure of the widget.
	info, err := a2s(conn, 'T', []byte("Source Engine Query\x00"), 'I')
	if err != nil || len(info) < 2 {
		return res, nil
	}
	rest := info[1:] // protocol version
	res.Name, rest = cstring(rest)
	res.Map, rest = cstring(rest)
	_, rest = cstring(rest) // folder
	res.Game, rest = cstring(rest)
	if len(rest) >= 4 {
		res.Players, res.Max = int(rest[2]), int(rest[3])
	}
	res.Online = true

	if res.Players > 0 {
		if list, err := a2s(conn, 'U', nil, 'D'); err == nil && len(list) > 0 {
			count, body := int(list[0]), list[1:]
			for i := 0; i < count && len(body) > 1; i++ {
				var name string
				name, body = cstring(body[1:]) // index byte, then name
				if len(body) < 8 {
					break
				}
				_ = binary.LittleEndian.Uint32(body) // score
				body = body[8:]                      // score + duration
				if name != "" {
					res.Names = append(res.Names, name)
				}
			}
		}
	}
	return res, nil
}

// --- GitHub: latest releases and open pull requests ----------------------

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func githubHeaders(token string) []string {
	h := []string{"Accept", "application/vnd.github+json", "X-GitHub-Api-Version", "2022-11-28"}
	if token != "" {
		h = append(h, "Authorization", "Bearer "+token)
	}
	return h
}

type release struct {
	Repo      string    `json:"repo"`
	Tag       string    `json:"tag"`
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
}

func fetchReleases(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Repos []string `json:"repos"`
		Token string   `json:"token"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	var repos []string
	for _, r := range o.Repos {
		if r = strings.TrimSpace(r); repoRe.MatchString(r) {
			repos = append(repos, r)
		}
	}
	if len(repos) == 0 {
		return nil, errors.New("ajoutez au moins un dépôt GitHub au format propriétaire/nom")
	}
	if len(repos) > 30 {
		repos = repos[:30]
	}
	found := make([]*release, len(repos))
	errs := make([]error, len(repos))
	parallel(len(repos), 4, func(i int) {
		var raw struct {
			Tag       string    `json:"tag_name"`
			URL       string    `json:"html_url"`
			Published time.Time `json:"published_at"`
		}
		if err := getJSON(ctx, "https://api.github.com/repos/"+repos[i]+"/releases/latest", &raw, githubHeaders(o.Token)...); err != nil {
			errs[i] = err
			return
		}
		found[i] = &release{repos[i], raw.Tag, raw.URL, raw.Published}
	})
	releases := []release{}
	failed := 0
	for i, r := range found {
		if r != nil {
			releases = append(releases, *r)
		} else if errs[i] != nil {
			failed++
		}
	}
	if len(releases) == 0 {
		if strings.Contains(errs[0].Error(), "403") || strings.Contains(errs[0].Error(), "429") {
			return nil, errors.New("GitHub limite les requêtes anonymes depuis cette adresse ; ajoutez un jeton ou réessayez dans une heure")
		}
		return nil, errs[0]
	}
	sort.SliceStable(releases, func(a, b int) bool { return releases[a].Published.After(releases[b].Published) })
	return map[string]any{"releases": releases, "failed": failed}, nil
}

type pull struct {
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	Author  string    `json:"author"`
	URL     string    `json:"url"`
	Created time.Time `json:"created"`
	Draft   bool      `json:"draft"`
	Labels  []string  `json:"labels"`
}

func fetchPulls(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Repo  string `json:"repo"`
		Token string `json:"token"`
		Limit int    `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if !repoRe.MatchString(strings.TrimSpace(o.Repo)) {
		return nil, errors.New("indiquez le dépôt GitHub au format propriétaire/nom")
	}
	var raw []struct {
		Number  int       `json:"number"`
		Title   string    `json:"title"`
		URL     string    `json:"html_url"`
		Created time.Time `json:"created_at"`
		Draft   bool      `json:"draft"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	u := "https://api.github.com/repos/" + strings.TrimSpace(o.Repo) + "/pulls?" + url.Values{"state": {"open"}, "per_page": {"50"}}.Encode()
	if err := getJSON(ctx, u, &raw, githubHeaders(o.Token)...); err != nil {
		return nil, err
	}
	pulls := []pull{}
	for _, p := range raw {
		item := pull{p.Number, p.Title, p.User.Login, p.URL, p.Created, p.Draft, []string{}}
		for _, l := range p.Labels {
			item.Labels = append(item.Labels, l.Name)
		}
		pulls = append(pulls, item)
	}
	total := len(pulls)
	if limit := clamp(o.Limit, 8, 1, 50); len(pulls) > limit {
		pulls = pulls[:limit]
	}
	return map[string]any{"pulls": pulls, "total": total, "url": "https://github.com/" + strings.TrimSpace(o.Repo) + "/pulls"}, nil
}

// --- Detail panels of certificates, backups and Gatus endpoints ----------

func init() {
	Details["certificates"] = certificateDetail
	Details["backups"] = backupDetail
	Details["gatus"] = gatusDetail
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("02/01/2006 15:04")
}

func conditionRows(u *unstructured.Unstructured) []DetailRow {
	rows := []DetailRow{}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, raw := range conds {
		c, _ := raw.(map[string]any)
		typ, _ := c["type"].(string)
		status, _ := c["status"].(string)
		reason, _ := c["reason"].(string)
		msg, _ := c["message"].(string)
		row := DetailRow{State: "ok", Title: typ, Sub: reason, Text: msg}
		if status != "True" {
			row.State = "warn"
		}
		rows = append(rows, row)
	}
	return rows
}

func certificateDetail(ctx context.Context, d *Deps, opts map[string]any, q url.Values) (*Detail, error) {
	c, o, err := kubeFor(d, opts)
	if err != nil {
		return nil, err
	}
	ns, name := q.Get("namespace"), q.Get("name")
	if ns == "" || name == "" || !o.keep(ns) {
		return nil, errors.New("certificat introuvable")
	}
	u, err := c.dynamic.Resource(certificates).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, kubeErr(err)
	}
	names, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "dnsNames")
	issuer, _, _ := unstructured.NestedString(u.Object, "spec", "issuerRef", "name")
	secret, _, _ := unstructured.NestedString(u.Object, "spec", "secretName")
	return &Detail{
		Facts: []Fact{
			{"Namespace", ns}, {"Noms", strings.Join(names, ", ")}, {"Émetteur", issuer}, {"Secret TLS", secret},
			{"Valide depuis", stamp(nestedTime(u, "status", "notBefore"))}, {"Expire le", stamp(nestedTime(u, "status", "notAfter"))},
			{"Renouvellement prévu", stamp(nestedTime(u, "status", "renewalTime"))},
		},
		Sections: []Section{{Title: "Conditions", Empty: "cert-manager n'a encore rien rapporté.", Rows: conditionRows(u)}},
	}, nil
}

func backupDetail(ctx context.Context, d *Deps, opts map[string]any, q url.Values) (*Detail, error) {
	var o struct {
		Context  string   `json:"context"`
		CronJobs []string `json:"cronjobs"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	c, err := d.Kube.get(o.Context)
	if err != nil {
		return nil, err
	}
	ns, name := q.Get("namespace"), q.Get("name")
	if ns == "" || name == "" {
		return nil, errors.New("sauvegarde introuvable")
	}

	if q.Get("kind") == "CronJob" {
		listed := false
		for _, ref := range o.CronJobs {
			listed = listed || strings.TrimSpace(ref) == ns+"/"+name
		}
		if !listed {
			return nil, errors.New("ce CronJob n'est pas suivi par le widget")
		}
		job, err := c.typed.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, kubeErr(err)
		}
		out := &Detail{Facts: []Fact{{"Namespace", ns}, {"Planification", job.Spec.Schedule}}}
		if t := job.Status.LastScheduleTime; t != nil {
			out.Facts = append(out.Facts, Fact{"Dernier lancement", stamp(&t.Time)})
		}
		if t := job.Status.LastSuccessfulTime; t != nil {
			out.Facts = append(out.Facts, Fact{"Dernière réussite", stamp(&t.Time)})
		}
		runs := Section{Title: "Exécutions récentes", Empty: "Aucune exécution conservée.", Rows: []DetailRow{}}
		if jobs, err := c.typed.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range jobs.Items {
				j := &jobs.Items[i]
				owned := false
				for _, ref := range j.OwnerReferences {
					owned = owned || (ref.Kind == "CronJob" && ref.Name == name)
				}
				if !owned {
					continue
				}
				row := DetailRow{State: "unknown", Title: j.Name, Aside: "en cours", Time: &j.CreationTimestamp.Time}
				if j.Status.Succeeded > 0 {
					row.State, row.Aside = "ok", "réussie"
				} else if j.Status.Failed > 0 {
					row.State, row.Aside = "down", "en échec"
				}
				runs.Rows = append(runs.Rows, row)
			}
			sort.SliceStable(runs.Rows, func(a, b int) bool { return runs.Rows[a].Time.After(*runs.Rows[b].Time) })
		}
		out.Sections = []Section{runs}
		return out, nil
	}

	cluster, err := c.dynamic.Resource(cnpgClusters).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, kubeErr(err)
	}
	out := &Detail{Facts: []Fact{{"Namespace", ns}}}
	if phase, _, _ := unstructured.NestedString(cluster.Object, "status", "phase"); phase != "" {
		out.Facts = append(out.Facts, Fact{"État du cluster", phase})
	}
	conds := Section{Title: "Archivage et sauvegarde", Rows: []DetailRow{}}
	for _, row := range conditionRows(cluster) {
		if row.Title == "ContinuousArchiving" || row.Title == "LastBackupSucceeded" {
			conds.Rows = append(conds.Rows, row)
		}
	}
	runs := Section{Title: "Sauvegardes", Empty: "Aucune sauvegarde enregistrée pour ce cluster.", Rows: []DetailRow{}}
	if list, err := c.dynamic.Resource(cnpgBackups).Namespace(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			u := &list.Items[i]
			if owner, _, _ := unstructured.NestedString(u.Object, "spec", "cluster", "name"); owner != name {
				continue
			}
			phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
			msg, _, _ := unstructured.NestedString(u.Object, "status", "error")
			created := u.GetCreationTimestamp().Time
			row := DetailRow{State: "unknown", Title: u.GetName(), Aside: phase, Text: msg, Time: &created}
			switch phase {
			case "completed":
				row.State = "ok"
			case "failed":
				row.State = "down"
			}
			runs.Rows = append(runs.Rows, row)
		}
		sort.SliceStable(runs.Rows, func(a, b int) bool { return runs.Rows[a].Time.After(*runs.Rows[b].Time) })
		if len(runs.Rows) > 10 {
			runs.Rows = runs.Rows[:10]
		}
	}
	out.Sections = []Section{conds, runs}
	return out, nil
}

var gatusKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,200}$`)

func gatusDetail(ctx context.Context, _ *Deps, opts map[string]any, q url.Values) (*Detail, error) {
	var o struct {
		URL string `json:"url"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	key := q.Get("key")
	if o.URL == "" || !gatusKeyRe.MatchString(key) {
		return nil, errors.New("endpoint introuvable")
	}
	var raw struct {
		Name    string `json:"name"`
		Group   string `json:"group"`
		Results []struct {
			Success    bool      `json:"success"`
			Status     int       `json:"status"`
			Duration   int64     `json:"duration"`
			Timestamp  time.Time `json:"timestamp"`
			Errors     []string  `json:"errors"`
			Conditions []struct {
				Condition string `json:"condition"`
				Success   bool   `json:"success"`
			} `json:"conditionResults"`
		} `json:"results"`
	}
	if err := getJSON(ctx, trimSlash(o.URL)+"/api/v1/endpoints/"+url.PathEscape(key)+"/statuses?page=1&pageSize=20", &raw); err != nil {
		return nil, err
	}
	checks := Section{Title: "Dernières vérifications", Empty: "Gatus n'a encore rien mesuré.", Rows: []DetailRow{}}
	for i := len(raw.Results) - 1; i >= 0; i-- {
		r := raw.Results[i]
		t := r.Timestamp
		row := DetailRow{State: "ok", Title: fmt.Sprintf("HTTP %d", r.Status), Aside: fmt.Sprintf("%d ms", r.Duration/1e6), Time: &t}
		if r.Status == 0 {
			row.Title = "Sans réponse"
		}
		if !r.Success {
			row.State = "down"
			var failed []string
			for _, c := range r.Conditions {
				if !c.Success {
					failed = append(failed, c.Condition)
				}
			}
			row.Text = strings.Join(append(failed, r.Errors...), " ; ")
		}
		checks.Rows = append(checks.Rows, row)
	}
	return &Detail{Facts: []Fact{{"Groupe", raw.Group}}, Sections: []Section{checks}}, nil
}
