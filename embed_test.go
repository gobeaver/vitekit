package vitekit

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The single-binary deployment is the production story, and it is the one path
// a unit test with a hand-built fstest.MapFS cannot honestly stand in for:
// //go:embed keeps the pattern's own directory prefix, so the manifest does not
// land at any of the conventional paths. This fixture reproduces that layout
// exactly.
//
//go:embed all:testdata/frontend/dist
var embeddedBuild embed.FS

func TestEmbeddedBuildNeedsNoConfiguration(t *testing.T) {
	Reset()
	defer Reset()

	// No options at all: the embed prefix has to be discovered, not declared.
	if err := Init(embeddedBuild); err != nil {
		t.Fatalf("embedded build failed to initialize: %v", err)
	}
	engine := Service()

	tags, err := engine.Tags("src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<link rel="modulepreload" href="/assets/vendor-C8s2xQ1p.js" crossorigin>`,
		`<link rel="stylesheet" href="/assets/main-DkP2xy91.css">`,
		`<script type="module" src="/assets/main-BsO1RtEz.js"></script>`,
	} {
		if !strings.Contains(tags, want) {
			t.Errorf("tags missing %q:\n%s", want, tags)
		}
	}

	// The output directory has to be inferred alongside the manifest, or the
	// asset handler would be rooted at the wrong place.
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path      string
		wantCache string
	}{
		{"/assets/main-BsO1RtEz.js", immutableCacheControl},
		{"/assets/main-DkP2xy91.css", immutableCacheControl},
		{"/favicon.ico", revalidateCacheControl},
	}
	for _, testCase := range cases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", testCase.path, recorder.Code)
			continue
		}
		if recorder.Body.Len() == 0 {
			t.Errorf("%s: served an empty body", testCase.path)
		}
		if got := recorder.Header().Get("Cache-Control"); got != testCase.wantCache {
			t.Errorf("%s: Cache-Control = %q, want %q", testCase.path, got, testCase.wantCache)
		}
	}
}

// An explicitly configured output directory must win over the inferred one,
// even when discovery had to go looking for the manifest.
func TestExplicitOutputDirSurvivesManifestDiscovery(t *testing.T) {
	Reset()
	defer Reset()

	if err := Init(embeddedBuild, WithOutputDir("testdata/frontend/dist/assets")); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	handler, err := Service().AssetHandler()
	if err != nil {
		t.Fatal(err)
	}
	// Rooted at assets/, the file is reachable without the extra path segment.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/main-BsO1RtEz.js", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("explicit output dir was overridden by discovery: status %d", recorder.Code)
	}
}

func TestDiscoverManifestPrefersTheViteDirectory(t *testing.T) {
	found, ok := discoverManifest(embeddedBuild)
	if !ok {
		t.Fatal("no manifest discovered")
	}
	if found != "testdata/frontend/dist/.vite/manifest.json" {
		t.Errorf("discovered %q", found)
	}
	if got := manifestOutputDir(found); got != "testdata/frontend/dist" {
		t.Errorf("manifestOutputDir(%q) = %q", found, got)
	}
}
