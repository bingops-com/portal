// Package providers fetches the data behind each server-side widget type.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
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
	// Type is the widget type behind the readout; Since is when the state
	// last changed, when the server has seen it change.
	Type  string     `json:"type,omitempty"`
	Since *time.Time `json:"since,omitempty"`
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
	"activity":     {TTL: 45 * time.Second, Fetch: fetchActivity},
	"gameserver":   {TTL: 30 * time.Second, Fetch: fetchGameServer},
	"releases":     {TTL: time.Hour, Fetch: fetchReleases},
	"pulls":        {TTL: 10 * time.Minute, Fetch: fetchPulls},
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

// internalHosts, when set, is the only way to reach private addresses: a
// request whose host is not listed may only connect to public IPs. This keeps
// an editor from pointing a widget at arbitrary services inside the network.
var internalHosts []string

// SetInternalHosts enables the private-address guard. Entries are exact
// hostnames or suffixes starting with a dot (".svc.cluster.local").
func SetInternalHosts(hosts []string) {
	internalHosts = nil
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			internalHosts = append(internalHosts, h)
		}
	}
}

func internalAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range internalHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			return true
		}
	}
	return false
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

type hostKey struct{}

// forHost records in the context whether the URL's host may reach private
// addresses; the dialer enforces it on the resolved IP, redirects included.
func forHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, hostKey{}, internalAllowed(host))
}

// guard refuses a connection to a private address unless the request's host
// was explicitly allowed.
func guard(ctx context.Context, address string) error {
	if internalHosts == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil && privateIP(ip) {
		if ok, _ := ctx.Value(hostKey{}).(bool); !ok {
			return errors.New("adresse interne non autorisée (PORTAL_INTERNAL_HOSTS)")
		}
	}
	return nil
}

var httpClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: 8 * time.Second,
			ControlContext: func(ctx context.Context, _, address string, _ syscall.RawConn) error {
				return guard(ctx, address)
			},
		}).DialContext,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
	},
}

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

func getBytes(ctx context.Context, rawurl string, headers ...string) ([]byte, error) {
	if err := checkURL(rawurl); err != nil {
		return nil, err
	}
	u, _ := url.Parse(rawurl)
	req, err := http.NewRequestWithContext(forHost(ctx, u.Hostname()), http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
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

func getJSON(ctx context.Context, rawurl string, out any, headers ...string) error {
	body, err := getBytes(ctx, rawurl, headers...)
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
