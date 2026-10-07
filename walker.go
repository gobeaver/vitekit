package vitekit

import (
	"fmt"
	"html/template"
	"strings"
)

// RenderOptions controls the attributes added to generated tags.
type RenderOptions struct {
	Nonce string
}

// Tags resolves all static dependencies of an entry and returns
// the corresponding HTML tags: stylesheet links for the full CSS
// import chain, modulepreload links for code-split JS chunks, and
// the entry <script> tag last.
func (e *Engine) Tags(entry string) (string, error) {
	return e.TagsWithOptions(entry, RenderOptions{})
}

// TagsWithOptions resolves an entry and renders its dependency tags with the
// supplied options.
func (e *Engine) TagsWithOptions(entry string, options RenderOptions) (string, error) {
	return e.TagsFor([]string{entry}, options)
}

// TagsFor renders one set of tags covering several entries at once — a page
// that loads both an application bundle and a standalone stylesheet, for
// example. Chunks and stylesheets shared between the entries are emitted once,
// which is why this is not the same as concatenating separate Tags calls.
func (e *Engine) TagsFor(entries []string, options RenderOptions) (string, error) {
	entries = e.resolveEntries(entries)
	if len(entries) == 0 {
		return "", fmt.Errorf("vitekit: no entry configured")
	}
	if devURL, ok := e.DevURL(); ok {
		return e.devTags(devURL, entries, options), nil
	}
	return e.productionTags(entries, options)
}

// resolveEntries drops empty entries and falls back to the configured default
// entry, so Tags("") keeps working.
func (e *Engine) resolveEntries(entries []string) []string {
	resolved := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == "" {
			entry = e.entry
		}
		if entry != "" {
			resolved = append(resolved, entry)
		}
	}
	return resolved
}

func (e *Engine) devTags(devURL string, entries []string, options RenderOptions) string {
	attribute := nonceAttribute(options)
	base := strings.TrimRight(devURL, "/")
	escapedBase := template.HTMLEscapeString(base)

	tags := []string{fmt.Sprintf(
		// Dev-mode scripts load cross-origin (Go on :8080, Vite on :5173), so
		// crossorigin is required for CORS to work and for error events to
		// produce useful stack traces instead of "Script error.".
		`<script type="module" src="%s/@vite/client"%s crossorigin></script>`,
		escapedBase, attribute,
	)}
	for _, entry := range entries {
		url := template.HTMLEscapeString(base + "/" + strings.TrimLeft(entry, "/"))
		if isStylesheet(entry) {
			// A CSS entry stays a <link> in dev so it blocks rendering exactly
			// as it will in production; loading it as a module would let the
			// page paint unstyled first.
			tags = append(tags, fmt.Sprintf(`<link rel="stylesheet" href="%s"%s crossorigin>`, url, attribute))
			continue
		}
		tags = append(tags, fmt.Sprintf(`<script type="module" src="%s"%s crossorigin></script>`, url, attribute))
	}
	return strings.Join(tags, "\n")
}

func (e *Engine) productionTags(entries []string, options RenderOptions) (string, error) {
	graph, err := e.walk(entries)
	if err != nil {
		return "", err
	}
	attribute := nonceAttribute(options)

	tags := make([]string, 0, len(graph.preloads)+len(graph.stylesheets)+len(entries))
	for _, file := range graph.preloads {
		// The HTML spec requires crossorigin on modulepreload for the preloaded
		// resource to match the module script fetch. Without it, browsers may
		// double-fetch the module.
		tags = append(tags, fmt.Sprintf(`<link rel="modulepreload" href="%s"%s crossorigin>`,
			template.HTMLEscapeString(e.assetURL(file)), attribute))
	}
	for _, file := range graph.stylesheets {
		tags = append(tags, fmt.Sprintf(`<link rel="stylesheet" href="%s"%s>`,
			template.HTMLEscapeString(e.assetURL(file)), attribute))
	}
	for _, entry := range entries {
		file := graph.manifest[entry].File
		url := template.HTMLEscapeString(e.assetURL(file))
		if isStylesheet(file) {
			tags = append(tags, fmt.Sprintf(`<link rel="stylesheet" href="%s"%s>`, url, attribute))
			continue
		}
		tags = append(tags, fmt.Sprintf(`<script type="module" src="%s"%s></script>`, url, attribute))
	}
	return strings.Join(tags, "\n"), nil
}

// assetGraph is the result of walking the manifest for one render: the
// stylesheets and code-split chunks an entry set depends on, in load order.
type assetGraph struct {
	manifest    map[string]ManifestEntry
	entries     map[string]bool
	visited     map[string]bool
	seenCSS     map[string]bool
	seenPreload map[string]bool
	stylesheets []string
	preloads    []string
}

// walk resolves the dependency graph of every entry in one pass so that chunks
// and stylesheets shared between entries are emitted only once.
func (e *Engine) walk(entries []string) (*assetGraph, error) {
	manifestPtr := e.manifest.Load()
	if manifestPtr == nil {
		return nil, fmt.Errorf("vitekit: manifest not loaded")
	}
	manifest := *manifestPtr

	graph := &assetGraph{
		manifest:    manifest,
		entries:     make(map[string]bool, len(entries)),
		visited:     make(map[string]bool),
		seenCSS:     make(map[string]bool),
		seenPreload: make(map[string]bool),
	}
	for _, entry := range entries {
		if _, ok := manifest[entry]; !ok {
			return nil, fmt.Errorf("vitekit: entry %q not found in manifest", entry)
		}
		graph.entries[entry] = true
	}
	for _, entry := range entries {
		graph.descend(entry)
	}
	return graph, nil
}

