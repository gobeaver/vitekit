package vitekit

import (
	"html/template"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadManifestAndTags(t *testing.T) {
	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: []byte(`{
			"src/main.js":{"file":"assets/main.js","isEntry":true,"css":["assets/main.css"],"imports":["src/dashboard.js"]},
			"src/dashboard.js":{"file":"assets/dashboard.js","imports":["src/chart.js","src/theme.js"]},
			"src/chart.js":{"file":"assets/chart.js","css":["assets/chart.css"]},
			"src/theme.js":{"file":"assets/theme.js","css":["assets/main.css"]}
		}`)},
	}
	m, err := loadManifest(fsys, ".vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(fsys, defaultConfig())
	engine.manifest.Store(&m)
	tags, err := engine.TagsWithOptions("src/main.js", RenderOptions{Nonce: "nonce-123"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`<link rel="modulepreload" href="/assets/dashboard.js" nonce="nonce-123" crossorigin>`,
		`<link rel="modulepreload" href="/assets/chart.js" nonce="nonce-123" crossorigin>`,
		`<link rel="modulepreload" href="/assets/theme.js" nonce="nonce-123" crossorigin>`,
		`<link rel="stylesheet" href="/assets/main.css" nonce="nonce-123">`,
		`<link rel="stylesheet" href="/assets/chart.css" nonce="nonce-123">`,
		`<script type="module" src="/assets/main.js" nonce="nonce-123"></script>`,
	} {
		if !strings.Contains(tags, expected) {
			t.Errorf("tags missing %q:\n%s", expected, tags)
		}
	}
	if strings.Count(tags, "main.css") != 1 {
		t.Errorf("shared CSS emitted more than once: %s", tags)
	}
	if strings.Index(tags, `rel="modulepreload"`) > strings.Index(tags, `rel="stylesheet"`) {
		t.Errorf("modulepreload tags must precede stylesheet tags: %s", tags)
	}
}

func TestTagsUsesConfiguredEntry(t *testing.T) {
	config := defaultConfig()
	config.Entry = "src/main.js"
	engine := newEngine(fstest.MapFS{}, config)
	manifest := map[string]ManifestEntry{
		"src/main.js": {File: "assets/main.js", IsEntry: true},
	}
	engine.manifest.Store(&manifest)

	tags, err := engine.Tags("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tags, "/assets/main.js") {
		t.Fatalf("configured entry was not used: %s", tags)
	}
}

// Escaping belongs at the point a URL is written into HTML, not in the URL
// itself: Asset and Preloads feed Link headers and JSON payloads, where an
// HTML-escaped URL is simply wrong.
func TestAssetURLsAreEscapedOnlyWhenRenderedAsHTML(t *testing.T) {
	config := defaultConfig()
	config.AssetsPrefix = `/assets?x="bad"`
	engine := newEngine(fstest.MapFS{}, config)
	manifest := map[string]ManifestEntry{
		"logo.svg":    {File: `logo".svg`},
		"src/main.js": {File: `main".js`, IsEntry: true},
	}
	engine.manifest.Store(&manifest)

	asset, ok := engine.Asset("logo.svg")
	if !ok {
		t.Fatal("asset lookup failed")
	}
	if asset != `/assets?x="bad"/logo".svg` {
		t.Fatalf("Asset must return the raw URL, got %q", asset)
	}

	tags, err := engine.Tags("src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tags, `src="/assets?x="bad"`) {
		t.Fatalf("rendered HTML did not escape the URL: %s", tags)
	}
	if !strings.Contains(tags, "&#34;") {
		t.Fatalf("rendered HTML is missing the escaped quote: %s", tags)
	}
}

func TestNewNonce(t *testing.T) {
	first, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first == second {
		t.Fatalf("expected distinct nonces, got %q and %q", first, second)
	}
	if CSPHeader(first) != `script-src 'nonce-`+first+`'` {
		t.Fatalf("unexpected CSP header")
	}
}

func TestDevTags(t *testing.T) {
	engine := newEngine(fstest.MapFS{}, defaultConfig())
	url := "http://localhost:5174"
	engine.devURL.Store(&url)

	tags, err := engine.TagsWithOptions("src/main.js", RenderOptions{Nonce: "dev-nonce"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tags, `<script type="module" src="http://localhost:5174/@vite/client" nonce="dev-nonce" crossorigin></script>`) ||
		!strings.Contains(tags, `src="http://localhost:5174/src/main.js"`) {
		t.Fatalf("unexpected dev tags: %s", tags)
	}
}

func TestInitAllowsDevModeWithoutManifest(t *testing.T) {
	Reset()
	defer Reset()
	fsys := fstest.MapFS{".vite/hot": {Data: []byte("http://localhost:5173\n")}}
	if err := Init(fsys); err != nil {
		t.Fatal(err)
	}
	if url, ok := Service().DevURL(); !ok || url != "http://localhost:5173" {
		t.Fatalf("unexpected dev URL: %q, %v", url, ok)
	}
}

func TestPreloads(t *testing.T) {
	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: []byte(`{
			"src/main.js":{"file":"assets/main.js","isEntry":true,"imports":["src/dashboard.js"]},
			"src/dashboard.js":{"file":"assets/dashboard.js","imports":["src/chart.js"]},
			"src/chart.js":{"file":"assets/chart.js"}
		}`)},
	}
	m, err := loadManifest(fsys, ".vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(fsys, defaultConfig())
	engine.manifest.Store(&m)

	urls, err := engine.Preloads("src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 preload URLs, got %d: %v", len(urls), urls)
	}
	expected := []string{"/assets/dashboard.js", "/assets/chart.js"}
	for i, u := range expected {
		if urls[i] != u {
			t.Errorf("urls[%d] = %q, want %q", i, urls[i], u)
		}
	}
}

func TestPackageLevelHelpers(t *testing.T) {
	Reset()
	defer Reset()

	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: []byte(`{
			"src/main.js":{"file":"assets/main.js","isEntry":true,"css":["assets/main.css"],"imports":["src/vendor.js"]},
			"src/vendor.js":{"file":"assets/vendor.js"},
			"logo.png":{"file":"assets/logo.png"}
		}`)},
	}
	if err := Init(fsys); err != nil {
		t.Fatal(err)
	}

	tags, err := Tags("src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tags, "assets/main.js") || !strings.Contains(tags, "assets/vendor.js") {
		t.Fatalf("Tags failed: %s", tags)
	}

	tagsOpt, err := TagsWithOptions("src/main.js", RenderOptions{Nonce: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tagsOpt, `nonce="abc"`) {
		t.Fatalf("TagsWithOptions missing nonce: %s", tagsOpt)
	}

	preloads, err := Preloads("src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(preloads) != 1 || preloads[0] != "/assets/vendor.js" {
		t.Fatalf("unexpected Preloads: %v", preloads)
	}

	asset, ok := Asset("logo.png")
	if !ok || asset != "/assets/logo.png" {
		t.Fatalf("Asset lookup failed: %q, %v", asset, ok)
	}

	funcMap := FuncMap(RenderOptions{Nonce: "xyz"})
	viteFn, ok := funcMap["vite"].(func(...string) (template.HTML, error))
	if !ok {
		t.Fatal("FuncMap missing vite function")
	}
	html, err := viteFn("src/main.js")
	if err != nil || !strings.Contains(string(html), `nonce="xyz"`) {
		t.Fatalf("FuncMap vite function output invalid: %v, %s", err, html)
	}
}
