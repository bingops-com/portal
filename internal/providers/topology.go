package providers

import (
	"context"
	"errors"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// --- Lab map: services, what they depend on, and their live state --------

type topoNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Layer int    `json:"layer"`
	Note  string `json:"note,omitempty"`
	State string `json:"state"` // ok | warn | down | none
}

type topoLink struct {
	From  string `json:"from"`
	To    string `json:"to"`
	State string `json:"state"`
}

var stateRank = map[string]int{"none": 0, "ok": 1, "unknown": 2, "warn": 3, "down": 4}

func fetchTopology(ctx context.Context, d *Deps, opts map[string]any) (any, error) {
	var o struct {
		Context string `json:"context"`
		Gatus   string `json:"gatus"`
		Nodes   []struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			Layer    int    `json:"layer"`
			Note     string `json:"note"`
			App      string `json:"app"`      // Argo CD Application
			Endpoint string `json:"endpoint"` // Gatus endpoint name
		} `json:"nodes"`
		Links []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"links"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Nodes) == 0 {
		return nil, errors.New("décrivez les éléments du lab dans les réglages du widget")
	}
	if len(o.Nodes) > 60 {
		o.Nodes = o.Nodes[:60]
	}

	apps := map[string]string{}
	needApps, needGatus := false, false
	for _, n := range o.Nodes {
		needApps = needApps || n.App != ""
		needGatus = needGatus || n.Endpoint != ""
	}
	if needApps {
		c, err := d.Kube.get(o.Context)
		if err != nil {
			return nil, err
		}
		list, err := c.dynamic.Resource(argoApps).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, kubeErr(err)
		}
		for i := range list.Items {
			u := &list.Items[i]
			sync, _, _ := unstructured.NestedString(u.Object, "status", "sync", "status")
			health, _, _ := unstructured.NestedString(u.Object, "status", "health", "status")
			state := "warn"
			switch {
			case health == "Degraded" || health == "Missing":
				state = "down"
			case health == "Healthy" && sync == "Synced":
				state = "ok"
			}
			apps[u.GetName()] = state
		}
	}
	endpoints := map[string]string{}
	if needGatus && o.Gatus != "" {
		var raw []struct {
			Name    string `json:"name"`
			Results []struct {
				Success bool `json:"success"`
			} `json:"results"`
		}
		// A map with one unreadable source still shows the rest.
		if getJSON(ctx, trimSlash(o.Gatus)+"/api/v1/endpoints/statuses", &raw) == nil {
			for _, e := range raw {
				state := "unknown"
				if n := len(e.Results); n > 0 {
					state = "down"
					if e.Results[n-1].Success {
						state = "ok"
					}
				}
				endpoints[e.Name] = state
			}
		}
	}

	nodes := make([]topoNode, 0, len(o.Nodes))
	states := map[string]string{}
	for _, n := range o.Nodes {
		id := strings.TrimSpace(n.ID)
		if id == "" {
			continue
		}
		state := "none"
		if n.App != "" {
			state = "unknown"
			if s, ok := apps[n.App]; ok {
				state = s
			}
		}
		if n.Endpoint != "" {
			s, ok := endpoints[n.Endpoint]
			if !ok {
				s = "unknown"
			}
			if stateRank[s] > stateRank[state] {
				state = s
			}
		}
		label := n.Label
		if label == "" {
			label = id
		}
		states[id] = state
		nodes = append(nodes, topoNode{id, label, n.Layer, n.Note, state})
	}
	links := []topoLink{}
	for _, l := range o.Links {
		from, okFrom := states[l.From]
		to, okTo := states[l.To]
		if !okFrom || !okTo {
			continue
		}
		// A link carries traffic only as well as its worse end.
		state := from
		if stateRank[to] > stateRank[state] {
			state = to
		}
		links = append(links, topoLink{l.From, l.To, state})
	}
	return map[string]any{"nodes": nodes, "links": links}, nil
}

func init() {
	Registry["topology"] = Provider{TTL: 30 * time.Second, Fetch: fetchTopology}
}
