package vitekit

import (
	"strings"
	"testing"
	"testing/fstest"
)

func multiEntryEngine(t *testing.T) *Engine {
	t.Helper()
	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: []byte(`{
			"src/app.js":{"file":"assets/app.js","isEntry":true,"css":["assets/app.css"],"imports":["src/shared.js"]},
			"src/admin.js":{"file":"assets/admin.js","isEntry":true,"css":["assets/admin.css"],"imports":["src/shared.js"]},
			"src/shared.js":{"file":"assets/shared.js","css":["assets/shared.css"]},
			"src/theme.css":{"file":"assets/theme.css","isEntry":true,"src":"src/theme.css"}
		}`)},
	}
	manifest, err := loadManifest(fsys, ".vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(fsys, defaultConfig())
	engine.manifest.Store(&manifest)
	return engine
}

// Rendering two entries together must not repeat the chunk they share; that is
// the whole reason TagsFor exists rather than calling Tags twice.
func TestTagsForDeduplicatesSharedChunks(t *testing.T) {
	engine := multiEntryEngine(t)

	tags, err := engine.TagsFor([]string{"src/app.js", "src/admin.js"}, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(tags, "assets/shared.js"); got != 1 {
		t.Errorf("shared chunk preloaded %d times, want 1:\n%s", got, tags)
	}
	if got := strings.Count(tags, "assets/shared.css"); got != 1 {
		t.Errorf("shared stylesheet emitted %d times, want 1:\n%s", got, tags)
	}
	for _, want := range []string{
		`<script type="module" src="/assets/app.js"></script>`,
		`<script type="module" src="/assets/admin.js"></script>`,
		`<link rel="stylesheet" href="/assets/app.css">`,
		`<link rel="stylesheet" href="/assets/admin.css">`,
	} {
		if !strings.Contains(tags, want) {
			t.Errorf("tags missing %q:\n%s", want, tags)
		}
	}
}

// An entry that is itself rendered as a <script> must not also be advertised
// as a modulepreload — that asks the browser to fetch one module twice.
func TestTagsForDoesNotPreloadAnEntryItRenders(t *testing.T) {
	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: []byte(`{
			"src/app.js":{"file":"assets/app.js","isEntry":true,"imports":["src/shared.js"]},
			"src/shared.js":{"file":"assets/shared.js","isEntry":true}
		}`)},
	}
	manifest, err := loadManifest(fsys, ".vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(fsys, defaultConfig())
	engine.manifest.Store(&manifest)

	tags, err := engine.TagsFor([]string{"src/app.js", "src/shared.js"}, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tags, `rel="modulepreload" href="/assets/shared.js"`) {
		t.Errorf("an entry was both preloaded and scripted:\n%s", tags)
	}
	if got := strings.Count(tags, "assets/shared.js"); got != 1 {
		t.Errorf("shared.js referenced %d times, want 1:\n%s", got, tags)
	}
}

func TestTagsForRendersCSSEntryAsStylesheet(t *testing.T) {
	engine := multiEntryEngine(t)

	tags, err := engine.TagsFor([]string{"src/theme.css"}, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tags, `<link rel="stylesheet" href="/assets/theme.css">`) {
		t.Errorf("CSS entry was not rendered as a stylesheet:\n%s", tags)
	}
	if strings.Contains(tags, "<script") {
		t.Errorf("CSS entry was rendered as a script:\n%s", tags)
	}
}

func TestDevTagsRenderCSSEntryAsStylesheet(t *testing.T) {
	engine := multiEntryEngine(t)
	devURL := "http://localhost:5173"
	engine.devURL.Store(&devURL)

	tags, err := engine.TagsFor([]string{"src/theme.css", "src/app.js"}, RenderOptions{Nonce: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<script type="module" src="http://localhost:5173/@vite/client" nonce="n1" crossorigin></script>`,
		`<link rel="stylesheet" href="http://localhost:5173/src/theme.css" nonce="n1" crossorigin>`,
		`<script type="module" src="http://localhost:5173/src/app.js" nonce="n1" crossorigin></script>`,
	} {
		if !strings.Contains(tags, want) {
			t.Errorf("dev tags missing %q:\n%s", want, tags)
		}
	}
	if got := strings.Count(tags, "@vite/client"); got != 1 {
		t.Errorf("Vite client injected %d times, want 1:\n%s", got, tags)
	}
}

func TestPreloadHeader(t *testing.T) {
	engine := multiEntryEngine(t)

	header, err := engine.PreloadHeader("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"</assets/app.css>; rel=preload; as=style",
		"</assets/shared.css>; rel=preload; as=style",
		"</assets/shared.js>; rel=modulepreload; as=script",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("Link header missing %q: %s", want, header)
		}
	}
}

// In dev there is no manifest to walk and Vite discovers modules itself, so
// emitting preload hints would point at stale production paths.
func TestPreloadHeaderIsEmptyInDevMode(t *testing.T) {
	engine := multiEntryEngine(t)
	devURL := "http://localhost:5173"
	engine.devURL.Store(&devURL)

	header, err := engine.PreloadHeader("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		t.Errorf("expected no preload hints in dev mode, got %q", header)
	}
}

func TestTagsForReportsUnknownEntry(t *testing.T) {
	engine := multiEntryEngine(t)
	if _, err := engine.TagsFor([]string{"src/app.js", "src/missing.js"}, RenderOptions{}); err == nil {
		t.Fatal("expected an error for an entry that is not in the manifest")
	}
}

func TestFuncMapViteAsset(t *testing.T) {
	engine := multiEntryEngine(t)
	funcMap := engine.FuncMap(RenderOptions{})

	assetFn, ok := funcMap["viteAsset"].(func(string) (string, error))
	if !ok {
		t.Fatal("FuncMap is missing the viteAsset function")
	}
	url, err := assetFn("src/shared.js")
	if err != nil {
		t.Fatal(err)
	}
	if url != "/assets/shared.js" {
		t.Errorf("viteAsset returned %q", url)
	}
	if _, err := assetFn("src/nope.js"); err == nil {
		t.Error("expected an error for an unknown asset")
	}
}
