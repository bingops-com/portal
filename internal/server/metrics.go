package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// fetchStats counts provider fetches per widget type for /metrics.
type fetchStats struct {
	mu   sync.Mutex
	rows map[string]*fetchRow
}

type fetchRow struct {
	ok, failed int64
	seconds    float64
}

func (f *fetchStats) record(typ string, took time.Duration, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]*fetchRow{}
	}
	r := f.rows[typ]
	if r == nil {
		r = &fetchRow{}
		f.rows[typ] = r
	}
	if err != nil {
		r.failed++
	} else {
		r.ok++
	}
	r.seconds += took.Seconds()
}

var stateValues = map[string]int{"ok": 0, "unknown": 1, "warn": 2, "down": 3}

// getMetrics exposes the portal's own health in the Prometheus text format:
// how its sources behave, and the state of each header readout.
func (s *Server) getMetrics(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	b.WriteString("# HELP portal_fetch_total Provider fetches by widget type and result.\n# TYPE portal_fetch_total counter\n")
	s.stats.mu.Lock()
	types := make([]string, 0, len(s.stats.rows))
	for t := range s.stats.rows {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		r := s.stats.rows[t]
		fmt.Fprintf(&b, "portal_fetch_total{type=%q,result=\"ok\"} %d\nportal_fetch_total{type=%q,result=\"error\"} %d\n", t, r.ok, t, r.failed)
	}
	b.WriteString("# HELP portal_fetch_duration_seconds_sum Time spent in provider fetches.\n# TYPE portal_fetch_duration_seconds_sum counter\n")
	for _, t := range types {
		fmt.Fprintf(&b, "portal_fetch_duration_seconds_sum{type=%q} %g\n", t, s.stats.rows[t].seconds)
	}
	s.stats.mu.Unlock()

	b.WriteString("# HELP portal_readout_state Header readout state: 0 ok, 1 unknown, 2 warn, 3 down.\n# TYPE portal_readout_state gauge\n")
	s.transitionsMu.Lock()
	labels := make([]string, 0, len(s.transitions))
	for l := range s.transitions {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		fmt.Fprintf(&b, "portal_readout_state{readout=%q} %d\n", l, stateValues[s.transitions[l].state])
	}
	s.transitionsMu.Unlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(b.String()))
}

type savedTransition struct {
	State string     `json:"state"`
	Since *time.Time `json:"since,omitempty"`
}

// loadTransitions restores readout states and their change dates, so a
// restart does not forget since when something has been wrong.
func (s *Server) loadTransitions() {
	if s.StatePath == "" {
		return
	}
	raw, err := os.ReadFile(s.StatePath)
	if err != nil {
		return
	}
	var saved map[string]savedTransition
	if json.Unmarshal(raw, &saved) != nil {
		return
	}
	s.transitions = map[string]transition{}
	for label, t := range saved {
		s.transitions[label] = transition{state: t.State, since: t.Since}
	}
}

// saveTransitions is called with transitionsMu held.
func (s *Server) saveTransitions() {
	if s.StatePath == "" {
		return
	}
	saved := map[string]savedTransition{}
	for label, t := range s.transitions {
		saved[label] = savedTransition{t.state, t.since}
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(s.StatePath), 0o755) != nil {
		return
	}
	tmp := s.StatePath + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		os.Rename(tmp, s.StatePath)
	}
}
