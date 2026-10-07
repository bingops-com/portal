package providers

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func serve(t *testing.T, routes map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestDecodeExpandsOnlyPortalVariables(t *testing.T) {
	t.Setenv("PORTAL_VAR_TOKEN", "s3cret")
	t.Setenv("HOME_SECRET", "nope")
	var o struct {
		A string     `json:"a"`
		B string     `json:"b"`
		C []namedURL `json:"c"`
	}
	err := decode(map[string]any{
		"a": "x/${PORTAL_VAR_TOKEN}",
		"b": "${HOME_SECRET}",
		"c": []any{"https://a.example/feed", map[string]any{"title": "B", "url": "${PORTAL_VAR_TOKEN}"}},
	}, &o)
	if err != nil {
		t.Fatal(err)
	}
	if o.A != "x/s3cret" || o.B != "${HOME_SECRET}" {
		t.Errorf("a=%q b=%q", o.A, o.B)
	}
	if o.C[0].URL != "https://a.example/feed" || o.C[1].Title != "B" || o.C[1].URL != "s3cret" {
		t.Errorf("c=%+v", o.C)
	}
}

func TestGetBytesRejectsNonHTTPAndHidesPaths(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://host/x", "not a url"} {
		if _, err := getBytes(context.Background(), u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	base := serve(t, nil)
	_, err := getBytes(context.Background(), base+"/private/token-123.ics")
	if err == nil || strings.Contains(err.Error(), "token-123") {
		t.Errorf("error must not leak the URL path: %v", err)
	}
}

func TestPrometheusStatsAndThresholds(t *testing.T) {
	base := serve(t, map[string]string{
		"/api/v1/query":       `{"status":"success","data":{"result":[{"value":[1700000000,"82.5"]}]}}`,
		"/api/v1/query_range": `{"status":"success","data":{"result":[{"values":[[1,"1"],[2,"NaN"],[3,"3"]]}]}}`,
	})
	out, err := fetchPrometheus(context.Background(), nil, map[string]any{
		"url": base + "/",
		"stats": []any{
			map[string]any{"label": "CPU", "query": "up", "format": "percent", "warn": 70, "danger": 90},
			map[string]any{"label": "Disk", "query": "up", "warn": 70, "danger": 80, "sparkline": false},
			map[string]any{"label": "Pods", "query": "up"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stats := out.(map[string]any)["stats"].([]promStat)
	if *stats[0].Value != 82.5 || stats[0].State != "warn" || len(stats[0].Series) != 2 {
		t.Errorf("cpu = %+v", stats[0])
	}
	if stats[1].State != "down" || stats[1].Series != nil {
		t.Errorf("disk = %+v", stats[1])
	}
	if stats[2].State != "none" {
		t.Errorf("pods = %+v", stats[2])
	}
}

func TestPrometheusEmptyResultAndOutage(t *testing.T) {
	base := serve(t, map[string]string{"/api/v1/query": `{"status":"success","data":{"result":[]}}`})
	opts := map[string]any{"url": base, "stats": []any{map[string]any{"label": "X", "query": "absent"}}}
	out, err := fetchPrometheus(context.Background(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := out.(map[string]any)["stats"].([]promStat)[0]; st.Value != nil || st.Error == "" {
		t.Errorf("%+v", st)
	}

	opts["url"] = serve(t, nil)
	if _, err := fetchPrometheus(context.Background(), nil, opts); err == nil {
		t.Error("an unreachable Prometheus must be an error, not empty stats")
	}
	if _, err := fetchPrometheus(context.Background(), nil, map[string]any{"stats": opts["stats"]}); err == nil {
		t.Error("a missing URL must be an error")
	}
}

func TestAlertsFilterSortAndSummary(t *testing.T) {
	base := serve(t, map[string]string{"/api/v1/alerts": `{"data":{"alerts":[
		{"labels":{"alertname":"Watchdog","severity":"none"},"state":"firing","activeAt":"2026-01-01T00:00:00Z"},
		{"labels":{"alertname":"Slow","severity":"warning"},"annotations":{"description":"d"},"state":"firing","activeAt":"2026-01-01T00:00:00Z"},
		{"labels":{"alertname":"Pending","severity":"critical"},"state":"pending","activeAt":"2026-01-01T00:00:00Z"},
		{"labels":{"alertname":"Muted","severity":"critical"},"state":"firing","activeAt":"2026-01-01T00:00:00Z"},
		{"labels":{"alertname":"Down","severity":"critical","namespace":"db"},"annotations":{"summary":"s"},"state":"firing","activeAt":"2026-01-01T00:00:00Z"}
	]}}`})
	out, err := fetchAlerts(context.Background(), nil, map[string]any{"url": base, "ignore": []any{"Muted"}})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(alertsResult)
	if len(res.Alerts) != 2 || res.Alerts[0].Name != "Down" || res.Alerts[0].Summary != "s" || res.Alerts[1].Summary != "d" {
		t.Fatalf("%+v", res.Alerts)
	}
	if s := res.Summary(); s.State != "down" {
		t.Errorf("summary = %+v", s)
	}
	if s := (alertsResult{}).Summary(); s.State != "ok" {
		t.Errorf("empty summary = %+v", s)
	}
}

func TestGatusUptimeAndSummary(t *testing.T) {
	base := serve(t, map[string]string{"/api/v1/endpoints/statuses": `[
		{"name":"web","group":"public","key":"public_web","results":[
			{"success":true,"duration":10000000,"timestamp":"2026-01-01T00:00:00Z"},
			{"success":false,"duration":20000000,"timestamp":"2026-01-01T00:01:00Z"},
			{"success":true,"duration":30000000,"timestamp":"2026-01-01T00:02:00Z"},
			{"success":false,"duration":40000000,"timestamp":"2026-01-01T00:03:00Z"}]},
		{"name":"api","group":"core","key":"core_api","results":[
			{"success":true,"duration":5000000,"timestamp":"2026-01-01T00:00:00Z"}]},
		{"name":"new","group":"core","key":"core_new","results":[]}
	]`})
	out, err := fetchGatus(context.Background(), nil, map[string]any{"url": base, "publicUrl": "https://status.example/"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(gatusResult)
	if len(res.Endpoints) != 3 || res.Up != 1 {
		t.Fatalf("%+v", res)
	}
	api, web := res.Endpoints[0], res.Endpoints[2]
	if api.Name != "api" || !api.Up || api.Uptime != 100 || api.Millis != 5 {
		t.Errorf("api = %+v", api)
	}
	if web.Up || web.Uptime != 50 || web.Millis != 40 || web.URL != "https://status.example/endpoints/public_web" {
		t.Errorf("web = %+v", web)
	}
	if s := res.Summary(); s.State != "down" || s.Detail != "1/3 en ligne" {
		t.Errorf("summary = %+v", s)
	}
}

func TestBookmarksProbeAndHideCheckURLs(t *testing.T) {
	base := serve(t, map[string]string{"/ok": "fine"})
	out, err := fetchBookmarks(context.Background(), nil, map[string]any{"groups": []any{map[string]any{
		"title": "Lab",
		"links": []any{
			map[string]any{"title": "Up", "url": "https://a.example", "checkUrl": base + "/ok"},
			map[string]any{"title": "Down", "url": "https://b.example", "checkUrl": base + "/missing"},
			map[string]any{"title": "Plain", "url": "https://c.example"},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	links := out.(map[string]any)["groups"].([]bookmarkGroup)[0].Links
	if links[0].Status != "up" || links[1].Status != "down" || links[2].Status != "" {
		t.Errorf("%+v", links)
	}
	for _, l := range links {
		if l.CheckURL != "" {
			t.Errorf("checkUrl of %q reached the response", l.Title)
		}
	}
}

func TestRSSMergesFeedsNewestFirst(t *testing.T) {
	feed := func(title, date string) string {
		return `<?xml version="1.0"?><rss version="2.0"><channel><title>Chan</title><item><title>` + title +
			`</title><link>https://x.example/` + title + `</link><pubDate>` + date + `</pubDate></item></channel></rss>`
	}
	base := serve(t, map[string]string{
		"/a": feed("old", "Mon, 01 Jan 2024 10:00:00 GMT"),
		"/b": feed("new", "Tue, 02 Jan 2024 10:00:00 GMT"),
	})
	out, err := fetchRSS(context.Background(), nil, map[string]any{"feeds": []any{
		map[string]any{"title": "A", "url": base + "/a"}, base + "/b", base + "/broken",
	}})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(feedResult)
	if len(res.Items) != 2 || res.Items[0].Title != "new" || res.Items[0].Source != "Chan" || res.Items[1].Source != "A" {
		t.Errorf("%+v", res.Items)
	}
	if len(res.Failed) != 1 {
		t.Errorf("failed = %v", res.Failed)
	}
}

func kubeDeps(objects []runtime.Object, argo ...runtime.Object) *Deps {
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{argoApps: "ApplicationList"}, argo...)
	k := NewKubeClients()
	k.clients[""] = &kubeClient{name: "test", typed: fake.NewSimpleClientset(objects...), dynamic: dyn}
	return &Deps{Kube: k}
}

func TestClusterOverviewFlagsProblemPods(t *testing.T) {
	old := metav1.NewTime(time.Now().Add(-time.Hour))
	node := func(name string, ready corev1.ConditionStatus) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""}},
			Status: corev1.NodeStatus{
				NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.36.1"},
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}},
			},
		}
	}
	pod := func(name string, phase corev1.PodPhase, statuses ...corev1.ContainerStatus) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "apps", CreationTimestamp: old},
			Status:     corev1.PodStatus{Phase: phase, ContainerStatuses: statuses},
		}
	}
	deps := kubeDeps([]runtime.Object{
		node("n1", corev1.ConditionTrue), node("n2", corev1.ConditionFalse),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}},
		pod("healthy", corev1.PodRunning),
		pod("done", corev1.PodSucceeded, corev1.ContainerStatus{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}}),
		pod("crash", corev1.PodRunning, corev1.ContainerStatus{RestartCount: 7, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}),
		pod("stuck", corev1.PodPending),
	})
	out, err := fetchCluster(context.Background(), deps, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(clusterResult)
	if res.NodesReady != 1 || len(res.Nodes) != 2 || res.Namespaces != 1 || res.PodsTotal != 4 || res.Version != "v1.36.1" {
		t.Fatalf("%+v", res)
	}
	if res.Nodes[0].Roles[0] != "control-plane" {
		t.Errorf("roles = %v", res.Nodes[0].Roles)
	}
	reasons := map[string]string{}
	for _, p := range res.Problems {
		reasons[p.Name] = p.Reason
	}
	if len(reasons) != 2 || reasons["crash"] != "CrashLoopBackOff" || reasons["stuck"] != "Pending" {
		t.Errorf("problems = %v", reasons)
	}
	if s := res.Summary(); s.State != "down" {
		t.Errorf("a NotReady node must dominate the summary: %+v", s)
	}
}

func TestWorkloadsFilterAndOrder(t *testing.T) {
	one := int32(1)
	dep := func(ns, name string, ready int32) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec: appsv1.DeploymentSpec{Replicas: &one, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Image: "ghcr.io/org/app:1.2@sha256:abc"}},
			}}},
			Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
		}
	}
	deps := kubeDeps([]runtime.Object{dep("apps", "ok", 1), dep("apps", "broken", 0), dep("kube-system", "hidden", 1)})
	out, err := fetchWorkloads(context.Background(), deps, map[string]any{"exclude": []any{"kube-system"}})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	items := res["workloads"].([]workload)
	if len(items) != 2 || items[0].Name != "broken" || items[0].Healthy || res["healthy"] != 1 {
		t.Fatalf("%+v", items)
	}
	if items[0].Image != "app:1.2" {
		t.Errorf("image = %q", items[0].Image)
	}
}

