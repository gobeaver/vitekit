package vitekit

import (
	"testing"
	"testing/fstest"
)

func TestWithPrefixReadsCustomEnvVars(t *testing.T) {
	t.Setenv("BEAVER_ADMIN_VITE_MANIFEST_PATH", "admin/.vite/manifest.json")
	t.Setenv("BEAVER_ADMIN_VITE_OUTPUT_DIR", "admin/dist")
	t.Setenv("BEAVER_ADMIN_VITE_ASSETS_PREFIX", "/admin/assets/")
	t.Setenv("BEAVER_ADMIN_VITE_ENTRY", "src/admin.ts")

	cfg := prefixedConfig("ADMIN_VITE_")
	if cfg.ManifestPath != "admin/.vite/manifest.json" {
		t.Fatalf("ManifestPath: got %q", cfg.ManifestPath)
	}
	if cfg.OutputDir != "admin/dist" {
		t.Fatalf("OutputDir: got %q", cfg.OutputDir)
	}
	if cfg.AssetsPrefix != "/admin/assets/" {
		t.Fatalf("AssetsPrefix: got %q", cfg.AssetsPrefix)
	}
	if cfg.Entry != "src/admin.ts" {
		t.Fatalf("Entry: got %q", cfg.Entry)
	}
}

func TestWithPrefixFallsBackToDefaults(t *testing.T) {
	cfg := prefixedConfig("CUSTOM_")
	if cfg.ManifestPath != ".vite/manifest.json" {
		t.Fatalf("expected default ManifestPath, got %q", cfg.ManifestPath)
	}
	if cfg.OutputDir != "dist" {
		t.Fatalf("expected default OutputDir, got %q", cfg.OutputDir)
	}
}

func TestBuilderNewCreatesWorkingEngine(t *testing.T) {
	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: []byte(`{
			"src/admin.js":{"file":"assets/admin.js","isEntry":true}
		}`)},
	}

	engine, cancel, err := WithPrefix("TEST_").New(fsys)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	tags, err := engine.Tags("src/admin.js")
	if err != nil {
		t.Fatal(err)
	}
	if tags == "" {
		t.Fatal("expected non-empty tags")
	}
}

func TestBuilderNewWithOptionOverride(t *testing.T) {
	fsys := fstest.MapFS{
		"custom/manifest.json": {Data: []byte(`{
			"src/app.js":{"file":"assets/app.js","isEntry":true}
		}`)},
	}

	engine, cancel, err := WithPrefix("OPT_").New(fsys, WithManifestPath("custom/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	tags, err := engine.Tags("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if tags == "" {
		t.Fatal("expected non-empty tags")
	}
}
