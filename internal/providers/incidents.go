package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Incident is one stretch during which a header readout was degraded.
type Incident struct {
	ID     int        `json:"id"`
	Label  string     `json:"label"`
	Type   string     `json:"type"`
	State  string     `json:"state"` // worst state reached: warn | down
	Detail string     `json:"detail"`
	Opened time.Time  `json:"opened"`
	Closed *time.Time `json:"closed,omitempty"`
	// Suspects are the changes seen shortly before the incident opened.
	Suspects []ActivityItem `json:"suspects,omitempty"`
}

// IncidentLog records incidents from readout transitions and keeps them on
// disk. Nothing is typed in by hand.
type IncidentLog struct {
	mu     sync.Mutex
	path   string
	items  []Incident
	nextID int
}

// Incidents is the log fed by the server's readout watcher.
var Incidents = &IncidentLog{}

const (
	maxIncidents  = 300
	suspectWindow = 30 * time.Minute
)

// SetPath loads the log from path and persists later changes there.
func (l *IncidentLog) SetPath(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.path, l.items, l.nextID = path, nil, 0
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if json.Unmarshal(raw, &l.items) != nil {
		l.items = nil
	}
	for _, it := range l.items {
		if it.ID > l.nextID {
			l.nextID = it.ID
		}
	}
}

func (l *IncidentLog) save() {
	if l.path == "" {
		return
	}
	raw, err := json.Marshal(l.items)
	if err != nil || os.MkdirAll(filepath.Dir(l.path), 0o755) != nil {
		return
	}
	tmp := l.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		os.Rename(tmp, l.path)
	}
}

// Observe applies the current state of a readout: a degraded readout opens an
// incident (or worsens the open one), a healthy one closes it. An unknown
// state, a source that cannot be read, changes nothing. suspects, when set, is
// called once in the background for a new incident.
func (l *IncidentLog) Observe(item SummaryItem, suspects func() []ActivityItem) {
	// An approaching deadline is a reminder, not something that broke.
	if item.Type == "deadlines" {
		return
	}
	l.mu.Lock()
	open := -1
	for i := range l.items {
		if l.items[i].Label == item.Label && l.items[i].Closed == nil {
			open = i
		}
	}
	bad := item.State == "warn" || item.State == "down"
	changed, opened := false, 0
	switch {
	case bad && open < 0:
		l.nextID++
		l.items = append(l.items, Incident{ID: l.nextID, Label: item.Label, Type: item.Type, State: item.State, Detail: item.Detail, Opened: time.Now()})
		if len(l.items) > maxIncidents {
			l.items = l.items[len(l.items)-maxIncidents:]
		}
		changed, opened = true, l.nextID
	case bad && item.State == "down" && l.items[open].State != "down":
		l.items[open].State, l.items[open].Detail, changed = "down", item.Detail, true
	case item.State == "ok" && open >= 0:
		now := time.Now()
		l.items[open].Closed, changed = &now, true
	}
	if changed {
		l.save()
	}
	l.mu.Unlock()

	if opened > 0 && suspects != nil {
		go func() {
			found := suspects()
			l.mu.Lock()
			defer l.mu.Unlock()
			for i := range l.items {
				if l.items[i].ID == opened {
					l.items[i].Suspects = found
					l.save()
				}
			}
		}()
	}
}

// RecentChanges lists what the activity sources report in the window before
// now, alerts excluded: it is what "changed" that may explain an incident.
func RecentChanges(ctx context.Context, d *Deps, opts map[string]any) []ActivityItem {
	scoped := map[string]any{"hours": 1, "limit": 40}
	for _, k := range []string{"context", "cronjobs"} {
		if v, ok := opts[k]; ok {
			scoped[k] = v
		}
	}
	out, err := fetchActivity(ctx, d, scoped)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-suspectWindow)
	var changes []ActivityItem
	for _, it := range out.(map[string]any)["items"].([]ActivityItem) {
		if it.Time.After(cutoff) && it.Kind != "alert" {
			changes = append(changes, it)
		}
	}
	return changes
}

func (l *IncidentLog) snapshot() []Incident {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Incident(nil), l.items...)
}

func fetchIncidents(_ context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Days  int `json:"days"`
		Limit int `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	days := clamp(o.Days, 30, 1, 365)
	since := time.Now().AddDate(0, 0, -days)
	open, recent := []Incident{}, []Incident{}
	var total time.Duration
	count := 0
	for _, it := range Incidents.snapshot() {
		end := time.Now()
		if it.Closed != nil {
			end = *it.Closed
		}
		if end.Before(since) {
			continue
		}
		count++
		total += end.Sub(it.Opened)
		if it.Closed == nil {
			open = append(open, it)
		} else {
			recent = append(recent, it)
		}
	}
	sort.SliceStable(open, func(a, b int) bool { return open[a].Opened.After(open[b].Opened) })
	sort.SliceStable(recent, func(a, b int) bool { return recent[a].Closed.After(*recent[b].Closed) })
	if limit := clamp(o.Limit, 6, 1, 50); len(recent) > limit {
		recent = recent[:limit]
	}
	return map[string]any{"open": open, "recent": recent, "days": days, "count": count, "minutes": int(total.Minutes())}, nil
}

func incidentDetail(_ context.Context, _ *Deps, _ map[string]any, q url.Values) (*Detail, error) {
	id, _ := strconv.Atoi(q.Get("id"))
	for _, it := range Incidents.snapshot() {
		if it.ID != id {
			continue
		}
		out := &Detail{Facts: []Fact{{"Indicateur", it.Label}, {"Constat", it.Detail}, {"Début", stamp(&it.Opened)}, {"Fin", stamp(it.Closed)}}}
		sec := Section{Title: "Changements dans la demi-heure précédente", Empty: "Aucun déploiement, redémarrage ni sauvegarde relevé juste avant.", Rows: []DetailRow{}}
		for _, s := range it.Suspects {
			t := s.Time
			sec.Rows = append(sec.Rows, DetailRow{State: s.State, Title: s.Title, Text: s.Detail, Time: &t})
		}
		out.Sections = []Section{sec}
		return out, nil
	}
	return nil, errors.New("incident introuvable")
}

func init() {
	Registry["incidents"] = Provider{TTL: 15 * time.Second, Fetch: fetchIncidents}
	Details["incidents"] = incidentDetail
}