func TestArgoApplications(t *testing.T) {
	app := func(name, sync, health, revision string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
			"metadata": map[string]any{"name": name, "namespace": "argocd"},
			"spec":     map[string]any{"project": "lab", "source": map[string]any{"path": "apps/" + name}},
			"status": map[string]any{
				"sync":           map[string]any{"status": sync, "revision": revision},
				"health":         map[string]any{"status": health},
				"operationState": map[string]any{"finishedAt": "2026-01-01T00:00:00Z"},
			},
		}}
	}
	deps := kubeDeps(nil,
		app("good", "Synced", "Healthy", "0123456789abcdef0123456789abcdef01234567"),
		app("drift", "OutOfSync", "Healthy", "1.2.3"))
	out, err := fetchArgo(context.Background(), deps, map[string]any{"url": "https://argo.example/"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(argoResult)
	if len(res.Apps) != 2 || res.Healthy != 1 || res.Apps[0].Name != "drift" {
		t.Fatalf("%+v", res)
	}
	good := res.Apps[1]
	if good.Revision != "0123456" || good.Path != "apps/good" || good.SyncedAt == nil || good.URL != "https://argo.example/applications/argocd/good" {
		t.Errorf("%+v", good)
	}
	if s := res.Summary(); s.State != "warn" {
		t.Errorf("summary = %+v", s)
	}
}

func custom(gvr schema.GroupVersionResource, kind, ns, name string, created time.Time, body map[string]any) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": gvr.Group + "/" + gvr.Version, "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": ns, "creationTimestamp": created.UTC().Format(time.RFC3339)},
	}
	for k, v := range body {
		obj[k] = v
	}
	return &unstructured.Unstructured{Object: obj}
}

