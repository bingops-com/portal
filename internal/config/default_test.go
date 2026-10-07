package config_test

import (
	"testing"

	"github.com/bingops-com/portal/internal/config"
	"github.com/bingops-com/portal/internal/providers"
)

// The example configuration shipped in the repository must always load.
func TestDefaultConfigIsValid(t *testing.T) {
	cfg, source, err := config.NewStore("../../config/portal.yaml", t.TempDir(), providers.Known).Effective()
	if err != nil {
		t.Fatal(err)
	}
	if source != "yaml" || len(cfg.Pages) == 0 {
		t.Fatalf("source %q, %d pages", source, len(cfg.Pages))
	}
}