func (g *assetGraph) descend(key string) {
	if g.visited[key] {
		return
	}
	chunk, ok := g.manifest[key]
	if !ok {
		return
	}
	g.visited[key] = true

	for _, css := range chunk.CSS {
		if g.seenCSS[css] {
			continue
		}
		g.seenCSS[css] = true
		g.stylesheets = append(g.stylesheets, css)
	}

	for _, importKey := range chunk.Imports {
		imported, ok := g.manifest[importKey]
		if !ok {
			continue
		}
		// An entry gets its own <script> tag, so preloading it as well would
		// tell the browser to fetch the same module under two different hints.
		if !g.visited[importKey] && !g.seenPreload[importKey] && !g.entries[importKey] {
			g.seenPreload[importKey] = true
			g.preloads = append(g.preloads, imported.File)
		}
		g.descend(importKey)
	}
}

// Preloads returns the resolved asset URLs for all code-split JS chunks
// imported by the given entry. The URLs are returned unescaped, ready for an
// HTTP header or a JSON payload; see PreloadHeader for a formatted Link value.
func (e *Engine) Preloads(entry string) ([]string, error) {
	graph, err := e.walk(e.resolveEntries([]string{entry}))
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(graph.preloads))
	for _, file := range graph.preloads {
		urls = append(urls, e.assetURL(file))
	}
	return urls, nil
}

// PreloadHeader returns a value for the HTTP Link header covering every
// stylesheet and code-split chunk the given entries need. Sending it as a 103
// Early Hints response — or alongside the final response — lets the browser
// start fetching assets before it has parsed a single byte of the document.
//
// It returns an empty string in dev mode, where Vite handles module discovery.
func (e *Engine) PreloadHeader(entries ...string) (string, error) {
	if _, ok := e.DevURL(); ok {
		return "", nil
	}
	graph, err := e.walk(e.resolveEntries(entries))
	if err != nil {
		return "", err
	}
	links := make([]string, 0, len(graph.preloads)+len(graph.stylesheets))
	for _, file := range graph.stylesheets {
		links = append(links, fmt.Sprintf("<%s>; rel=preload; as=style", e.assetURL(file)))
	}
	for _, file := range graph.preloads {
		links = append(links, fmt.Sprintf("<%s>; rel=modulepreload; as=script", e.assetURL(file)))
	}
	return strings.Join(links, ", "), nil
}

// FuncMap exposes vitekit to Go html/templates.
//
//	{{ vite "src/main.tsx" }}              one entry
//	{{ vite "src/app.ts" "src/admin.css" }} several, deduplicated
//	{{ viteAsset "src/images/logo.png" }}   a hashed asset URL
func (e *Engine) FuncMap(options RenderOptions) template.FuncMap {
	return template.FuncMap{
		"vite": func(entries ...string) (template.HTML, error) {
			tags, err := e.TagsFor(entries, options)
			return template.HTML(tags), err
		},
		"viteAsset": func(name string) (string, error) {
			url, ok := e.Asset(name)
			if !ok {
				return "", fmt.Errorf("vitekit: asset %q not found in manifest", name)
			}
			return url, nil
		},
		"viteReactPreamble": e.ReactPreamble,
	}
}

// ReactPreamble returns the Vite React refresh bootstrap for Go-served HTML.
// Vite cannot inject this itself when the backend owns the document response.
func (e *Engine) ReactPreamble() template.HTML {
	devURL, ok := e.DevURL()
	if !ok {
		return ""
	}
	base := template.HTMLEscapeString(strings.TrimRight(devURL, "/"))
	return template.HTML(fmt.Sprintf(`<script type="module">
import RefreshRuntime from "%s/@react-refresh";
RefreshRuntime.injectIntoGlobalHook(window);
window.$RefreshReg$ = () => {};
window.$RefreshSig$ = () => (type) => type;
window.__vite_plugin_react_preamble_installed__ = true;
</script>`, base))
}

// Asset resolves a logical asset name to its Vite-generated,
// hashed filename — e.g. Asset("src/images/logo.png") returns
// "/assets/logo-a8f31c.png".
//
// The URL is returned unescaped. html/template escapes it on insertion, and
// callers writing headers or JSON need the raw value.
func (e *Engine) Asset(name string) (string, bool) {
	asset, ok := e.lookup(name)
	if !ok {
		return "", false
	}
	return e.assetURL(asset.File), true
}

func (e *Engine) assetURL(file string) string {
	return strings.TrimRight(e.assetsPrefix, "/") + "/" + strings.TrimLeft(file, "/")
}

func nonceAttribute(options RenderOptions) string {
	if options.Nonce == "" {
		return ""
	}
	return fmt.Sprintf(` nonce="%s"`, template.HTMLEscapeString(options.Nonce))
}

func isStylesheet(path string) bool {
	return strings.HasSuffix(path, ".css")
}