func platformDeps(typed []runtime.Object, objects ...runtime.Object) *Deps {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		certificates: "CertificateList", cnpgClusters: "ClusterList", cnpgBackups: "BackupList", argoApps: "ApplicationList",
	}, objects...)
	k := NewKubeClients()
	k.clients[""] = &kubeClient{name: "test", typed: fake.NewSimpleClientset(typed...), dynamic: dyn}
	return &Deps{Kube: k}
}

func TestCertificatesStates(t *testing.T) {
	now := time.Now()
	cert := func(name, ready string, notAfter time.Time) *unstructured.Unstructured {
		return custom(certificates, "Certificate", "web", name, now, map[string]any{
			"spec": map[string]any{"dnsNames": []any{name + ".example"}},
			"status": map[string]any{
				"notAfter":   notAfter.UTC().Format(time.RFC3339),
				"conditions": []any{map[string]any{"type": "Ready", "status": ready, "reason": "Failed"}},
			},
		})
	}
	deps := platformDeps(nil,
		cert("fine", "True", now.Add(60*24*time.Hour)),
		cert("soon", "True", now.Add(5*24*time.Hour)),
		cert("broken", "False", now.Add(60*24*time.Hour)),
		cert("expired", "True", now.Add(-time.Hour)))
	out, err := fetchCertificates(context.Background(), deps, map[string]any{"warnDays": 14})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(certificatesResult)
	states := map[string]string{}
	for _, c := range res.Certificates {
		states[c.Name] = c.State
	}
	want := map[string]string{"fine": "ok", "soon": "warn", "broken": "down", "expired": "down"}
	for name, state := range want {
		if states[name] != state {
			t.Errorf("%s = %q, want %q", name, states[name], state)
		}
	}
	if res.Valid != 1 || res.Certificates[len(res.Certificates)-1].Name != "fine" || res.Certificates[0].State != "down" {
		t.Errorf("valid=%d order=%+v", res.Valid, res.Certificates)
	}
	if s := res.Summary(); s.State != "down" || s.Detail != "1/4 valides" {
		t.Errorf("summary = %+v", s)
	}
}

