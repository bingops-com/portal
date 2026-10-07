package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bingops-com/portal/internal/auth"
	"github.com/bingops-com/portal/internal/cache"
	"github.com/bingops-com/portal/internal/config"
	"github.com/bingops-com/portal/internal/providers"
)

const testYAML = `
title: Test
pages:
  - name: Home
    columns:
      - size: full
        widgets:
          - { id: links, type: bookmarks, options: { groups: [{ title: G, links: [{ title: A, url: "https://a.example" }] }] } }
          - { id: time, type: clock }
`

const layoutBody = `{"pages":[{"name":"Mine","columns":[{"size":"full","widgets":[{"type":"clock"}]}]}]}`

func newServer(t *testing.T, readOnly bool) http.Handler {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "portal.yaml")
	if err := os.WriteFile(path, []byte(testYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return (&Server{
		Store:    config.NewStore(path, filepath.Join(dir, "data"), providers.Known),
		Deps:     &providers.Deps{Kube: providers.NewKubeClients()},
		Cache:    cache.New(),
		Assets:   fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/app.js": {Data: []byte("js")}},
		ReadOnly: readOnly,
	}).Handler()
}

func do(h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var jsonHeader = map[string]string{"Content-Type": "application/json"}

func source(t *testing.T, h http.Handler) string {
	t.Helper()
	var cfg struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(do(h, "GET", "/api/config", "", nil).Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Source
}

func TestLayoutSaveAndReset(t *testing.T) {
	h := newServer(t, false)
	if rec := do(h, "PUT", "/api/layout", layoutBody, jsonHeader); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if got := source(t, h); got != "custom" {
		t.Fatalf("source = %q", got)
	}
	if rec := do(h, "DELETE", "/api/layout", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	if got := source(t, h); got != "yaml" {
		t.Fatalf("source = %q", got)
	}
}

func TestWritesAreRefusedCrossSiteAndWithoutJSON(t *testing.T) {
	h := newServer(t, false)
	cases := map[string]map[string]string{
		"foreign origin":   {"Content-Type": "application/json", "Origin": "https://evil.example"},
		"cross-site fetch": {"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"},
		"form post":        {"Content-Type": "text/plain"},
	}
	for name, headers := range cases {
		if rec := do(h, "PUT", "/api/layout", layoutBody, headers); rec.Code < 400 {
			t.Errorf("%s: accepted with %d", name, rec.Code)
		}
	}
	same := map[string]string{"Content-Type": "application/json", "Origin": "http://example.com", "Sec-Fetch-Site": "same-origin"}
	if rec := do(h, "PUT", "/api/layout", layoutBody, same); rec.Code != http.StatusOK {
		t.Errorf("same-origin save refused: %d %s", rec.Code, rec.Body)
	}
}

func TestReadOnlyBlocksWritesAndPreview(t *testing.T) {
	h := newServer(t, true)
	for _, c := range [][2]string{{"PUT", "/api/layout"}, {"DELETE", "/api/layout"}, {"POST", "/api/preview"}} {
		if rec := do(h, c[0], c[1], layoutBody, jsonHeader); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d", c[0], c[1], rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/data/links", "", nil); rec.Code != http.StatusOK {
		t.Errorf("reads must still work: %d", rec.Code)
	}
}

func TestDataOnlyServesConfiguredWidgets(t *testing.T) {
	h := newServer(t, false)
	rec := do(h, "GET", "/api/data/links", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"A"`) {
		t.Errorf("links: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/data/unknown", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown widget: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/nope", "", nil); rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "json") {
		t.Errorf("unknown API route must be a JSON 404, got %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestStaticFallsBackToIndexForPageRoutes(t *testing.T) {
	h := newServer(t, false)
	for _, path := range []string{"/", "/veille", "/a/b"} {
		rec := do(h, "GET", path, "", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
	rec := do(h, "GET", "/assets/app.js", "", nil)
	if rec.Body.String() != "js" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %q %q", rec.Body, rec.Header().Get("Cache-Control"))
	}
	if rec := do(h, "GET", "/healthz", "", nil); rec.Code != http.StatusOK {
		t.Errorf("healthz: %d", rec.Code)
	}
}

func TestLoginRequiredForWritesButNotReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "portal.yaml")
	os.WriteFile(path, []byte(testYAML), 0o644)
	login, err := auth.New(auth.Config{Issuer: "https://idp.invalid/", ClientID: "portal", PublicURL: "https://portal.invalid"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{
		Store: config.NewStore(path, filepath.Join(dir, "data"), providers.Known), Deps: &providers.Deps{Kube: providers.NewKubeClients()},
		Cache: cache.New(), Assets: fstest.MapFS{"index.html": {Data: []byte("app")}}, Auth: login,
	}).Handler()

	for _, c := range [][2]string{{"PUT", "/api/layout"}, {"DELETE", "/api/layout"}, {"POST", "/api/preview"}} {
		if rec := do(h, c[0], c[1], layoutBody, jsonHeader); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", c[0], c[1], rec.Code)
		}
	}
	rec := do(h, "GET", "/api/config", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"loginRequired":true`) || strings.Contains(rec.Body.String(), `"user"`) {
		t.Errorf("config: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/data/links", "", nil); rec.Code != http.StatusOK {
		t.Errorf("reads must stay open: %d", rec.Code)
	}
}
