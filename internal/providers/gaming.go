package providers

import (
	"context"
	"errors"

	"github.com/bingops-com/portal/internal/gaming"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type GamingLive struct {
	Rooms        map[string]gaming.Observed `json:"rooms"`
	ArgoRevision string                     `json:"argoRevision"`
	ArgoSync     string                     `json:"argoSync"`
}

// ObserveGaming exposes only bounded status fields. No Secret/ConfigMap read
// and no Kubernetes mutation is needed by the portal.
func (d *Deps) ObserveGaming(ctx context.Context, cfg gaming.Config) (GamingLive, error) {
	out := GamingLive{Rooms: map[string]gaming.Observed{}}
	client, err := d.Kube.get("")
	if err != nil {
		return out, errors.New("état Kubernetes indisponible")
	}
	resource := schema.GroupVersionResource{Group: "gaming.bingops.com", Version: "v1alpha1", Resource: "gamerooms"}
	for _, name := range cfg.Rooms {
		room, err := client.dynamic.Resource(resource).Namespace(cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return out, errors.New("état Kubernetes indisponible")
		}
		if text, _, _ := unstructured.NestedString(room.Object, "spec", "gatewayRef"); text != cfg.Gateway {
			return out, errors.New("GameRoom observé hors du périmètre autorisé")
		}
		state, _, _ := unstructured.NestedString(room.Object, "spec", "state")
		version, _, _ := unstructured.NestedString(room.Object, "spec", "version")
		phase, _, _ := unstructured.NestedString(room.Object, "status", "phase")
		message, _, _ := unstructured.NestedString(room.Object, "status", "message")
		generation, _, _ := unstructured.NestedInt64(room.Object, "status", "observedGeneration")
		backup, _, _ := unstructured.NestedBool(room.Object, "status", "backup", "verified")
		out.Rooms[name] = gaming.Observed{Generation: room.GetGeneration(), ObservedGeneration: generation, State: state, Version: version, Phase: phase, Message: message, BackupVerified: backup}
	}
	app, err := client.dynamic.Resource(schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}).Namespace(cfg.ArgoNamespace).Get(ctx, cfg.ArgoApplication, metav1.GetOptions{})
	if err != nil {
		return out, errors.New("état Argo CD indisponible")
	}
	repo, _, _ := unstructured.NestedString(app.Object, "spec", "source", "repoURL")
	branch, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
	dir, _, _ := unstructured.NestedString(app.Object, "spec", "source", "path")
	if repo != "https://github.com/"+cfg.Repository+".git" || branch != cfg.Branch || dir != cfg.Directory {
		return out, errors.New("Application Argo CD hors du périmètre autorisé")
	}
	out.ArgoRevision, _, _ = unstructured.NestedString(app.Object, "status", "sync", "revision")
	out.ArgoSync, _, _ = unstructured.NestedString(app.Object, "status", "sync", "status")
	return out, nil
}