func TestBackupsFromCloudNativePGAndCronJobs(t *testing.T) {
	now := time.Now()
	old := now.Add(-5 * 24 * time.Hour)
	cluster := func(name string, created time.Time) *unstructured.Unstructured {
		return custom(cnpgClusters, "Cluster", "db", name, created, nil)
	}
	run := func(name, cluster, phase string, created time.Time) *unstructured.Unstructured {
		return custom(cnpgBackups, "Backup", "db", name, created, map[string]any{
			"spec":   map[string]any{"cluster": map[string]any{"name": cluster}},
			"status": map[string]any{"phase": phase, "stoppedAt": created.UTC().Format(time.RFC3339)},
		})
	}
	success := metav1.NewTime(now.Add(-2 * time.Hour))
	cron := func(name string, created time.Time, last *metav1.Time) *batchv1.CronJob {
		return &batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Namespace: "game", Name: name, CreationTimestamp: metav1.NewTime(created)},
			Status:     batchv1.CronJobStatus{LastSuccessfulTime: last},
		}
	}
	deps := platformDeps(
		[]runtime.Object{cron("recent", old, &success), cron("new", now.Add(-time.Hour), nil)},
		cluster("healthy", old), cluster("stuck", old), cluster("fresh", now.Add(-time.Hour)),
		run("healthy-1", "healthy", "completed", now.Add(-30*time.Hour)),
		run("healthy-2", "healthy", "completed", now.Add(-3*time.Hour)),
		run("stuck-1", "stuck", "started", now.Add(-60*time.Hour)),
	)
	out, err := fetchBackups(context.Background(), deps, map[string]any{
		"cronjobs": []any{"game/recent", "game/new", "game/missing", "malformed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(backupsResult)
	got := map[string]backup{}
	for _, b := range res.Backups {
		got[b.Name] = b
	}
	want := map[string]string{"healthy": "ok", "stuck": "down", "fresh": "unknown", "recent": "ok", "new": "unknown", "missing": "down"}
	if len(got) != len(want) {
		t.Fatalf("%d sources: %+v", len(got), res.Backups)
	}
	for name, state := range want {
		if got[name].State != state {
			t.Errorf("%s = %q (%s), want %q", name, got[name].State, got[name].Detail, state)
		}
	}
	if !strings.Contains(got["stuck"].Detail, "started") {
		t.Errorf("stuck detail = %q", got["stuck"].Detail)
	}
	if res.Healthy != 2 || res.Backups[0].State != "down" {
		t.Errorf("healthy=%d first=%+v", res.Healthy, res.Backups[0])
	}
	if s := res.Summary(); s.State != "down" {
		t.Errorf("summary = %+v", s)
	}
}

func TestBackupsWithoutAnySourceIsAnError(t *testing.T) {
	if _, err := fetchBackups(context.Background(), platformDeps(nil), nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestArgoIgnoredApplicationsDoNotCount(t *testing.T) {
	app := func(name, sync string) *unstructured.Unstructured {
		return custom(argoApps, "Application", "argocd", name, time.Now(), map[string]any{
			"status": map[string]any{"sync": map[string]any{"status": sync}, "health": map[string]any{"status": "Healthy"}},
		})
	}
	deps := platformDeps(nil, app("good", "Synced"), app("drift", "OutOfSync"))
	out, err := fetchArgo(context.Background(), deps, map[string]any{"ignore": []any{"drift"}})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(argoResult)
	if res.Tracked != 1 || res.Healthy != 1 || len(res.Apps) != 2 {
		t.Fatalf("%+v", res)
	}
	if s := res.Summary(); s.State != "ok" || s.Detail != "1/1 application à jour" {
		t.Errorf("summary = %+v", s)
	}
}

func TestAlertSummaryIgnoresInformationalAlerts(t *testing.T) {
	info := alertsResult{Alerts: []alert{{Name: "Throttling", Severity: "info"}}}
	if s := info.Summary(); s.State != "ok" {
		t.Errorf("info only = %+v", s)
	}
	mixed := alertsResult{Alerts: []alert{{Severity: "info"}, {Severity: "warning"}}}
	if s := mixed.Summary(); s.State != "warn" || s.Detail != "1 alerte active" {
		t.Errorf("mixed = %+v", s)
	}
}

func TestActivityMergesSourcesNewestFirst(t *testing.T) {
	now := time.Now()
	stamp := func(d time.Duration) string { return now.Add(-d).UTC().Format(time.RFC3339) }
	app := custom(argoApps, "Application", "argocd", "web", now, map[string]any{
		"status": map[string]any{"history": []any{
			map[string]any{"deployedAt": stamp(100 * time.Hour), "revision": "old"},
			map[string]any{"deployedAt": stamp(2 * time.Hour), "revision": "0123456789abcdef0123456789abcdef01234567"},
		}},
	})
	done := custom(cnpgBackups, "Backup", "db", "b1", now, map[string]any{
		"spec":   map[string]any{"cluster": map[string]any{"name": "pg"}},
		"status": map[string]any{"phase": "completed", "stoppedAt": stamp(time.Hour)},
	})
	crashed := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "api-1"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 2,
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(now.Add(-30 * time.Minute)),
			}},
		}}},
	}
	prom := serve(t, map[string]string{"/api/v1/alerts": `{"data":{"alerts":[{"labels":{"alertname":"DiskFull","severity":"critical"},"state":"firing","activeAt":"` + stamp(10*time.Minute) + `"}]}}`})

	out, err := fetchActivity(context.Background(), platformDeps([]runtime.Object{crashed}, app, done), map[string]any{"url": prom, "hours": 72})
	if err != nil {
		t.Fatal(err)
	}
	items := out.(map[string]any)["items"].([]activityItem)
	var got []string
	for _, it := range items {
		got = append(got, it.Kind+":"+it.Title)
	}
	want := []string{"alert:DiskFull", "restart:api-1", "backup:pg", "deploy:web"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if items[0].State != "down" || items[3].Detail != "déployée en 0123456" || !strings.Contains(items[1].Detail, "OOMKilled") || items[1].State != "warn" {
		t.Errorf("%+v", items)
	}
}

