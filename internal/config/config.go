// Package config loads the portal definition: a versioned YAML file provides
// the default pages, and an optional layout saved from the UI overrides them.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Title string `yaml:"title" json:"title"`
	Theme string `yaml:"theme,omitempty" json:"theme,omitempty"`
	Pages []Page `yaml:"pages" json:"pages"`
}

type Page struct {
	Name string `yaml:"name" json:"name"`
	Slug string `yaml:"slug,omitempty" json:"slug"`
	// Icon names the pictogram shown on the page's tab.
	Icon string `yaml:"icon,omitempty" json:"icon,omitempty"`
	// Group gathers pages under one caption in the navigation.
	Group   string   `yaml:"group,omitempty" json:"group,omitempty"`
	Columns []Column `yaml:"columns" json:"columns"`
}

type Column struct {
	Size    string   `yaml:"size" json:"size"`
	Widgets []Widget `yaml:"widgets" json:"widgets"`
}

type Widget struct {
	ID      string         `yaml:"id,omitempty" json:"id"`
	Type    string         `yaml:"type" json:"type"`
	Title   string         `yaml:"title,omitempty" json:"title,omitempty"`
	Options map[string]any `yaml:"options,omitempty" json:"options,omitempty"`
}

const (
	maxPages   = 20
	maxColumns = 4
	maxWidgets = 40
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// Normalize validates pages and fills in slugs, column sizes and widget IDs.
// knownType reports whether a widget type exists.
func Normalize(pages []Page, knownType func(string) bool) error {
	if len(pages) == 0 {
		return errors.New("au moins une page est requise")
	}
	if len(pages) > maxPages {
		return fmt.Errorf("trop de pages (maximum %d)", maxPages)
	}
	slugs := map[string]bool{}
	ids := map[string]bool{}
	for pi := range pages {
		p := &pages[pi]
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return fmt.Errorf("la page %d n'a pas de nom", pi+1)
		}
		if p.Slug = slugify(p.Slug); p.Slug == "" {
			p.Slug = slugify(p.Name)
		}
		if p.Slug == "" {
			p.Slug = fmt.Sprintf("page-%d", pi+1)
		}
		if slugs[p.Slug] {
			return fmt.Errorf("deux pages portent l'adresse %q", p.Slug)
		}
		slugs[p.Slug] = true
		if len(p.Columns) == 0 || len(p.Columns) > maxColumns {
			return fmt.Errorf("la page %q doit avoir entre 1 et %d colonnes", p.Name, maxColumns)
		}
		for ci := range p.Columns {
			c := &p.Columns[ci]
			if c.Size != "small" {
				c.Size = "full"
			}
			if len(c.Widgets) > maxWidgets {
				return fmt.Errorf("trop de widgets dans une colonne de %q (maximum %d)", p.Name, maxWidgets)
			}
			if c.Widgets == nil {
				c.Widgets = []Widget{}
			}
			for wi := range c.Widgets {
				w := &c.Widgets[wi]
				if !knownType(w.Type) {
					return fmt.Errorf("type de widget inconnu %q (page %q)", w.Type, p.Name)
				}
				if w.ID == "" || ids[w.ID] {
					w.ID = fmt.Sprintf("%s-%d-%d-%s", p.Slug, ci+1, wi+1, w.Type)
				}
				ids[w.ID] = true
			}
		}
	}
	return nil
}

// Store resolves the effective configuration and persists UI edits.
type Store struct {
	yamlPath   string
	layoutPath string
	knownType  func(string) bool

	mu         sync.Mutex
	base       Config
	baseMod    time.Time
	layout     []Page
	layoutMod  time.Time
	hasLayout  bool
	loadedOnce bool
}

func NewStore(yamlPath, dataDir string, knownType func(string) bool) *Store {
	return &Store{yamlPath: yamlPath, layoutPath: filepath.Join(dataDir, "layout.json"), knownType: knownType}
}

// Effective returns the configuration to serve and whether its pages come
// from the saved layout ("custom") or from the YAML file ("yaml"). Both files
// are re-read when they change on disk.
func (s *Store) Effective() (Config, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if st, err := os.Stat(s.yamlPath); err != nil {
		if !s.loadedOnce {
			return Config{}, "", fmt.Errorf("lecture de %s: %w", s.yamlPath, err)
		}
	} else if !s.loadedOnce || !st.ModTime().Equal(s.baseMod) {
		cfg, err := loadYAML(s.yamlPath, s.knownType)
		if err != nil {
			if !s.loadedOnce {
				return Config{}, "", err
			}
			// Keep serving the last valid file when a reload fails.
		} else {
			s.base, s.loadedOnce = cfg, true
		}
		s.baseMod = st.ModTime()
	}

	if st, err := os.Stat(s.layoutPath); err != nil {
		s.hasLayout = false
	} else if !s.hasLayout || !st.ModTime().Equal(s.layoutMod) {
		var pages []Page
		raw, err := os.ReadFile(s.layoutPath)
		if err == nil {
			err = json.Unmarshal(raw, &pages)
		}
		if err == nil {
			err = Normalize(pages, s.knownType)
		}
		s.hasLayout = err == nil
		s.layout, s.layoutMod = pages, st.ModTime()
	}

	cfg := s.base
	if s.hasLayout {
		cfg.Pages = s.layout
		return cfg, "custom", nil
	}
	return cfg, "yaml", nil
}

func loadYAML(path string, knownType func(string) bool) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Title == "" {
		cfg.Title = "LabOps"
	}
	if err := Normalize(cfg.Pages, knownType); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// SaveLayout validates and atomically writes pages edited in the UI.
func (s *Store) SaveLayout(pages []Page) error {
	if err := Normalize(pages, s.knownType); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(pages, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.layoutPath), 0o755); err != nil {
		return err
	}
	tmp := s.layoutPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.layoutPath); err != nil {
		return err
	}
	s.hasLayout = false
	return nil
}

// ResetLayout drops the saved layout so the YAML pages apply again.
func (s *Store) ResetLayout() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasLayout = false
	if err := os.Remove(s.layoutPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
