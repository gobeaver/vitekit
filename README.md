# ViteKit

[![CI](https://github.com/michael-amedaz/vitekit-gobeaver/actions/workflows/ci.yml/badge.svg)](https://github.com/michael-amedaz/vitekit-gobeaver/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/michael-amedaz/vitekit-gobeaver.svg)](https://pkg.go.dev/github.com/michael-amedaz/vitekit-gobeaver)

ViteKit connects a Vite frontend to a Go backend — the Laravel Vite / vite-ruby
experience, for Go.

```sh
go get github.com/michael-amedaz/vitekit-gobeaver
```

- **One command for both servers.** `vitekit dev` runs Vite and your Go server
  together, streams both logs, and one Ctrl+C stops everything — including the
  `node` process Vite forks.
- **No hot-file plugin.** Go writes and cleans up `.vite/hot` itself, and picks
  up the port Vite actually bound, including when 5173 was taken.
- **Complete tags.** The manifest's dependency graph is walked in full, so the
  CSS import chain, `modulepreload` hints for code-split chunks, and the entry
  script all come out in the right order.
- **Assets served properly.** Content-hashed files are cached permanently,
  everything else is revalidated, `.br`/`.gz` sidecars are used when the build
  produced them, and frontend MIME types are corrected.
- **Built for deployment.** Reads through any `io/fs.FS`, so `go:embed` works;
  reloads the manifest atomically during a rolling deploy without dropping a
  request.
- **CSP that actually works**, in development as well as production.
- Adapters for `net/http`, Gin, Fiber and Echo.

Works with vanilla JS, TypeScript, React, Vue, Svelte and Angular — ViteKit
consumes Vite's output, so whatever Vite compiles, it serves.

## Requirements

- Go 1.25 or newer, for every module.
- Node.js and npm/pnpm/yarn/bun for the frontend.

## Quick start

```sh
go install github.com/michael-amedaz/vitekit-gobeaver/cmd/vitekit@latest

vitekit init --dir ./frontend
npm --prefix ./frontend install
vitekit dev --dir ./frontend --backend "go run ."
```

`vitekit dev` starts Vite, waits for it to listen, writes `.vite/hot`, then
starts your Go server. Drop `--backend` and it runs a built-in preview server
instead, so you can see the frontend working before writing any Go.

## Using it from Go

Initialize once at startup against any `fs.FS`. Nothing needs configuring for
an embedded build — a `go:embed` pattern keeps its own directory prefix, so the
manifest is not at a conventional path, and ViteKit locates it and infers the
output directory from it:

```go
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := vitekit.Init(assets); err != nil {
		log.Fatal(err)
	}
	engine := vitekit.Service()

	tmpl := template.Must(template.New("index").
		Funcs(engine.FuncMap(vitekit.RenderOptions{})).
		ParseFiles("templates/index.html"))

	// Mounted with no path stripping: the default assets prefix reproduces
	// Vite's own layout, so /assets/main-BsO1RtEz.js maps to
	// dist/assets/main-BsO1RtEz.js.
	assetHandler, _ := engine.AssetHandler()
	http.Handle("/assets/", assetHandler)
}
```

In the template:

```gotemplate
{{ vite "src/main.tsx" }}                  {{/* one entry */}}
{{ vite "src/app.ts" "src/admin.css" }}    {{/* several, deduplicated */}}
{{ viteAsset "src/images/logo.png" }}      {{/* a hashed asset URL */}}
{{ viteReactPreamble }}                    {{/* React Fast Refresh, dev only */}}
```

Nothing here changes between development and production. ViteKit detects which
mode it is in by watching for `.vite/hot`.

### Content Security Policy

```go
nonce, _ := vitekit.NewNonce()
w.Header().Set("Content-Security-Policy", engine.ContentSecurityPolicy(nonce))
tags, _ := engine.TagsWithOptions("src/main.tsx", vitekit.RenderOptions{Nonce: nonce})
```

`ContentSecurityPolicy` emits `'strict-dynamic'` so the chunks your entry module
imports are covered — a nonce alone does not authorize them — and in development
it also allows the Vite client's origin, its HMR websocket, and the `<style>`
elements Vite injects at runtime.

### Early hints

```go
if header, _ := engine.PreloadHeader("src/main.tsx"); header != "" {
	w.Header().Set("Link", header)
}
```

Returns a `Link` value covering the stylesheets and code-split chunks the entry
needs, ready for a 103 Early Hints response. Empty in development, where Vite
discovers modules itself.

## CLI

```
vitekit dev [options]      Start Vite and your Go server together
vitekit build [options]    Run the production Vite build
vitekit init [options]     Scaffold Vite config for a Go backend
vitekit version            Print the version
```

`vitekit dev` flags: `--dir`, `--cmd`, `--backend`, `--backend-dir`, `--entry`,
`--vite-port`, `--port`, `--preview-host`. The port flags are advisory — if a
port is taken, the next free one is used and the backend is told about it. The
built-in preview server binds `127.0.0.1`; `--preview-host 0.0.0.0` exposes it,
which you want only for a container or another device on the network.

The `--cmd` value has host and port flags appended for npm, pnpm, yarn, bun,
npx and bare `vite` invocations. A command that already pins `--port` is left
alone.

### Embedding the commands in your own CLI

The `vitekit` binary is a thin shell over `vitekit/cli`, which exposes the same
three commands as ordinary functions. Import it to offer `dev`, `build` and
`init` from a CLI you already ship, instead of asking people to install a second
tool:

```go
import "github.com/michael-amedaz/vitekit-gobeaver/cli"

err := cli.Dev(ctx, cli.DevOptions{
    Dir:     "./frontend",
    Backend: "go run ./cmd/server",
    Stdout:  cmd.OutOrStdout(),
    Stderr:  cmd.ErrOrStderr(),
    OnEvent: func(e cli.Event) {
        switch e.Kind {
        case cli.EventViteReady:
            log.Info("vite", "url", e.URL)
        case cli.EventPreviewReady:
            log.Info("preview", "url", e.URL)
        }
    },
})
```

`Build` and `Scaffold` follow the same shape. The package is built to sit inside
a host CLI: it parses no flags, reads no `os.Args`, never calls `os.Exit`, and
writes only to the writers it is handed — an unset writer discards rather than
falling back to `os.Stdout`. Every option has a working default, and progress
arrives through `OnEvent` so milestones render in your own style rather than
vitekit's.

It holds no package-level state either. `Dev`'s built-in preview server
constructs its own `Engine` rather than the process-wide one `Init` installs, so
embedding these commands cannot disturb an engine your program already set up.

`cmd/vitekit` is the reference implementation of exactly this wiring.

## Configuration

Options: `WithManifestPath`, `WithOutputDir`, `WithHotFilePath`,
`WithAssetsPrefix`, `WithEntry`, `WithDevCommand`, `WithRootDir`, `WithDevMode`.

`WithAssetsPrefix` sets the URL prefix the build output is served under, and
must match where `AssetHandler` is mounted. The default `/` reproduces Vite's
own layout — `dist/assets/main-BsO1RtEz.js` is served as
`/assets/main-BsO1RtEz.js`. Behind a reverse proxy serving the app from a
subdirectory, set it to that subdirectory: `WithAssetsPrefix("/app/")` yields
`/app/assets/main-BsO1RtEz.js`.

`WithRootDir` tells ViteKit where your `fs.FS` is rooted on disk. The engine
always reads through the `fs.FS`; this is what lets the file watcher attach OS
notifications to the right directory when the frontend is not in the working
directory. It is irrelevant for `embed.FS`.

Environment variables use the `VITEKIT_` prefix:

```
VITEKIT_MANIFEST_PATH   VITEKIT_OUTPUT_DIR    VITEKIT_DEV_COMMAND
VITEKIT_HOT_FILE        VITEKIT_ASSETS_PREFIX VITEKIT_ENTRY
VITEKIT_ROOT_DIR
```

`BEAVER_VITE_*` is still read as a fallback for applications that adopted
ViteKit as part of Beaver Kit.

For an app with more than one Vite build, `WithPrefix` gives each its own
engine and its own environment variables:

```go
admin, cancel, err := vitekit.WithPrefix("ADMIN_VITE_").New(adminFS)
```

## Adapters

The core module depends only on `fsnotify`. Router integrations are separate
modules so importing ViteKit never pulls in a framework you do not use.

| Adapter | Import |
|---|---|
| `net/http` (and chi, gorilla/mux, anything using `http.Handler`) | `github.com/michael-amedaz/vitekit-gobeaver/adapter/stdlib` |
| Gin | `github.com/michael-amedaz/vitekit-gobeaver/adapter/gin` |
| Fiber | `github.com/michael-amedaz/vitekit-gobeaver/adapter/fiber` |
| Echo | `github.com/michael-amedaz/vitekit-gobeaver/adapter/echo` |

The stdlib adapter is standard `func(http.Handler) http.Handler` middleware, so
it works unchanged with chi, gorilla/mux and every other `net/http` router.

## Vite configuration

ViteKit needs exactly two things from `vite.config.js`:

```js
export default defineConfig({
  build: {
    manifest: true,   // ViteKit reads .vite/manifest.json
    outDir: 'dist',
    rollupOptions: { input: 'src/main.jsx' },
  },
  server: {
    cors: true,       // the Go server owns the document; modules load cross-origin
  },
})
```

No hot-file plugin. `vitekit init` writes this for you.

## Security

Vulnerability reports go through GitHub's private reporting, never a public
issue — see [SECURITY.md](SECURITY.md), which also documents what is in scope
and how the CSP behaves in each mode.

One deployment detail is worth repeating here, because it is easy to hit by
accident. Serving a build from disk with `os.DirFS` will follow a symlink that
points *out* of the directory and serve whatever is on the other end:

```go
vitekit.Init(os.DirFS("./frontend"))   // a symlink inside can escape
```

That is [documented behavior of `os.DirFS`][dirfs] rather than something vitekit
adds — it guarantees only that the paths it opens start with the given prefix,
and it is not a chroot. It matters when anything other than you can write into
the build output: a build step, a dependency's `postinstall`, or files copied
verbatim from Vite's `public/`.

Embedding the build avoids it entirely, because `go:embed` will not follow a
symlink out of the module:

```go
//go:embed all:frontend/dist
var assets embed.FS

vitekit.Init(assets)
```

If you must serve from disk, open the directory as a root (Go 1.24+), which
refuses any path that leaves it:

```go
root, err := os.OpenRoot("./frontend")
if err != nil {
	log.Fatal(err)
}
defer root.Close()

vitekit.Init(root.FS())
```

vitekit takes either without changes.

The built-in preview server binds `127.0.0.1`. Pass `--preview-host 0.0.0.0`
only when something off the machine has to reach it — it has no authentication.

[dirfs]: https://pkg.go.dev/os#DirFS

## Examples

Five programs sharing one Vite project, in [`example/`](example):

| Example | What it shows |
|---|---|
| `stdlib/` | The smallest integration: `net/http`, one entry, no framework. |
| `gin/` | React with Fast Refresh through a Go-served document, `html/template` + `FuncMap`, the Gin adapter. |
| `echo/` | The Echo adapter's extended context, and `WithPrefix` for a self-owned engine. |
| `multientry/` | Several entries with a shared chunk emitted once, a CSP nonce, and `Link` preload hints. |
| `embed/` | Single-binary production deployment with `go:embed` and no configuration. |

```sh
cd example
npm --prefix frontend install
npm --prefix frontend run build
go run ./stdlib
```

For the hot-reloading dev loop, from the repository root:

```sh
go run ./cmd/vitekit dev --dir ./example/frontend --backend "go run ./gin" --backend-dir ./example
```

See [example/README.md](example/README.md).

## Development

The repository is several Go modules, so use the Makefile rather than
`go test ./...`, which would only cover the core one:

```sh
make test     # every module, with -race
make lint     # golangci-lint, which covers govet and gofmt
make tidy
```

Fuzz targets cover manifest parsing, HTML escaping, the asset handler's path
handling and dev-command port injection. CI runs a short campaign on every push;
run a longer one when you touch those paths:

```sh
go test -run '^$' -fuzz FuzzAssetHandlerStaysInsideTheOutputDirectory -fuzztime=5m .
```

Benchmarks cover the per-request paths — `Tags` and the asset handler run on
every page view:

```sh
go test -run '^$' -bench . .
```

[CONTRIBUTING.md](CONTRIBUTING.md) covers the repository layout and what a
change is expected to come with. Releasing is documented in
[RELEASING.md](RELEASING.md), including `scripts/set-module-path.sh` for moving
the package to its permanent import path.

The race detector runs in CI on every job. To run it locally without a system C
compiler, [zig](https://ziglang.org) works as one:

```sh
CGO_ENABLED=1 CC="zig cc" go test -race ./...
```

## License

Apache-2.0. See [LICENSE](LICENSE).