func TestWorkloadDetailListsPodsAndEvents(t *testing.T) {
	one := int32(1)
	sel := map[string]string{"app": "api"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "api"},
		Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: sel},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: "org/api:1"}}}}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "api-abc", Labels: sel},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 3, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		}}},
	}
	other := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "web-1", Labels: map[string]string{"app": "web"}}}
	event := func(name, obj string) *corev1.Event {
		return &corev1.Event{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: name}, Type: corev1.EventTypeWarning, Reason: "BackOff",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: obj}, LastTimestamp: metav1.Now()}
	}
	deps := platformDeps([]runtime.Object{dep, pod, other, event("e1", "api-abc"), event("e2", "web-1")})
	q := url.Values{"namespace": {"apps"}, "name": {"api"}, "kind": {"Deployment"}}
	out, err := workloadDetail(context.Background(), deps, nil, q)
	if err != nil {
		t.Fatal(err)
	}
	pods, events := out.Sections[0], out.Sections[1]
	if len(pods.Rows) != 1 || pods.Rows[0].Title != "api-abc" || pods.Rows[0].State != "warn" || pods.Rows[0].Text != "CrashLoopBackOff" || pods.Rows[0].Aside != "3 redémarrages" {
		t.Errorf("pods = %+v", pods.Rows)
	}
	if len(events.Rows) != 1 || events.Rows[0].Sub != "Pod/api-abc" {
		t.Errorf("events = %+v", events.Rows)
	}
	if _, err := workloadDetail(context.Background(), deps, map[string]any{"exclude": []any{"apps"}}, q); err == nil {
		t.Error("a namespace hidden from the widget must not be readable through its detail")
	}
}

