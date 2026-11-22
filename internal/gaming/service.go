package gaming

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var requestPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{16,64}$`)

type Config struct {
	Repository      string   `json:"repository"`
	Branch          string   `json:"branch"`
	Directory       string   `json:"directory"`
	Catalogue       string   `json:"catalogue"`
	Namespace       string   `json:"namespace"`
	Gateway         string   `json:"gateway"`
	Rooms           []string `json:"rooms"`
	ArgoNamespace   string   `json:"argoNamespace"`
	ArgoApplication string   `json:"argoApplication"`
	EndpointHost    string   `json:"endpointHost"`
	StateWorkflow   string   `json:"stateWorkflow,omitempty"`
}

type Room struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Version  string `json:"version"`
	Endpoint string `json:"endpoint,omitempty"`
}
type Observed struct {
	Generation         int64  `json:"generation"`
	ObservedGeneration int64  `json:"observedGeneration"`
	State              string `json:"state"`
	Version            string `json:"version"`
	Phase              string `json:"phase"`
	Message            string `json:"message,omitempty"`
	BackupVerified     bool   `json:"backupVerified"`
}
type Snapshot struct {
	Revision    string   `json:"revision"`
	Rooms       []Room   `json:"rooms"`
	Versions    []string `json:"versions"`
	Writable    bool     `json:"writable"`
	WorkflowURL string   `json:"workflowURL,omitempty"`
}
type Change struct {
	ExpectedRevision string `json:"expectedRevision"`
	RequestID        string `json:"requestId"`
	State            string `json:"state,omitempty"`
	Version          string `json:"version,omitempty"`
}
type Result struct {
	Revision string `json:"revision"`
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	Diff     string `json:"diff"`
}
type Service struct {
	Config     Config
	GitHub     *GitHub
	mu         sync.Mutex
	cached     *Snapshot
	cacheUntil time.Time
}

func New(cfg Config, g *GitHub) (*Service, error) {
	parts := strings.Split(cfg.Repository, "/")
	if len(parts) != 2 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(parts[0]) || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(parts[1]) || cfg.Branch == "" || strings.ContainsAny(cfg.Branch, "\r\n?#%") || !safePath(cfg.Directory) || !safePath(cfg.Catalogue) || !namePattern.MatchString(cfg.Namespace) || !namePattern.MatchString(cfg.Gateway) || len(cfg.Rooms) == 0 {
		return nil, errors.New("configuration des GameRooms invalide")
	}
	seen := map[string]bool{}
	if cfg.StateWorkflow != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]+\.ya?ml$`).MatchString(cfg.StateWorkflow) {
		return nil, errors.New("workflow de commande invalide")
	}
	for _, name := range cfg.Rooms {
		if !namePattern.MatchString(name) || seen[name] {
			return nil, errors.New("liste des GameRooms invalide")
		}
		seen[name] = true
	}
	return &Service{Config: cfg, GitHub: g}, nil
}
func safePath(s string) bool {
	return s != "" && s != "." && s != ".." && path.Clean(s) == s && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\\\r\n?#%")
}
func (s *Service) repo() string { return "/repos/" + s.Config.Repository }
func (s *Service) allowed(name string) bool {
	for _, n := range s.Config.Rooms {
		if n == name {
			return true
		}
	}
	return false
}
func (s *Service) head(ctx context.Context) (string, error) {
	var v struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := s.GitHub.call(ctx, "GET", s.repo()+"/git/ref/heads/"+url.PathEscape(s.Config.Branch), nil, &v)
	if err != nil {
		return "", err
	}
	if !shaPattern.MatchString(v.Object.SHA) {
		return "", errors.New("révision Git invalide")
	}
	return v.Object.SHA, nil
}
func (s *Service) file(ctx context.Context, filename, revision string) ([]byte, error) {
	var v struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	err := s.GitHub.call(ctx, "GET", s.repo()+"/contents/"+filename+"?ref="+url.QueryEscape(revision), nil, &v)
	if err != nil {
		return nil, err
	}
	if v.Type != "file" || v.Encoding != "base64" {
		return nil, errors.New("un fichier Git ordinaire est requis")
	}
	value, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(v.Content, "\n", ""))
	if err != nil || len(value) > 1<<20 {
		return nil, errors.New("manifeste Git invalide")
	}
	return value, nil
}
func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return field(n.Content[0], key)
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func canonical(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return false
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Value == "<<" || seen[k.Value] {
				return false
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if !canonical(child) {
			return false
		}
	}
	return true
}
func text(n *yaml.Node, key string) string {
	if n == nil {
		return ""
	}
	v := field(n, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}
func (s *Service) manifest(ctx context.Context, name, rev string) (*yaml.Node, Room, error) {
	if !s.allowed(name) {
		return nil, Room{}, errors.New("serveur non autorisé")
	}
	data, err := s.file(ctx, s.Config.Directory+"/"+name+".yaml", rev)
	if err != nil {
		return nil, Room{}, err
	}
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if dec.Decode(&node) != nil {
		return nil, Room{}, errors.New("manifeste YAML invalide")
	}
	if !canonical(&node) {
		return nil, Room{}, errors.New("les alias et clés YAML dupliquées ne sont pas autorisés")
	}
	var extra yaml.Node
	if dec.Decode(&extra) != io.EOF {
		return nil, Room{}, errors.New("un seul GameRoom est autorisé par fichier")
	}
	meta, spec := field(&node, "metadata"), field(&node, "spec")
	if text(&node, "apiVersion") != "gaming.bingops.com/v1alpha1" || text(&node, "kind") != "GameRoom" || text(meta, "name") != name || text(meta, "namespace") != s.Config.Namespace || text(spec, "gatewayRef") != s.Config.Gateway || text(spec, "game") != "project-zomboid" {
		return nil, Room{}, errors.New("manifeste hors du périmètre autorisé")
	}
	r := Room{Name: name, State: text(spec, "state"), Version: text(spec, "version")}
	if r.State != "Running" && r.State != "Suspended" {
		return nil, Room{}, errors.New("état Git invalide")
	}
	ports := field(field(spec, "network"), "publicPorts")
	if ports != nil && s.Config.EndpointHost != "" {
		r.Endpoint = s.Config.EndpointHost + ":" + text(ports, "game")
	}
	return &node, r, nil
}
func (s *Service) versions(ctx context.Context, rev string) ([]string, error) {
	data, err := s.file(ctx, s.Config.Catalogue, rev)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("catalogue YAML invalide")
		}
		if doc.Kind != "ConfigMap" || doc.Metadata.Name != "gaas-release-catalog" {
			continue
		}
		var releases map[string]struct {
			Image      string `json:"runtimeImage"`
			Build      string `json:"steamBuildId"`
			Snapshot   string `json:"binarySnapshot"`
			Repository string `json:"binaryRepository"`
			Protocol   int    `json:"operationProtocol"`
		}
		if json.Unmarshal([]byte(doc.Data["releases.json"]), &releases) != nil {
			return nil, errors.New("catalogue JSON invalide")
		}
		versions := []string{}
		for key, r := range releases {
			if regexp.MustCompile(`^\S+@sha256:[a-f0-9]{64}$`).MatchString(r.Image) && regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.Snapshot) && regexp.MustCompile(`^[0-9]+$`).MatchString(r.Build) && r.Repository != "" && r.Protocol == 1 {
				versions = append(versions, key)
			}
		}
		sort.Strings(versions)
		return versions, nil
	}
	return nil, errors.New("catalogue de versions absent")
}
func (s *Service) Read(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Now().Before(s.cacheUntil) {
		return *s.cached, nil
	}
	rev, err := s.head(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{Revision: rev, Rooms: []Room{}, Writable: s.GitHub.Tokens != nil}
	if s.Config.StateWorkflow != "" {
		out.WorkflowURL = "https://github.com/" + s.Config.Repository + "/actions/workflows/" + s.Config.StateWorkflow
	}
	out.Versions, err = s.versions(ctx, rev)
	if err != nil {
		return Snapshot{}, err
	}
	for _, name := range s.Config.Rooms {
		_, room, err := s.manifest(ctx, name, rev)
		if err != nil {
			return Snapshot{}, err
		}
		out.Rooms = append(out.Rooms, room)
	}
	ttl := 30 * time.Second
	if s.GitHub.Tokens == nil {
		ttl = 5 * time.Minute
	}
	s.cached = &out
	s.cacheUntil = time.Now().Add(ttl)
	return out, nil
}

func (s *Service) invalidate() { s.mu.Lock(); defer s.mu.Unlock(); s.cached = nil }

func (s *Service) prepare(ctx context.Context, name string, change Change) (string, Room, string, error) {
	if len(change.Version) > 128 || strings.ContainsAny(change.Version, "\r\n") {
		return "", Room{}, "", errors.New("version demandée invalide")
	}
	if !shaPattern.MatchString(change.ExpectedRevision) || !requestPattern.MatchString(change.RequestID) || (change.State == "") == (change.Version == "") {
		return "", Room{}, "", errors.New("une seule modification et une révision Git sont requises")
	}
	if change.State != "" && change.State != "Running" && change.State != "Suspended" {
		return "", Room{}, "", errors.New("état demandé invalide")
	}
	node, room, err := s.manifest(ctx, name, change.ExpectedRevision)
	if err != nil {
		return "", room, "", err
	}
	spec := field(node, "spec")
	key, value, old := "state", change.State, room.State
	if change.Version != "" {
		key, value, old = "version", change.Version, room.Version
		versions, err := s.versions(ctx, change.ExpectedRevision)
		if err != nil {
			return "", room, "", err
		}
		found := false
		for _, v := range versions {
			found = found || v == value
		}
		if !found {
			return "", room, "", errors.New("version absente du catalogue validé")
		}
	}
	target := field(spec, key)
	if target == nil {
		return "", room, "", errors.New("champ modifiable absent")
	}
	target.Value = value
	target.Tag = "!!str"
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if encoder.Encode(node) != nil {
		return "", room, "", errors.New("modification YAML impossible")
	}
	diff := fmt.Sprintf("%s.yaml\n- spec.%s: %s\n+ spec.%s: %s", name, key, old, key, value)
	return buffer.String(), room, diff, nil
}
func (s *Service) Preview(ctx context.Context, name string, change Change) (Result, error) {
	data, _, diff, err := s.prepare(ctx, name, change)
	_ = data
	return Result{Kind: "preview", Revision: change.ExpectedRevision, Diff: diff}, err
}
func (s *Service) Apply(ctx context.Context, name, actor string, change Change) (Result, error) {
	if s.GitHub.Tokens == nil {
		return Result{}, ErrCredentials
	}
	defer s.invalidate()
	if strings.ContainsAny(actor, "\r\n") || actor == "" || len(actor) > 200 {
		return Result{}, errors.New("identité de l’auteur invalide")
	}
	data, room, diff, err := s.prepare(ctx, name, change)
	if err != nil {
		return Result{}, err
	}
	head, err := s.head(ctx)
	if err != nil {
		return Result{}, err
	}
	var parent struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
		Tree    struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err = s.GitHub.call(ctx, "GET", s.repo()+"/git/commits/"+head, nil, &parent); err != nil {
		return Result{}, err
	}
	value := change.State
	if value == "" {
		value = change.Version
	}
	message := fmt.Sprintf("game(%s): request %s\n\nRequested-by: %s\nGameRoom-Request: %s", name, value, actor, change.RequestID)
	if head != change.ExpectedRevision {
		if parent.Message == message && change.State != "" {
			return Result{Kind: "commit", Revision: head, URL: "https://github.com/" + s.Config.Repository + "/commit/" + head, Diff: diff}, nil
		}
		return Result{}, ErrConflict
	}
	if (change.State != "" && room.State == change.State) || (change.Version != "" && room.Version == change.Version) {
		return Result{Kind: "unchanged", Revision: head, Diff: diff}, nil
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	err = s.GitHub.call(ctx, "POST", s.repo()+"/git/trees", map[string]any{"base_tree": parent.Tree.SHA, "tree": []any{map[string]any{"path": s.Config.Directory + "/" + name + ".yaml", "mode": "100644", "type": "blob", "content": data}}}, &tree)
	if err != nil {
		return Result{}, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	err = s.GitHub.call(ctx, "POST", s.repo()+"/git/commits", map[string]any{"message": message, "tree": tree.SHA, "parents": []string{head}}, &commit)
	if err != nil {
		return Result{}, err
	}
	if !shaPattern.MatchString(commit.SHA) {
		return Result{}, errors.New("commit GitHub invalide")
	}
	if change.Version == "" {
		err = s.GitHub.call(ctx, "PATCH", s.repo()+"/git/refs/heads/"+url.PathEscape(s.Config.Branch), map[string]any{"sha": commit.SHA, "force": false}, nil)
		return Result{Kind: "commit", Revision: commit.SHA, URL: "https://github.com/" + s.Config.Repository + "/commit/" + commit.SHA, Diff: diff}, err
	}
	branch := "game-room/" + change.RequestID
	err = s.GitHub.call(ctx, "POST", s.repo()+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": commit.SHA}, nil)
	if err != nil && !errors.Is(err, ErrConflict) {
		return Result{}, err
	}
	if errors.Is(err, ErrConflict) {
		var existing struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if err = s.GitHub.call(ctx, "GET", s.repo()+"/git/ref/heads/"+url.PathEscape(branch), nil, &existing); err != nil {
			return Result{}, err
		}
		var prior struct {
			Message string `json:"message"`
			Parents []struct {
				SHA string `json:"sha"`
			} `json:"parents"`
		}
		if err = s.GitHub.call(ctx, "GET", s.repo()+"/git/commits/"+existing.Object.SHA, nil, &prior); err != nil {
			return Result{}, err
		}
		if prior.Message != message || len(prior.Parents) != 1 || prior.Parents[0].SHA != head {
			return Result{}, ErrConflict
		}
		commit.SHA = existing.Object.SHA
	}
	var pulls []struct {
		URL string `json:"html_url"`
	}
	err = s.GitHub.call(ctx, "GET", s.repo()+"/pulls?state=open&head="+url.QueryEscape(strings.Split(s.Config.Repository, "/")[0]+":"+branch)+"&base="+url.QueryEscape(s.Config.Branch), nil, &pulls)
	if err != nil {
		return Result{}, err
	}
	if len(pulls) == 1 {
		return Result{Kind: "pull_request", Revision: commit.SHA, URL: pulls[0].URL, Diff: diff}, nil
	}
	if len(pulls) > 1 {
		return Result{}, errors.New("plusieurs PR correspondent à la demande")
	}
	var pr struct {
		URL string `json:"html_url"`
	}
	err = s.GitHub.call(ctx, "POST", s.repo()+"/pulls", map[string]string{"title": "game(" + name + "): version " + change.Version, "head": branch, "base": s.Config.Branch, "body": "Changement demandé depuis LabOps Portal par " + actor + ".\n\n```diff\n" + diff + "\n```\n\nLe contrôleur sauvegarde le monde avant installation."}, &pr)
	return Result{Kind: "pull_request", Revision: commit.SHA, URL: pr.URL, Diff: diff}, err
}
