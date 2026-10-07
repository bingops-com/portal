package auth

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// provider is a minimal OpenID Connect provider checking the PKCE exchange.
type provider struct {
	*httptest.Server
	challenge string
	groups    []string
}

func newProvider(t *testing.T, groups ...string) *provider {
	p := &provider{groups: groups}
	mux := http.NewServeMux()
	mux.HandleFunc("/o/portal/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token", "userinfo_endpoint": p.URL + "/userinfo",
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != "portal" || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		p.challenge = q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=abc&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "abc" || r.Form.Get("code_verifier") == "" || p.challenge == "" {
			http.Error(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"preferred_username": "bingo", "email": "b@example.com", "groups": p.groups})
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// portal serves the login routes plus /whoami, and returns a browser-like
// client that keeps cookies and follows redirects.
func portal(t *testing.T, idp *provider, group string) (*Auth, *httptest.Server, *http.Client) {
	t.Helper()
	var a *Auth
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a, err := New(Config{Issuer: idp.URL + "/o/portal/", ClientID: "portal", Group: group, PublicURL: srv.URL}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("GET /auth/login", a.Login)
	mux.HandleFunc("GET /auth/callback", a.Callback)
	mux.HandleFunc("GET /auth/logout", a.Logout)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if u := a.User(r); u != nil {
			w.Write([]byte("user=" + u.Name + " path=" + r.URL.Path))
			return
		}
		w.Write([]byte("anonymous path=" + r.URL.Path))
	})
	jar, _ := cookiejar.New(nil)
	return a, srv, &http.Client{Jar: jar}
}

func body(t *testing.T, c *http.Client, target string) (int, string) {
	t.Helper()
	resp, err := c.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func TestLoginFlowOpensASessionAndReturnsToThePage(t *testing.T) {
	idp := newProvider(t, "portal-editors")
	_, srv, client := portal(t, idp, "portal-editors")

	if _, got := body(t, client, srv.URL+"/veille"); got != "anonymous path=/veille" {
		t.Fatalf("before login: %q", got)
	}
	status, got := body(t, client, srv.URL+"/auth/login?return=/veille")
	if status != http.StatusOK || got != "user=bingo path=/veille" {
		t.Fatalf("after login: %d %q", status, got)
	}
	if _, got := body(t, client, srv.URL+"/auth/logout"); got != "anonymous path=/" {
		t.Fatalf("after logout: %q", got)
	}
}

func TestLoginRefusesUsersOutsideTheGroup(t *testing.T) {
	idp := newProvider(t, "someone-else")
	_, srv, client := portal(t, idp, "portal-editors")
	status, got := body(t, client, srv.URL+"/auth/login")
	if status != http.StatusForbidden || !strings.Contains(got, "portal-editors") {
		t.Fatalf("%d %q", status, got)
	}
	if _, got := body(t, client, srv.URL+"/"); got != "anonymous path=/" {
		t.Fatalf("a refused user must not get a session: %q", got)
	}
}

func TestCallbackRejectsForgedOrMissingState(t *testing.T) {
	idp := newProvider(t)
	_, srv, client := portal(t, idp, "")
	if status, _ := body(t, client, srv.URL+"/auth/callback?code=abc&state=forged"); status != http.StatusBadRequest {
		t.Fatalf("callback without a login cookie: %d", status)
	}
	if _, got := body(t, client, srv.URL+"/"); got != "anonymous path=/" {
		t.Fatalf("%q", got)
	}
}

func TestSessionCookieCannotBeForged(t *testing.T) {
	idp := newProvider(t)
	a, _, _ := portal(t, idp, "")
	other, _, _ := portal(t, idp, "")

	valid := a.seal(User{Name: "bingo"}, sessionTTL)
	request := func(value string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
		return r
	}
	if u := a.User(request(valid)); u == nil || u.Name != "bingo" {
		t.Fatalf("valid session rejected: %+v", u)
	}
	payload, sig, _ := strings.Cut(valid, ".")
	for name, value := range map[string]string{
		"other key":        other.seal(User{Name: "bingo"}, sessionTTL),
		"expired":          a.seal(User{Name: "bingo"}, -time.Minute),
		"tampered":         payload + "x." + sig,
		"no signature":     payload,
		"empty":            "",
		"login as session": a.seal(loginState{State: "s"}, sessionTTL),
	} {
		if u := a.User(request(value)); u != nil {
			t.Errorf("%s: accepted as %+v", name, u)
		}
	}
}

func TestSafeReturnStaysOnThePortal(t *testing.T) {
	for in, want := range map[string]string{
		"/veille": "/veille", "": "/", "https://evil.example": "/", "//evil.example": "/", "/\\evil": "/", "veille": "/",
	} {
		if got := safeReturn(in); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewRequiresCompleteConfiguration(t *testing.T) {
	if _, err := New(Config{Issuer: "https://idp.example/"}, t.TempDir()); err == nil {
		t.Fatal("expected an error")
	}
}
