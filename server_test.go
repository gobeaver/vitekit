package vitekit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetHandlerServesEmbeddedFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"dist/app.js":           {Data: []byte("console.log('ok')")},
		"dist/nested/style.css": {Data: []byte("body { color: red }")},
	}
	engine := newEngine(fsys, defaultConfig())
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}

	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if record.Code != http.StatusOK || record.Body.String() != "console.log('ok')" {
		t.Fatalf("unexpected asset response: status=%d body=%q", record.Code, record.Body.String())
	}
	if got := record.Header().Get("Content-Type"); got == "" {
		t.Fatal("asset response did not include a content type")
	}
}

func TestAssetHandlerRejectsTraversal(t *testing.T) {
	fsys := fstest.MapFS{"dist/app.js": {Data: []byte("ok")}}
	engine := newEngine(fsys, defaultConfig())
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}
	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/../secret", nil))
	if record.Code == http.StatusOK {
		t.Fatal("path traversal request unexpectedly succeeded")
	}
}

func TestAssetHandlerMIMETypesAndNosniff(t *testing.T) {
	fsys := fstest.MapFS{
		"dist/module.mjs":    {Data: []byte("export const a = 1;")},
		"dist/component.jsx": {Data: []byte("export default () => <div/>")},
		"dist/comp.tsx":      {Data: []byte("export default () => <div/>")},
		"dist/component.ts":  {Data: []byte("export const App = 1;")},
		"dist/comp.vue":      {Data: []byte("<template><div/></template>")},
		"dist/comp.svelte":   {Data: []byte("<script></script>")},
	}
	engine := newEngine(fsys, defaultConfig())
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{"/module.mjs", "/component.jsx", "/comp.tsx", "/component.ts", "/comp.vue", "/comp.svelte"}
	for _, p := range paths {
		record := httptest.NewRecorder()
		handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, p, nil))
		if record.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", p, record.Code)
		}
		if got := record.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", p, got)
		}
		if got := record.Header().Get("Content-Type"); got != "application/javascript" {
			t.Errorf("%s: Content-Type = %q, want application/javascript", p, got)
		}
	}
}

// buildOutputFS mirrors a real Vite build: hashed files listed in the manifest,
// an unhashed file copied from public/, and precompressed sidecars.
func buildOutputFS() fstest.MapFS {
	return fstest.MapFS{
		"dist/.vite/manifest.json": {Data: []byte(`{
			"src/main.js":{"file":"assets/main-BsO1RtEz.js","isEntry":true,"css":["assets/main-DkP2xy91.css"]}
		}`)},
		"dist/assets/main-BsO1RtEz.js":    {Data: []byte("console.log(1)")},
		"dist/assets/main-BsO1RtEz.js.br": {Data: []byte("brotli-bytes")},
		"dist/assets/main-BsO1RtEz.js.gz": {Data: []byte("gzip-bytes")},
		"dist/assets/main-DkP2xy91.css":   {Data: []byte("body{}")},
		"dist/favicon.ico":                {Data: []byte("icon")},
	}
}

func newBuildEngine(t *testing.T) *Engine {
	t.Helper()
	config := defaultConfig()
	config.ManifestPath = "dist/.vite/manifest.json"
	engine := newEngine(buildOutputFS(), config)
	if err := engine.reloadManifest(); err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestAssetHandlerCachesHashedFilesForever(t *testing.T) {
	engine := newBuildEngine(t)
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want string
	}{
		// Content-hashed and listed in the manifest: the URL can never change
		// meaning, so it is safe to cache for a year.
		{"/assets/main-BsO1RtEz.js", immutableCacheControl},
		{"/assets/main-DkP2xy91.css", immutableCacheControl},
		// Copied verbatim from public/: the name survives the next deploy, so
		// caching it forever would make that deploy invisible.
		{"/favicon.ico", revalidateCacheControl},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d", recorder.Code)
			}
			if got := recorder.Header().Get("Cache-Control"); got != testCase.want {
				t.Errorf("Cache-Control = %q, want %q", got, testCase.want)
			}
			if got := recorder.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
				t.Errorf("Vary = %q, want it to include Accept-Encoding", got)
			}
		})
	}
}

func TestAssetHandlerServesPrecompressedSidecars(t *testing.T) {
	engine := newBuildEngine(t)
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name           string
		acceptEncoding string
		wantEncoding   string
		wantBody       string
	}{
		{"prefers brotli", "gzip, br", "br", "brotli-bytes"},
		{"falls back to gzip", "gzip", "gzip", "gzip-bytes"},
		{"plain when nothing is accepted", "", "", "console.log(1)"},
		// A client that explicitly rejects br must not be handed brotli bytes.
		{"honors q=0 rejection", "br;q=0, gzip", "gzip", "gzip-bytes"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/assets/main-BsO1RtEz.js", nil)
			if testCase.acceptEncoding != "" {
				request.Header.Set("Accept-Encoding", testCase.acceptEncoding)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if got := recorder.Header().Get("Content-Encoding"); got != testCase.wantEncoding {
				t.Errorf("Content-Encoding = %q, want %q", got, testCase.wantEncoding)
			}
			if got := recorder.Body.String(); got != testCase.wantBody {
				t.Errorf("body = %q, want %q", got, testCase.wantBody)
			}
			// The type must describe the asset, never the ".br" sidecar.
			if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
				t.Errorf("Content-Type = %q, want a JavaScript type", got)
			}
		})
	}
}

// A file with no sidecar must still be served normally to a client that would
// have accepted one.
func TestAssetHandlerFallsBackWhenNoSidecarExists(t *testing.T) {
	engine := newBuildEngine(t)
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/assets/main-DkP2xy91.css", nil)
	request.Header.Set("Accept-Encoding", "br, gzip")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
	if recorder.Body.String() != "body{}" {
		t.Errorf("body = %q", recorder.Body.String())
	}
}

// A build directory is not a browsable resource. Vite's metadata and a listing
// of every deployed file are both reachable by default from http.FileServer,
// and neither is something an application means to publish.
func TestAssetHandlerDoesNotExposeBuildInternals(t *testing.T) {
	engine := newBuildEngine(t)
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/.vite/manifest.json", // Vite's build metadata
		"/",                    // a listing of the whole build
		"/assets",              // a listing of the asset directory
	} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body: %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// public/.well-known is a legitimate thing to ship, so the dot-path rule must
// be narrow enough not to break it.
func TestAssetHandlerStillServesWellKnown(t *testing.T) {
	fsys := buildOutputFS()
	fsys["dist/.well-known/acme-challenge/token"] = &fstest.MapFile{Data: []byte("challenge")}
	config := defaultConfig()
	config.ManifestPath = "dist/.vite/manifest.json"
	engine := newEngine(fsys, config)
	if err := engine.reloadManifest(); err != nil {
		t.Fatal(err)
	}
	handler, err := engine.AssetHandler()
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/token", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if recorder.Body.String() != "challenge" {
		t.Errorf("body = %q", recorder.Body.String())
	}
}
