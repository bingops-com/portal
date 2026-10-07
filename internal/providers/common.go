// Package providers fetches the data behind each server-side widget type.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Provider describes how to load one widget type and how long to cache it.
type Provider struct {
	TTL   time.Duration
	Fetch func(ctx context.Context, d *Deps, opts map[string]any) (any, error)
}

// Deps carries the shared clients providers need.
type Deps struct {
	Kube *KubeClients
}

// SummaryItem is one health readout of the header band.
type SummaryItem struct {
	Label  string `json:"label"`
	State  string `json:"state"` // ok | warn | down | unknown
	Detail string `json:"detail"`
}

// Summarizer is implemented by ops results that feed the header band.
type Summarizer interface {
	Summary() SummaryItem
}

// SummaryLabels names the header readout of each ops type, used when its
// provider fails and no result is available to summarize.
var SummaryLabels = map[string]string{
	"kubernetes":   "Cluster",
	"argocd":       "Argo CD",
	"gatus":        "Endpoints",
	"alerts":       "Alertes",
	"certificates": "Certificats",
	"backups":      "Sauvegardes",
}

var Registry = map[string]Provider{
	"kubernetes":   {TTL: 20 * time.Second, Fetch: fetchCluster},
	"workloads":    {TTL: 20 * time.Second, Fetch: fetchWorkloads},
	"events":       {TTL: 20 * time.Second, Fetch: fetchEvents},
	"argocd":       {TTL: 20 * time.Second, Fetch: fetchArgo},
	"gatus":        {TTL: 30 * time.Second, Fetch: fetchGatus},
	"prometheus":   {TTL: 30 * time.Second, Fetch: fetchPrometheus},
	"alerts":       {TTL: 30 * time.Second, Fetch: fetchAlerts},
	"certificates": {TTL: 5 * time.Minute, Fetch: fetchCertificates},
	"backups":      {TTL: 2 * time.Minute, Fetch: fetchBackups},
	"bookmarks":    {TTL: 45 * time.Second, Fetch: fetchBookmarks},
	"rss":          {TTL: 10 * time.Minute, Fetch: fetchRSS},
	"videos":       {TTL: 15 * time.Minute, Fetch: fetchVideos},
	"markets":      {TTL: 5 * time.Minute, Fetch: fetchMarkets},
	"weather":      {TTL: 15 * time.Minute, Fetch: fetchWeather},
	"calendar":     {TTL: 10 * time.Minute, Fetch: fetchCalendar},
	"hackernews":   {TTL: 10 * time.Minute, Fetch: fetchHackerNews},
	"reddit":       {TTL: 10 * time.Minute, Fetch: fetchReddit},
}

// ClientOnly lists widget types rendered entirely in the browser.
var ClientOnly = map[string]bool{"clock": true, "search": true}

func Known(t string) bool {
	_, ok := Registry[t]
	return ok || ClientOnly[t]
}

// envRef matches the only environment variables options may reference, so a
// saved layout cannot read arbitrary process environment.
var envRef = regexp.MustCompile(`\$\{(PORTAL_VAR_[A-Z0-9_]+)\}`)

func expand(v any) any {
	switch t := v.(type) {
	case string:
		return envRef.ReplaceAllStringFunc(t, func(m string) string {
			return os.Getenv(m[2 : len(m)-1])
		})
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = expand(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = expand(val)
		}
		return out
	}
	return v
}

// decode maps widget options onto a typed struct after expanding ${PORTAL_VAR_*}.
func decode(opts map[string]any, out any) error {
	if opts == nil {
		opts = map[string]any{}
	}
	raw, err := json.Marshal(expand(opts))
	if err != nil {
		return fmt.Errorf("options invalides: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("options invalides: %w", err)
	}
	return nil
}

// namedURL accepts either a bare URL string or {title, url}.
type namedURL struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (n *namedURL) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &n.URL)
	}
	type plain namedURL
	return json.Unmarshal(b, (*plain)(n))
}

const userAgent = "labops-portal/0.1 (+https://github.com/bingops-com)"

var httpClient = &http.Client{Timeout: 12 * time.Second}

func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("URL invalide %q (http ou https attendu)", redact(raw))
	}
	return nil
}

// redact keeps scheme and host only, so private feed tokens never reach the UI.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…"
	}
	return u.Scheme + "://" + u.Host
}

func getBytes(ctx context.Context, rawurl string) ([]byte, error) {
	if err := checkURL(rawurl); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := httpClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("%s injoignable: %w", redact(rawurl), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s a répondu %d", redact(rawurl), resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func getJSON(ctx context.Context, rawurl string, out any) error {
	body, err := getBytes(ctx, rawurl)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("réponse illisible de %s", redact(rawurl))
	}
	return nil
}

// parallel runs fn for each index with bounded concurrency.
func parallel(n, limit int, fn func(i int)) {
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}()
	}
	wg.Wait()
}

func clamp(v, def, min, max int) int {
	if v == 0 {
		v = def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func plural(n int, one, many string) string {
	if n > 1 {
		return fmt.Sprintf("%d %s", n, many)
	}
	return fmt.Sprintf("%d %s", n, one)
}

func trimSlash(s string) string { return strings.TrimRight(s, "/") }
