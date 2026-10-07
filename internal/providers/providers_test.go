package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
