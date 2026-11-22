package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bingops-com/portal/internal/auth"
	"github.com/bingops-com/portal/internal/gaming"
)

func TestGameCommandsAlwaysRequireLogin(t *testing.T) {
	s := &Server{Games: &gaming.Service{}}
	for _, route := range []string{"/api/games/one/changes", "/api/games/one/preview"} {
		r := httptest.NewRequest("POST", route, strings.NewReader(`{"state":"Running"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("anonymous command returned %d", w.Code)
		}
	}
}

func TestGameCommandsKeepCrossSiteAndReadOnlyProtection(t *testing.T) {
	dir := t.TempDir()
	key := []byte(strings.Repeat("k", 32))
	if err := os.WriteFile(filepath.Join(dir, "session.key"), key, 0600); err != nil {
		t.Fatal(err)
	}
	login, err := auth.New(auth.Config{Issuer: "https://auth.example.invalid", ClientID: "test", Group: "owners", PublicURL: "http://portal.test"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"v": auth.User{Name: "owner"}, "exp": time.Now().Add(time.Hour).Unix()})
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	cookie := base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	for _, mode := range []string{"cross-site", "read-only"} {
		s := &Server{Games: &gaming.Service{}, Auth: login, ReadOnly: mode == "read-only"}
		r := httptest.NewRequest("POST", "http://portal.test/api/games/one/changes", strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: "portal_session", Value: cookie})
		r.Header.Set("Content-Type", "application/json")
		if mode == "cross-site" {
			r.Header.Set("Origin", "https://another.example.invalid")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s returned %d", mode, w.Code)
		}
	}
}
func TestUnconfiguredGameManagementHasNoWriteRoute(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/games/one/changes", nil))
	if w.Code != 404 {
		t.Fatalf("unconfigured route returned %d", w.Code)
	}
}
