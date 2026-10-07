// Package auth protects layout editing with an OpenID Connect login
// (authorization code flow with PKCE, public client: no client secret).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "portal_session"
	loginCookie   = "portal_login"
	sessionTTL    = 12 * time.Hour
	loginTTL      = 10 * time.Minute
)

type Config struct {
	Issuer    string // e.g. https://auth.example.com/application/o/portal/
	ClientID  string
	Group     string // optional: only members of this group may edit
	PublicURL string // external URL of the portal, e.g. https://lab.example.com
}

type User struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type endpoints struct {
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	Userinfo      string `json:"userinfo_endpoint"`
}

type Auth struct {
	cfg    Config
	key    []byte
	secure bool
	client *http.Client

	mu        sync.Mutex
	endpoints *endpoints
}

// New prepares the login flow. The cookie signing key is created once and
// kept in dataDir, so sessions survive restarts.
func New(cfg Config, dataDir string) (*Auth, error) {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	public, err := url.Parse(cfg.PublicURL)
	if err != nil || public.Host == "" || cfg.ClientID == "" || cfg.Issuer == "" {
		return nil, errors.New("PORTAL_OIDC_ISSUER, PORTAL_OIDC_CLIENT_ID and PORTAL_PUBLIC_URL are all required to enable login")
	}
	key, err := loadKey(filepath.Join(dataDir, "session.key"))
	if err != nil {
		return nil, err
	}
	return &Auth{cfg: cfg, key: key, secure: public.Scheme == "https", client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func loadKey(path string) ([]byte, error) {
	if key, err := os.ReadFile(path); err == nil && len(key) == 32 {
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return key, os.WriteFile(path, key, 0o600)
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// seal serializes v with an expiry and an HMAC; open verifies both.
func (a *Auth) seal(v any, ttl time.Duration) string {
	payload, _ := json.Marshal(struct {
		V   any   `json:"v"`
		Exp int64 `json:"exp"`
	}{v, time.Now().Add(ttl).Unix()})
	mac := hmac.New(sha256.New, a.key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) open(token string, out any) bool {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(body)
	got, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil {
		return false
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write(payload)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return false
	}
	var env struct {
		V   json.RawMessage `json:"v"`
		Exp int64           `json:"exp"`
	}
	if json.Unmarshal(payload, &env) != nil || time.Now().Unix() > env.Exp {
		return false
	}
	return json.Unmarshal(env.V, out) == nil
}

func (a *Auth) setCookie(w http.ResponseWriter, name, value, path string, ttl time.Duration) {
	c := &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())}
	if value == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// User returns the logged-in editor, or nil.
func (a *Auth) User(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	var u User
	if !a.open(c.Value, &u) || u.Name == "" {
		return nil
	}
	return &u
}

func (a *Auth) discover(ctx context.Context) (*endpoints, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.endpoints != nil {
		return a.endpoints, nil
	}
	var e endpoints
	if err := a.getJSON(ctx, strings.TrimRight(a.cfg.Issuer, "/")+"/.well-known/openid-configuration", "", &e); err != nil {
		return nil, err
	}
	if e.Authorization == "" || e.Token == "" || e.Userinfo == "" {
		return nil, errors.New("incomplete OpenID configuration")
	}
	a.endpoints = &e
	return &e, nil
}

func (a *Auth) getJSON(ctx context.Context, rawurl, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return a.do(req, out)
}

func (a *Auth) do(req *http.Request, out any) error {
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s answered %d", req.URL.Host, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

// safeReturn keeps post-login redirects on the portal itself.
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n") {
		return "/"
	}
	return p
}

type loginState struct {
	State    string `json:"s"`
	Verifier string `json:"p"`
	Return   string `json:"r"`
}

func (a *Auth) fail(w http.ResponseWriter, status int, msg string, err error) {
	if err != nil {
		slog.Warn("login failed", "reason", msg, "error", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="fr"><meta charset="utf-8"><title>Connexion impossible</title>`+
		`<body style="font:16px system-ui;max-width:34rem;margin:18vh auto;padding:0 1.25rem">`+
		`<h1 style="font-size:1.5rem">Connexion impossible</h1><p>%s</p><p><a href="/">Revenir au portail</a></p>`, msg)
}

// Login starts the authorization code flow.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	e, err := a.discover(r.Context())
	if err != nil {
		a.fail(w, http.StatusBadGateway, "Le fournisseur d’identité ne répond pas. Réessayez dans un instant.", err)
		return
	}
	st := loginState{State: randomToken(), Verifier: randomToken(), Return: safeReturn(r.URL.Query().Get("return"))}
	challenge := sha256.Sum256([]byte(st.Verifier))
	a.setCookie(w, loginCookie, a.seal(st, loginTTL), "/auth", loginTTL)
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {a.cfg.ClientID},
		"redirect_uri":          {a.cfg.PublicURL + "/auth/callback"},
		"scope":                 {"openid profile email"},
		"state":                 {st.State},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(e.Authorization, "?") {
		sep = "&"
	}
	http.Redirect(w, r, e.Authorization+sep+q.Encode(), http.StatusFound)
}

// Callback finishes the flow and opens the editing session.
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	var st loginState
	c, err := r.Cookie(loginCookie)
	if err != nil || !a.open(c.Value, &st) || st.State == "" || !hmac.Equal([]byte(st.State), []byte(r.URL.Query().Get("state"))) {
		a.fail(w, http.StatusBadRequest, "La demande de connexion a expiré. Relancez-la depuis le portail.", nil)
		return
	}
	a.setCookie(w, loginCookie, "", "/auth", 0)
	code := r.URL.Query().Get("code")
	if code == "" {
		a.fail(w, http.StatusBadRequest, "Le fournisseur d’identité a refusé la connexion.", errors.New(r.URL.Query().Get("error")))
		return
	}
	e, err := a.discover(r.Context())
	if err != nil {
		a.fail(w, http.StatusBadGateway, "Le fournisseur d’identité ne répond pas. Réessayez dans un instant.", err)
		return
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {a.cfg.PublicURL + "/auth/callback"},
		"client_id":     {a.cfg.ClientID},
		"code_verifier": {st.Verifier},
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, e.Token, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := a.do(req, &token); err != nil || token.AccessToken == "" {
		a.fail(w, http.StatusBadGateway, "L’échange avec le fournisseur d’identité a échoué.", err)
		return
	}
	// The access token comes straight from the token endpoint over TLS, so
	// the userinfo answer is trusted without verifying an ID token signature.
	var info struct {
		Username string   `json:"preferred_username"`
		Name     string   `json:"name"`
		Email    string   `json:"email"`
		Groups   []string `json:"groups"`
	}
	if err := a.getJSON(r.Context(), e.Userinfo, token.AccessToken, &info); err != nil {
		a.fail(w, http.StatusBadGateway, "Impossible de lire votre profil auprès du fournisseur d’identité.", err)
		return
	}
	user := User{Name: info.Name, Email: info.Email}
	if user.Name == "" {
		user.Name = info.Username
	}
	if user.Name == "" {
		user.Name = info.Email
	}
	if user.Name == "" {
		a.fail(w, http.StatusForbidden, "Votre profil ne contient ni nom ni adresse e-mail.", nil)
		return
	}
	if a.cfg.Group != "" {
		member := false
		for _, g := range info.Groups {
			member = member || g == a.cfg.Group
		}
		if !member {
			a.fail(w, http.StatusForbidden, fmt.Sprintf("Votre compte n’appartient pas au groupe « %s », nécessaire pour modifier le portail.", a.cfg.Group), nil)
			return
		}
	}
	a.setCookie(w, sessionCookie, a.seal(user, sessionTTL), "/", sessionTTL)
	slog.Info("editor logged in", "user", user.Name)
	http.Redirect(w, r, st.Return, http.StatusFound)
}

// Logout closes the editing session on the portal (not at the provider).
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	a.setCookie(w, sessionCookie, "", "/", 0)
	http.Redirect(w, r, "/", http.StatusFound)
}
