package gaming

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const oldSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const newSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const sampleRoom = "apiVersion: gaming.bingops.com/v1alpha1\nkind: GameRoom\nmetadata:\n  name: one\n  namespace: games\nspec:\n  gatewayRef: shared-test\n  game: project-zomboid\n  state: Suspended\n  version: v1\n  world:\n    id: keep-this-world\n"

type fixedToken struct{}

func (fixedToken) Token(context.Context) (string, error) { return "test-only-not-a-real-token", nil }

type fixture struct {
	head, room, message, treeContent string
	patches, posts                   int
	race                             bool
	methods                          []string
	server                           *httptest.Server
	service                          *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{head: oldSHA, room: sampleRoom}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.methods = append(f.methods, r.Method+" "+r.URL.Path)
		write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/git/ref/heads/"):
			write(map[string]any{"object": map[string]string{"sha": f.head}})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/contents/"):
			content := f.room
			if strings.HasSuffix(r.URL.Path, "catalogue.yaml") {
				releases := map[string]any{}
				for _, key := range []string{"v1", "v2"} {
					releases[key] = map[string]any{"runtimeImage": "example.invalid/pz@sha256:" + strings.Repeat("c", 64), "steamBuildId": "100", "binarySnapshot": strings.Repeat("d", 64), "binaryRepository": "s3:https://example.invalid/releases", "operationProtocol": 1}
				}
				body, _ := json.Marshal(releases)
				doc := map[string]any{"kind": "ConfigMap", "metadata": map[string]string{"name": "gaas-release-catalog"}, "data": map[string]string{"releases.json": string(body)}}
				out, _ := yaml.Marshal(doc)
				content = string(out)
			}
			write(map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/git/commits/"):
			write(map[string]any{"message": f.message, "tree": map[string]string{"sha": strings.Repeat("e", 40)}, "parents": []any{map[string]string{"sha": oldSHA}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/git/trees"):
			var v struct {
				Tree []struct{ Path, Content string }
			}
			if json.NewDecoder(r.Body).Decode(&v) != nil || len(v.Tree) != 1 || v.Tree[0].Path != "rooms/one.yaml" {
				t.Error("unexpected write scope")
				w.WriteHeader(400)
				return
			}
			f.treeContent = v.Tree[0].Content
			f.posts++
			write(map[string]string{"sha": strings.Repeat("f", 40)})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/git/commits"):
			var v struct{ Message string }
			json.NewDecoder(r.Body).Decode(&v)
			f.message = v.Message
			f.posts++
			write(map[string]string{"sha": newSHA})
		case r.Method == "PATCH":
			var v struct {
				SHA   string
				Force bool
			}
			json.NewDecoder(r.Body).Decode(&v)
			if v.Force {
				t.Error("force push attempted")
			}
			if f.race {
				w.WriteHeader(422)
				return
			}
			f.head = v.SHA
			f.patches++
			write(map[string]string{"sha": f.head})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/git/refs"):
			var v struct{ Ref string }
			json.NewDecoder(r.Body).Decode(&v)
			if !strings.HasPrefix(v.Ref, "refs/heads/game-room/") {
				t.Error("PR wrote operational branch")
			}
			f.posts++
			write(map[string]string{})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pulls"):
			write([]any{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/pulls"):
			var v struct{ Base string }
			json.NewDecoder(r.Body).Decode(&v)
			if v.Base != "ops" {
				t.Error("unexpected PR target")
			}
			f.posts++
			write(map[string]string{"html_url": "https://github.com/example/lab/pull/1"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(f.server.Close)
	g := NewGitHub(fixedToken{})
	g.baseURL = f.server.URL
	var err error
	f.service, err = New(Config{Repository: "example/lab", Branch: "ops", Directory: "rooms", Catalogue: "catalogue.yaml", Namespace: "games", Gateway: "shared-test", Rooms: []string{"one"}}, g)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func request() Change {
	return Change{ExpectedRevision: oldSHA, RequestID: "request-0123456789", State: "Running"}
}
func TestStateCommitPreservesWorldAndUsesNonForceUpdate(t *testing.T) {
	f := newFixture(t)
	result, err := f.service.Apply(context.Background(), "one", "owner@example.invalid", request())
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "commit" || result.Revision != newSHA || f.patches != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	var before, after map[string]any
	yaml.Unmarshal([]byte(sampleRoom), &before)
	yaml.Unmarshal([]byte(f.treeContent), &after)
	before["spec"].(map[string]any)["state"] = "Running"
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("fields other than state changed")
	}
	if !strings.Contains(f.message, "Requested-by: owner@example.invalid") {
		t.Fatal("missing actor audit")
	}
}
func TestPreviewIsReadOnly(t *testing.T) {
	f := newFixture(t)
	_, err := f.service.Preview(context.Background(), "one", request())
	if err != nil {
		t.Fatal(err)
	}
	if f.posts != 0 || f.patches != 0 {
		t.Fatal("preview wrote Git")
	}
}
func TestConflictsNeverForcePush(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale-view", true: "concurrent-write"}[race], func(t *testing.T) {
			f := newFixture(t)
			if race {
				f.race = true
			} else {
				f.head = newSHA
			}
			_, err := f.service.Apply(context.Background(), "one", "owner", request())
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
			if f.patches != 0 {
				t.Fatal("conflicting branch overwritten")
			}
		})
	}
}
func TestRetryReturnsSameCommittedRequest(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 2; i++ {
		_, err := f.service.Apply(context.Background(), "one", "owner", request())
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.patches != 1 || f.posts != 2 {
		t.Fatal("retry made another commit")
	}
}
func TestVersionChangeCreatesPRWithoutChangingOperationalBranch(t *testing.T) {
	f := newFixture(t)
	c := request()
	c.State = ""
	c.Version = "v2"
	r, err := f.service.Apply(context.Background(), "one", "owner", c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "pull_request" || f.patches != 0 || f.head != oldSHA {
		t.Fatal("version changed without PR merge")
	}
}
func TestScopeAndCatalogueValidation(t *testing.T) {
	for _, name := range []string{"unknown-room", "bad-state", "both-fields", "unknown-version", "wrong-gateway", "duplicate-key", "yaml-alias", "multiple-documents"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			c := request()
			room := "one"
			switch name {
			case "unknown-room":
				room = "../../production"
			case "bad-state":
				c.State = "Delete"
			case "both-fields":
				c.Version = "v2"
			case "unknown-version":
				c.State = ""
				c.Version = "unpublished"
			case "wrong-gateway":
				f.room = strings.Replace(f.room, "shared-test", "production", 1)
			case "duplicate-key":
				f.room = strings.Replace(f.room, "gatewayRef: shared-test", "gatewayRef: shared-test\n  gatewayRef: production", 1)
			case "yaml-alias":
				f.room += "extra: &x [one]\nalias: *x\n"
			case "multiple-documents":
				f.room += "---\nkind: Secret\n"
			}
			if _, err := f.service.Apply(context.Background(), room, "owner", c); err == nil {
				t.Fatal("unsafe command accepted")
			}
			if f.posts != 0 || f.patches != 0 {
				t.Fatal("validation wrote Git")
			}
		})
	}
}
func TestNoCredentialsMeansReadOnly(t *testing.T) {
	f := newFixture(t)
	f.service.GitHub.Tokens = nil
	_, err := f.service.Apply(context.Background(), "one", "owner", request())
	if !errors.Is(err, ErrCredentials) || len(f.methods) != 0 {
		t.Fatal("missing credentials did not fail closed")
	}
}
func TestGitHubErrorsDoNotExposeResponseBodies(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("PRIVATE RESPONSE CONTENT"))
	}))
	defer s.Close()
	g := NewGitHub(nil)
	g.baseURL = s.URL
	err := g.call(context.Background(), "GET", "/", nil, nil)
	if err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("response body exposed")
	}
}
