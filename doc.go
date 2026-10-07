// Package vitekit connects a Vite frontend to a Go backend.
//
// It resolves Vite's manifest into the exact HTML tags a page needs, serves the
// built assets, and — through [DevOrchestrator] — runs the Vite dev server and
// the Go server as a single process with a single Ctrl+C.
//
// # Two modes, detected automatically
//
// vitekit switches between development and production on its own by watching
// for the .vite/hot marker file that the dev server writes. In development,
// [Engine.Tags] points at the running Vite server so hot module replacement
// works; in production it walks the manifest. Nothing in application code
// changes between the two.
//
// # Getting started
//
// Initialize once at startup against any [io/fs.FS] — os.DirFS while
// developing, an embed.FS for a single-binary deployment:
//
//	//go:embed all:frontend/dist
//	var assets embed.FS
//
//	func main() {
//		if err := vitekit.Init(assets); err != nil {
//			log.Fatal(err)
//		}
//		engine := vitekit.Service()
//		...
//	}
//
// No paths need spelling out. A go:embed pattern keeps its own directory
// prefix, so the manifest ends up at frontend/dist/.vite/manifest.json rather
// than anywhere conventional; vitekit finds it and infers the output directory
// from it. Pass [WithOutputDir] to override that inference.
//
// Render the tags for an entry from a template:
//
//	tmpl := template.Must(template.New("index").
//		Funcs(engine.FuncMap(vitekit.RenderOptions{})).
//		ParseFiles("templates/index.html"))
//
//	{{ vite "src/main.tsx" }}
//
// and mount the built assets below the configured prefix:
//
//	mux.Handle("/assets/", http.StripPrefix("/assets", assetHandler))
//
// # Serving a build from disk
//
// [os.DirFS] is not a chroot: a symlink inside the directory that points out of
// it will be followed, and [Engine.AssetHandler] will serve what it points at.
// Embedding the build avoids this — go:embed does not follow a symlink out of
// the module — and so does opening the directory as a root on Go 1.24 and
// later, which vitekit accepts unchanged:
//
//	root, err := os.OpenRoot("./frontend")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer root.Close()
//
//	vitekit.Init(root.FS())
//
// See SECURITY.md for the rest of the deployment notes.
//
// # Concurrency
//
// An [Engine] is safe for concurrent use. The manifest and the dev-server URL
// are held in atomic pointers and replaced wholesale, so a rolling deploy that
// rewrites the manifest underneath a running server never blocks a request and
// never exposes a half-updated view: a request either sees the old manifest or
// the new one.
//
// # Content Security Policy
//
// Pass a per-response nonce through [RenderOptions] and send the matching
// policy from [Engine.ContentSecurityPolicy], which accounts for ES module
// imports and, in development, for everything the Vite client needs to connect.
//
//	nonce, err := vitekit.NewNonce()
//	w.Header().Set("Content-Security-Policy", engine.ContentSecurityPolicy(nonce))
//	tags, err := engine.TagsWithOptions("src/main.tsx", vitekit.RenderOptions{Nonce: nonce})
//
// # Driving the commands from your own CLI
//
// The vitekit binary's commands — dev, build and init — are also available as
// ordinary functions in [github.com/michael-amedaz/vitekit-gobeaver/cli], for a project that
// would rather offer them from a CLI it already ships than ask for a second tool
// to be installed. That package parses no flags, never calls os.Exit, writes
// only to the writers it is handed, and holds no package-level state.
//
// # Framework adapters
//
// The core package depends only on fsnotify. Router integrations live in
// separate modules so that importing vitekit never pulls in a web framework
// you do not use:
//
//	github.com/michael-amedaz/vitekit-gobeaver/adapter/stdlib  (in this module; no dependencies)
//	github.com/michael-amedaz/vitekit-gobeaver/adapter/gin
//	github.com/michael-amedaz/vitekit-gobeaver/adapter/fiber
//	github.com/michael-amedaz/vitekit-gobeaver/adapter/echo
package vitekit