func TestArgoDetailListsOnlyResourcesNeedingAttention(t *testing.T) {
	app := custom(argoApps, "Application", "argocd", "web", time.Now(), map[string]any{
		"spec": map[string]any{"project": "lab"},
		"status": map[string]any{
			"sync": map[string]any{"status": "OutOfSync"}, "health": map[string]any{"status": "Degraded"},
			"operationState": map[string]any{"phase": "Running", "message": "waiting for healthy state"},
			"resources": []any{
				map[string]any{"kind": "Service", "name": "web", "namespace": "web", "status": "Synced", "health": map[string]any{"status": "Healthy"}},
				map[string]any{"kind": "ConfigMap", "name": "cfg", "namespace": "web", "status": "OutOfSync"},
				map[string]any{"kind": "Deployment", "name": "web", "namespace": "web", "status": "Synced", "health": map[string]any{"status": "Degraded", "message": "crash"}},
			},
		},
	})
	out, err := argoDetail(context.Background(), platformDeps(nil, app), map[string]any{"url": "https://argo.example"}, url.Values{"name": {"web"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.URL != "https://argo.example/applications/argocd/web" || out.Sections[0].Rows[0].Text != "waiting for healthy state" {
		t.Errorf("%+v", out)
	}
	rows := out.Sections[len(out.Sections)-1].Rows
	if len(rows) != 2 || rows[0].Title != "ConfigMap/cfg" || rows[0].Aside != "OutOfSync" || rows[1].State != "down" || rows[1].Text != "crash" {
		t.Errorf("resources = %+v", rows)
	}
	if _, err := argoDetail(context.Background(), platformDeps(nil, app), nil, url.Values{"name": {"nope"}}); err == nil {
		t.Error("unknown application must be an error")
	}
}

func TestPrometheusDetailReturnsTimedPoints(t *testing.T) {
	base := serve(t, map[string]string{"/api/v1/query_range": `{"data":{"result":[{"values":[[1700000000,"1.5"],[1700000060,"NaN"],[1700000120,"3"]]}]}}`})
	opts := map[string]any{"url": base, "stats": []any{map[string]any{"label": "CPU", "query": "up", "format": "percent"}}}
	out, err := prometheusDetail(context.Background(), nil, opts, url.Values{"stat": {"0"}, "range": {"7d"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := out.Chart; c.Label != "CPU" || len(c.Points) != 2 || c.Points[1] != [2]float64{1700000120, 3} {
		t.Errorf("%+v", out.Chart)
	}
	for _, stat := range []string{"1", "-1", "x", ""} {
		if _, err := prometheusDetail(context.Background(), nil, opts, url.Values{"stat": {stat}}); err == nil {
			t.Errorf("stat=%q accepted", stat)
		}
	}
}

func TestInternalHostsGuard(t *testing.T) {
	base := serve(t, map[string]string{"/x": "ok"}) // listens on 127.0.0.1
	t.Cleanup(func() { SetInternalHosts(nil) })

	SetInternalHosts([]string{".svc.cluster.local"})
	if _, err := getBytes(context.Background(), base+"/x"); err == nil || !strings.Contains(err.Error(), "interne") {
		t.Fatalf("a private address must be refused when its host is not listed: %v", err)
	}
	SetInternalHosts([]string{"127.0.0.1"})
	if _, err := getBytes(context.Background(), base+"/x"); err != nil {
		t.Fatalf("a listed host must be reachable: %v", err)
	}
	for host, want := range map[string]bool{"a.svc.cluster.local": true, "svc.cluster.local": false, "evil.example": false, "127.0.0.1": false} {
		SetInternalHosts([]string{".svc.cluster.local"})
		if internalAllowed(host) != want {
			t.Errorf("internalAllowed(%q) != %v", host, want)
		}
	}
}

func TestGameServerQuery(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			req := buf[:n]
			challenged := bytes.HasSuffix(req, []byte{1, 2, 3, 4})
			switch {
			case !challenged:
				conn.WriteTo([]byte("\xff\xff\xff\xffA\x01\x02\x03\x04"), addr)
			case req[4] == 'T':
				conn.WriteTo([]byte("\xff\xff\xff\xffI\x11My Server\x00Muldraugh\x00zomboid\x00Project Zomboid\x00\x00\x00\x02\x20\x00dl\x00\x00"), addr)
			case req[4] == 'U':
				conn.WriteTo([]byte("\xff\xff\xff\xffD\x02\x00alice\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01bob\x00\x00\x00\x00\x00\x00\x00\x00\x00"), addr)
			}
		}
	}()
	out, err := fetchGameServer(context.Background(), nil, map[string]any{"address": conn.LocalAddr().String()})
	if err != nil {
		t.Fatal(err)
	}
	gs := out.(gameServer)
	if !gs.Online || gs.Name != "My Server" || gs.Map != "Muldraugh" || gs.Players != 2 || gs.Max != 32 || strings.Join(gs.Names, ",") != "alice,bob" {
		t.Fatalf("%+v", gs)
	}

	silent, _ := net.ListenPacket("udp", "127.0.0.1:0")
	addr := silent.LocalAddr().String()
	silent.Close()
	out, err = fetchGameServer(context.Background(), nil, map[string]any{"address": addr})
	if err != nil || out.(gameServer).Online {
		t.Fatalf("a silent server is offline, not an error: %+v %v", out, err)
	}
}

func TestGatusUsesSevenDayUptimeWhenAvailable(t *testing.T) {
	base := serve(t, map[string]string{
		"/api/v1/endpoints/statuses":         `[{"name":"web","group":"g","key":"g_web","results":[{"success":true,"duration":1000000,"timestamp":"2026-01-01T00:00:00Z"}]},{"name":"api","group":"g","key":"g_api","results":[{"success":true,"duration":1000000,"timestamp":"2026-01-01T00:00:00Z"}]}]`,
		"/api/v1/endpoints/g_web/uptimes/7d": `0.9871`,
	})
	out, err := fetchGatus(context.Background(), nil, map[string]any{"url": base})
	if err != nil {
		t.Fatal(err)
	}
	eps := out.(gatusResult).Endpoints
	if eps[1].Name != "web" || eps[1].Window != "7 j" || eps[1].Uptime < 98.7 || eps[1].Uptime > 98.72 {
		t.Errorf("web = %+v", eps[1])
	}
	if eps[0].Window != "récent" || eps[0].Uptime != 100 {
		t.Errorf("api = %+v", eps[0])
	}
}
