package vitekit

import "testing"

func TestDefaultConfigUsesEnvironment(t *testing.T) {
	t.Setenv("BEAVER_VITE_MANIFEST_PATH", "manifest.json")
	t.Setenv("BEAVER_VITE_OUTPUT_DIR", "public")
	t.Setenv("BEAVER_VITE_DEV_COMMAND", "npm run preview")
	t.Setenv("BEAVER_VITE_HOT_FILE", "hot")
	t.Setenv("BEAVER_VITE_ASSETS_PREFIX", "/static/")
	t.Setenv("BEAVER_VITE_ENTRY", "src/main.ts")

	config := defaultConfig()
	if config.ManifestPath != "manifest.json" || config.OutputDir != "public" ||
		config.DevCommand != "npm run preview" || config.HotFilePath != "hot" ||
		config.AssetsPrefix != "/static/" || config.Entry != "src/main.ts" {
		t.Fatalf("environment was not applied: %#v", config)
	}
}

func TestWithDevModeAllowsMissingManifest(t *testing.T) {
	config := defaultConfig()
	WithDevMode()(config)
	if !config.AllowMissingManifest {
		t.Fatal("WithDevMode did not enable missing-manifest startup")
	}
}
