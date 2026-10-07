// Package server exposes the portal API and serves the embedded web UI.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/bingops-com/portal/internal/auth"
	"github.com/bingops-com/portal/internal/cache"
	"github.com/bingops-com/portal/internal/config"
	"github.com/bingops-com/portal/internal/providers"
)

const fetchTimeout = 25 * time.Second

type Server struct {
	Store    *config.Store
	Deps     *providers.Deps
	Cache    *cache.Cache
	Assets   fs.FS
	ReadOnly bool
	// Auth, when set, requires a login for every write. Reads stay open.
	Auth *auth.Auth
}

type dataResponse struct {
	Data      any        `json:"data,omitempty"`
	Error     string     `json:"error,omitempty"`
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	Stale     bool       `json:"stale,omitempty"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/layout", s.guard(s.putLayout))
	mux.HandleFunc("DELETE /api/layout", s.guard(s.deleteLayout))
	mux.HandleFunc("GET /api/data/{id}", s.getData)
	mux.HandleFunc("POST /api/preview", s.guard(s.postPreview))
	mux.HandleFunc("GET /api/summary", s.getSummary)
	if s.Auth != nil {
		mux.HandleFunc("GET /auth/login", s.Auth.Login)
		mux.HandleFunc("GET /auth/callback", s.Auth.Callback)
		mux.HandleFunc("GET /auth/logout", s.Auth.Logout)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "route inconnue") })
	mux.Handle("/", s.static())
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// guard protects state-changing routes. The portal has no login, so it
// refuses cross-site requests: another website open in the operator's browser
// must not be able to rewrite the layout or trigger fetches.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ReadOnly {
			writeError(w, http.StatusForbidden, "le portail est en lecture seule (PORTAL_READONLY)")
			return
		}
		if s.Auth != nil && s.Auth.User(r) == nil {
			writeError(w, http.StatusUnauthorized, "connexion requise pour modifier le portail")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "requête inter-site refusée")
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			writeError(w, http.StatusForbidden, "requête inter-site refusée")
			return
		}
		if r.Method != http.MethodDelete && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "application/json attendu")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next(w, r)
	}
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, source, err := s.Store.Effective()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{
		"title": cfg.Title, "theme": cfg.Theme, "pages": cfg.Pages, "source": source, "readOnly": s.ReadOnly,
		"loginRequired": s.Auth != nil,
	}
	if s.Auth != nil {
		if u := s.Auth.User(r); u != nil {
			out["user"] = u.Name
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putLayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pages []config.Page `json:"pages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "JSON invalide")
		return
	}
	if err := s.Store.SaveLayout(body.Pages); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.getConfig(w, r)
}

func (s *Server) deleteLayout(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.ResetLayout(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.getConfig(w, r)
}

func cacheKey(typ string, opts map[string]any) string {
	raw, _ := json.Marshal(opts)
	sum := sha256.Sum256(raw)
	return typ + ":" + hex.EncodeToString(sum[:12])
}

func (s *Server) fetch(typ string, opts map[string]any) cache.Result {
	p, ok := providers.Registry[typ]
	if !ok {
		return cache.Result{Err: errors.New("ce widget n'a pas de données serveur")}
	}
	return s.Cache.Get(cacheKey(typ, opts), p.TTL, func() (any, error) {
		// Detached from the request so one closed tab does not fail the
		// fetch other viewers are waiting on.
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		started := time.Now()
		val, err := p.Fetch(ctx, s.Deps, opts)
		if err != nil {
			slog.Warn("widget fetch failed", "type", typ, "error", err, "duration", time.Since(started).Round(time.Millisecond))
		}
		return val, err
	})
}

func toResponse(res cache.Result) dataResponse {
	out := dataResponse{Data: res.Value, Stale: res.Stale}
	if res.Err != nil {
		out.Error = res.Err.Error()
	}
	if !res.FetchedAt.IsZero() {
		out.FetchedAt = &res.FetchedAt
	}
	return out
}

func (s *Server) getData(w http.ResponseWriter, r *http.Request) {
	cfg, _, err := s.Store.Effective()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := r.PathValue("id")
	for _, p := range cfg.Pages {
		for _, c := range p.Columns {
			for _, wd := range c.Widgets {
				if wd.ID == id {
					writeJSON(w, http.StatusOK, toResponse(s.fetch(wd.Type, wd.Options)))
					return
				}
			}
		}
	}
	writeError(w, http.StatusNotFound, "widget introuvable")
}

func (s *Server) postPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type    string         `json:"type"`
		Options map[string]any `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "JSON invalide")
		return
	}
	writeJSON(w, http.StatusOK, toResponse(s.fetch(body.Type, body.Options)))
}

// getSummary condenses every ops widget of the configuration into the
// header band readouts, reusing the widgets' cached results.
func (s *Server) getSummary(w http.ResponseWriter, _ *http.Request) {
	cfg, _, err := s.Store.Effective()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type source struct {
		typ  string
		opts map[string]any
	}
	var sources []source
	seen := map[string]bool{}
	for _, p := range cfg.Pages {
		for _, c := range p.Columns {
			for _, wd := range c.Widgets {
				if _, ok := providers.SummaryLabels[wd.Type]; !ok {
					continue
				}
				if key := cacheKey(wd.Type, wd.Options); !seen[key] {
					seen[key] = true
					sources = append(sources, source{wd.Type, wd.Options})
				}
			}
		}
	}
	items := make([]providers.SummaryItem, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := s.fetch(src.typ, src.opts)
			if sum, ok := res.Value.(providers.Summarizer); ok && res.Err == nil {
				items[i] = sum.Summary()
				return
			}
			items[i] = providers.SummaryItem{Label: providers.SummaryLabels[src.typ], State: "unknown", Detail: "source indisponible"}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// static serves the built UI, falling back to index.html for page routes.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.Assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(s.Assets, name); err != nil {
			name = "index.html"
			r.URL.Path = "/"
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
