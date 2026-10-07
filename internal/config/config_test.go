package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func known(t string) bool { return t == "clock" || t == "rss" }

func page(name string, widgets ...Widget) Page {
	return Page{Name: name, Columns: []Column{{Size: "small", Widgets: widgets}}}
}

func TestNormalizeFillsSlugsSizesAndIDs(t *testing.T) {
	pages := []Page{
		{Name: "Ops & Lab", Columns: []Column{{Size: "huge", Widgets: []Widget{{Type: "clock"}, {Type: "rss", ID: "keep"}}}, {Size: "small"}}},
	}
	if err := Normalize(pages, known); err != nil {
		t.Fatal(err)
	}
	p := pages[0]
	if p.Slug != "ops-lab" {
		t.Errorf("slug = %q", p.Slug)
	}
	if p.Columns[0].Size != "full" || p.Columns[1].Size != "small" {
		t.Errorf("sizes = %q, %q", p.Columns[0].Size, p.Columns[1].Size)
	}
	if got := p.Columns[0].Widgets[0].ID; got != "ops-lab-1-1-clock" {
		t.Errorf("generated id = %q", got)
	}
	if got := p.Columns[0].Widgets[1].ID; got != "keep" {
		t.Errorf("explicit id = %q", got)
	}
	if p.Columns[1].Widgets == nil {
		t.Error("empty column must serialize as [] rather than null")
	}
}

func TestNormalizeMakesDuplicateIDsUnique(t *testing.T) {
	pages := []Page{page("A", Widget{Type: "clock", ID: "x"}, Widget{Type: "clock", ID: "x"})}
	if err := Normalize(pages, known); err != nil {
		t.Fatal(err)
	}
	w := pages[0].Columns[0].Widgets
	if w[0].ID == w[1].ID {
		t.Errorf("duplicate id %q kept", w[0].ID)
	}
}

func TestNormalizeRejectsInvalidPages(t *testing.T) {
	cases := map[string][]Page{
		"no pages":         {},
		"unnamed page":     {page(" ")},
		"unknown widget":   {page("A", Widget{Type: "nope"})},
		"same slug":        {page("Ops"), page("ops")},
		"no column":        {{Name: "A"}},
		"too many columns": {{Name: "A", Columns: make([]Column, maxColumns+1)}},
	}
	for name, pages := range cases {
		if err := Normalize(pages, known); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

const baseYAML = `
title: Test
pages:
  - name: Home
    columns:
      - size: full
        widgets:
          - type: clock
`

func newStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "portal.yaml")
	if err := os.WriteFile(yamlPath, []byte(baseYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	return NewStore(yamlPath, data, known), yamlPath, data
}

func TestStoreLayoutOverridesYAMLUntilReset(t *testing.T) {
	store, _, data := newStore(t)

	cfg, source, err := store.Effective()
	if err != nil || source != "yaml" || cfg.Title != "Test" || cfg.Pages[0].Name != "Home" {
		t.Fatalf("initial: %+v %q %v", cfg, source, err)
	}

	if err := store.SaveLayout([]Page{page("Custom", Widget{Type: "rss"})}); err != nil {
		t.Fatal(err)
	}
	cfg, source, _ = store.Effective()
	if source != "custom" || cfg.Pages[0].Name != "Custom" || cfg.Title != "Test" {
		t.Fatalf("after save: %+v %q", cfg, source)
	}
	if _, err := os.Stat(filepath.Join(data, "layout.json")); err != nil {
		t.Fatalf("layout.json not written: %v", err)
	}

	if err := store.ResetLayout(); err != nil {
		t.Fatal(err)
	}
	if cfg, source, _ = store.Effective(); source != "yaml" || cfg.Pages[0].Name != "Home" {
		t.Fatalf("after reset: %+v %q", cfg, source)
	}
	if err := store.ResetLayout(); err != nil {
		t.Errorf("resetting twice must succeed: %v", err)
	}
}

func TestStoreRejectsInvalidLayout(t *testing.T) {
	store, _, _ := newStore(t)
	if err := store.SaveLayout([]Page{page("A", Widget{Type: "nope"})}); err == nil {
		t.Fatal("expected an error")
	}
	if _, source, _ := store.Effective(); source != "yaml" {
		t.Errorf("source = %q after a rejected save", source)
	}
}

func TestStoreIgnoresCorruptLayoutFile(t *testing.T) {
	store, _, data := newStore(t)
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "layout.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, source, err := store.Effective()
	if err != nil || source != "yaml" || cfg.Pages[0].Name != "Home" {
		t.Fatalf("%+v %q %v", cfg, source, err)
	}
}

func TestStoreKeepsLastValidYAMLWhenReloadFails(t *testing.T) {
	store, yamlPath, _ := newStore(t)
	if _, _, err := store.Effective(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, []byte("pages: [oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := store.Effective()
	if err != nil || cfg.Pages[0].Name != "Home" {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestStoreFailsOnInvalidInitialYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "portal.yaml")
	os.WriteFile(yamlPath, []byte(strings.Replace(baseYAML, "clock", "nope", 1)), 0o644)
	if _, _, err := NewStore(yamlPath, dir, known).Effective(); err == nil {
		t.Fatal("expected an error")
	}
}
