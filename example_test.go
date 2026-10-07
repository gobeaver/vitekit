package vitekit_test

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing/fstest"

	"github.com/michael-amedaz/vitekit-gobeaver"
)

// buildOutput stands in for a directory Vite has built into: a manifest plus
// the hashed files it names.
func buildOutput() fstest.MapFS {
	return fstest.MapFS{
		"dist/.vite/manifest.json": {Data: []byte(`{
			"src/main.ts":{"file":"assets/main-BsO1RtEz.js","isEntry":true,
				"css":["assets/main-DkP2xy91.css"],"imports":["_vendor-C8s2xQ1p.js"]},
			"src/admin.ts":{"file":"assets/admin-Ba91xQ2z.js","isEntry":true,
				"imports":["_vendor-C8s2xQ1p.js"]},
			"_vendor-C8s2xQ1p.js":{"file":"assets/vendor-C8s2xQ1p.js"},
			"src/theme.css":{"file":"assets/theme-Dp6o0bFX.css","isEntry":true}
		}`)},
		"dist/assets/main-BsO1RtEz.js":   {Data: []byte("console.log('main')")},
		"dist/assets/admin-Ba91xQ2z.js":  {Data: []byte("console.log('admin')")},
		"dist/assets/vendor-C8s2xQ1p.js": {Data: []byte("console.log('vendor')")},
		"dist/assets/main-DkP2xy91.css":  {Data: []byte("body{margin:0}")},
		"dist/assets/theme-Dp6o0bFX.css": {Data: []byte(":root{}")},
	}
}

func newExampleEngine() (*vitekit.Engine, func()) {
	engine, cancel, err := vitekit.WithPrefix("EXAMPLE_DOC_").New(
		buildOutput(),
		vitekit.WithManifestPath("dist/.vite/manifest.json"),
		vitekit.WithOutputDir("dist"),
	)
	if err != nil {
		log.Fatal(err)
	}
	return engine, func() { cancel() }
}

// Tags resolves an entry's whole dependency graph, not just the entry itself:
// the chunks it imports become modulepreload hints and its CSS becomes
// stylesheet links, emitted before the script that needs them.
func ExampleEngine_Tags() {
	engine, cancel := newExampleEngine()
	defer cancel()

	tags, err := engine.Tags("src/main.ts")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(tags)
	// Output:
	// <link rel="modulepreload" href="/assets/vendor-C8s2xQ1p.js" crossorigin>
	// <link rel="stylesheet" href="/assets/main-DkP2xy91.css">
	// <script type="module" src="/assets/main-BsO1RtEz.js"></script>
}

// TagsFor renders several entries in one pass. Both scripts here import the
// same vendor chunk, and it is preloaded once — calling Tags twice and
// concatenating would emit it twice. A CSS entry becomes a stylesheet link
// rather than a module script.
func ExampleEngine_TagsFor() {
	engine, cancel := newExampleEngine()
	defer cancel()

	tags, err := engine.TagsFor(
		[]string{"src/theme.css", "src/main.ts", "src/admin.ts"},
		vitekit.RenderOptions{},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(tags)
	// Output:
	// <link rel="modulepreload" href="/assets/vendor-C8s2xQ1p.js" crossorigin>
	// <link rel="stylesheet" href="/assets/main-DkP2xy91.css">
	// <link rel="stylesheet" href="/assets/theme-Dp6o0bFX.css">
	// <script type="module" src="/assets/main-BsO1RtEz.js"></script>
	// <script type="module" src="/assets/admin-Ba91xQ2z.js"></script>
}

// A nonce is generated per response, stamped onto every generated tag, and
// sent in the matching policy header. A fixed nonce is used here only so the
// example has deterministic output; real code calls [vitekit.NewNonce] per
// request.
func ExampleEngine_ContentSecurityPolicy() {
	engine, cancel := newExampleEngine()
	defer cancel()

	const nonce = "r4nd0m"

	tags, err := engine.TagsWithOptions("src/main.ts", vitekit.RenderOptions{Nonce: nonce})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(tags)
	// 'strict-dynamic' is what authorizes the chunks the entry module imports;
	// a nonce on the entry script alone would not cover them.
	fmt.Println(engine.ContentSecurityPolicy(nonce))
	// Output:
	// <link rel="modulepreload" href="/assets/vendor-C8s2xQ1p.js" nonce="r4nd0m" crossorigin>
	// <link rel="stylesheet" href="/assets/main-DkP2xy91.css" nonce="r4nd0m">
	// <script type="module" src="/assets/main-BsO1RtEz.js" nonce="r4nd0m"></script>
	// default-src 'self'; script-src 'nonce-r4nd0m' 'strict-dynamic' 'self'; style-src 'nonce-r4nd0m' 'self'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; base-uri 'none'; object-src 'none'
}

// The per-request shape: a fresh nonce, the tags carrying it, and the policy
// header that authorizes them.
func ExampleNewNonce() {
	engine, cancel := newExampleEngine()
	defer cancel()

	handler := func(writer http.ResponseWriter, request *http.Request) {
		nonce, err := vitekit.NewNonce()
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		tags, err := engine.TagsWithOptions("src/main.ts", vitekit.RenderOptions{Nonce: nonce})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Security-Policy", engine.ContentSecurityPolicy(nonce))
		_, _ = fmt.Fprintf(writer, "<!doctype html><html><head>%s</head><body></body></html>", tags)
	}

	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	fmt.Println(recorder.Code)
	fmt.Println(strings.Contains(recorder.Header().Get("Content-Security-Policy"), "strict-dynamic"))
	// Output:
	// 200
	// true
}

// AssetHandler serves the build output. Mount it at the same prefix the
// generated URLs use, with no path stripping.
func ExampleEngine_AssetHandler() {
	engine, cancel := newExampleEngine()
	defer cancel()

	assets, err := engine.AssetHandler()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/assets/", assets)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/main-BsO1RtEz.js", nil))

	fmt.Println(recorder.Code)
	// The filename carries a content hash, so the bytes behind this URL can
	// never change and it is safe to cache indefinitely.
	fmt.Println(recorder.Header().Get("Cache-Control"))
	// Output:
	// 200
	// public, max-age=31536000, immutable
}

// PreloadHeader produces a Link value covering everything an entry needs,
// suitable for a 103 Early Hints response so the browser can start fetching
// before it has parsed the document.
func ExampleEngine_PreloadHeader() {
	engine, cancel := newExampleEngine()
	defer cancel()

	header, err := engine.PreloadHeader("src/main.ts")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(header)
	// Output:
	// </assets/main-DkP2xy91.css>; rel=preload; as=style, </assets/vendor-C8s2xQ1p.js>; rel=modulepreload; as=script
}

// Asset resolves a logical name to the hashed file Vite emitted, for images
// and fonts referenced from a Go template rather than from JavaScript.
func ExampleEngine_Asset() {
	engine, cancel := newExampleEngine()
	defer cancel()

	url, ok := engine.Asset("src/theme.css")
	fmt.Println(url, ok)

	_, ok = engine.Asset("src/missing.png")
	fmt.Println(ok)
	// Output:
	// /assets/theme-Dp6o0bFX.css true
	// false
}
