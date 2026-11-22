package gaming

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAppJWTNarrowedPermissionsAndTokenCache(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	app, err := NewAppTokens(123, 456, "only-this-repo", keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/app/installations/456/access_tokens" {
			t.Error("wrong installation")
		}
		jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			t.Fatal("invalid JWT")
		}
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig) != nil {
			t.Error("invalid JWT signature")
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claim map[string]any
		if json.Unmarshal(claims, &claim) != nil || claim["iss"] != "123" {
			t.Error("invalid JWT claims")
		}
		var body struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Repositories) != 1 || body.Repositories[0] != "only-this-repo" || len(body.Permissions) != 2 || body.Permissions["contents"] != "write" || body.Permissions["pull_requests"] != "write" {
			t.Error("token permissions not narrowed")
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"token": "fake-installation-token", "expires_at": time.Now().Add(time.Hour)})
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	app.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.github.com" {
			t.Error("unexpected credential destination")
		}
		clone := req.Clone(req.Context())
		u := *req.URL
		clone.URL = &u
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		return http.DefaultTransport.RoundTrip(clone)
	})}
	for i := 0; i < 2; i++ {
		token, err := app.Token(context.Background())
		if err != nil || token != "fake-installation-token" {
			t.Fatalf("token error %v", err)
		}
	}
	if calls != 1 {
		t.Fatal("valid token was not cached")
	}
	app.expires = time.Now().Add(-time.Minute)
	if _, err := app.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("expired token was reused")
	}
}
