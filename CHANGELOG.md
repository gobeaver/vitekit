# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

First public release preparation. Nothing has been tagged yet, so none of the
below is a breaking change against a released version.

### Added

- `Engine.TagsFor` renders several entries in one pass, emitting chunks and
  stylesheets they share only once.
- `Engine.PreloadHeader` builds an HTTP `Link` value for 103 Early Hints.
- `Engine.ContentSecurityPolicy` emits a complete policy for the current mode,
  including `'strict-dynamic'` for ES module imports and, in development, the
  Vite client's origin and HMR websocket.
- `viteAsset` template function for resolving hashed asset URLs.
- The `vite` template function is now variadic: `{{ vite "a.ts" "b.css" }}`.
- CSS entries render as `<link rel="stylesheet">` in both modes.
- Precompressed `.br`/`.gz` sidecars are served when the build produced them.
- Content-hashed files are served with immutable cache headers; everything else
  is revalidated.
- Manifest auto-discovery, so an embedded build needs no configuration at all.
- `WithRootDir` tells the file watcher where the `fs.FS` is rooted on disk.
- `vitekit init`, `vitekit build` and `vitekit version` commands; `vitekit dev`
  gained `--backend` so it runs your Go server rather than a built-in demo.
- Dev-command port injection for pnpm, yarn, bun, npx and bare `vite`.
- `SECURITY.md` with a private reporting channel, what is in scope, and the
  deployment notes — including that `os.DirFS` follows symlinks out of the
  build directory, and that `embed.FS` or Go 1.24's `os.Root` avoid it.
- `CONTRIBUTING.md`, issue and pull request templates, and a Dependabot
  configuration covering every module and the example frontend.
- `govulncheck` and a short fuzz campaign run in CI on every push.
- `golangci-lint` with a documented configuration, run across every module in
  CI and by `make lint`. It replaces the separate vet and gofmt steps, which
  were two of the linters it already runs. Each excluded `gosec` rule names the
  behavior it describes and the test that pins the real property.
- Fuzz targets for manifest parsing, HTML escaping of manifest values, the
  asset handler's path handling, and dev-command port injection.
- Benchmarks for the per-request paths: `Tags`, `TagsWithOptions`, `Preloads`,
  `ContentSecurityPolicy` and the asset handler, plus a parallel `Tags` case.
- `DevOptions.PreviewHost` and `vitekit dev --preview-host`, for the container
  and remote-device cases that need the preview server off loopback.
- `darwin/amd64` joins the cross-compile matrix, covering Intel Macs.
- `vitekit/cli` package exposing `Dev`, `Build` and `Scaffold` as importable
  functions, so a framework's existing CLI can offer vitekit's commands without
  shelling out to the binary. The commands parse no flags, never call
  `os.Exit`, write only to the writers they are given, report progress through
  an `OnEvent` callback, and touch no package-level state. `cmd/vitekit` is now a
  thin shell over them, with identical output.

### Fixed

- **`Stop` waits for the process tree to actually exit**, rather than returning
  once the kill has been requested. `taskkill` returns as soon as it has asked,
  and until the processes really disappear Windows keeps their working
  directory locked — so a caller that deleted that directory next failed with a
  sharing violation.
- **Teardown no longer stalls for the full grace period on Windows when no
  console is attached.** A break event is delivered through a console; a
  service, a detached parent or a CI runner has none, and the call can report
  success while nothing receives it. That case now escalates straight to a
  forced kill instead of waiting five seconds for an exit that was never
  coming.
- `github.com/quic-go/quic-go` moved to v0.59.1 for GO-2026-5676, reachable
  through Gin's HTTP/3 support.
- `golang.org/x/sys` moved to v0.47.0, the newest release that still builds
  with Go 1.25.

- The example servers set `ReadHeaderTimeout` and parse `PORT` as a number
  instead of pasting it into the listen address. `PORT=nonsense` produced a
  confusing bind failure; it is now reported and ignored. `log.Fatal` no longer
  appears after a `defer`, where it skipped the cleanup it was paired with.
- Exported `WithManifestPath`, `WithOutputDir`, `WithDevCommand`,
  `WithHotFilePath`, `WithEntry` and `NewWatcher` have doc comments, and
  `adapter/stdlib` has a package comment, so they read properly on pkg.go.dev.
- The preview server sets `ReadHeaderTimeout` and bounds its shutdown wait, so
  a client that dribbles headers cannot hold a connection, and a stuck request
  cannot hold the dev session open.

- The dev server's whole process tree is now terminated on shutdown. Previously
  only the directly spawned shell was signalled, leaving Vite's `node` process
  running with the port held and the inherited output pipes open — which also
  made shutdown hang indefinitely.
- Vite's stdout and stderr are forwarded to the terminal. They were being read
  for the server URL and then discarded, so build errors were invisible.
- `Preloads` and `Asset` return unescaped URLs. They were HTML-escaped, which is
  wrong for the `Link` headers and JSON payloads they feed; escaping now happens
  where a URL is written into HTML.
- The file watcher resolves OS paths against the `fs.FS` root instead of the
  working directory, so it no longer watches the wrong directory when the
  frontend lives elsewhere.
- The asset handler no longer serves directory listings or Vite's `.vite`
  build metadata.
- A half-written `.vite/hot` no longer drops the engine out of dev mode.
- `Service()` no longer races with `Init()`.
- `vitekit init` printed a `go:embed` directive that did not compile whenever
  the frontend directory was given as an absolute path or as `.` — go:embed
  rejects a leading slash and any `.` element. Absolute directories are now
  resolved relative to the working directory, and one that go:embed genuinely
  cannot reach is told to use `os.DirFS` instead.
- Two concurrency tests raced nothing: nothing scheduled a reader before the
  writer finished its reload rounds, which made
  `TestManifestReloadIsAtomicUnderLoad` fail under load for want of renders and
  let `TestAssetHandlerConcurrentWithReload` pass without serving a request.
  Both now wait for the readers to start.

### Changed

- **The built-in preview server binds `127.0.0.1` rather than every
  interface.** It had been reachable by anyone on the same network, with no
  authentication in front of whatever the entry point renders. Pass
  `--preview-host 0.0.0.0` to restore the old behavior deliberately.
- **The binary's package moved from `cmd/cli` to `cmd/vitekit`**, so
  `go install github.com/michael-amedaz/vitekit-gobeaver/cmd/vitekit@latest` installs a
  command named `vitekit`. The old path installed one named `cli`, and it also
  collided with the new `cli` package, breaking `go build ./cmd/cli` from the
  repository root.
- **Module layout.** The Gin, Fiber and Echo adapters are separate Go modules,
  so the core module's dependency footprint is `fsnotify` alone.
- **Minimum Go version is 1.25 for every module.** The adapters always required
  it because their frameworks do; the core module joined them because
  `golang.org/x/sys`, which the Windows process handling depends on, raised its
  own floor past 1.23. Go supports only the two most recent releases, so 1.23
  was already outside that window.
- **Default `AssetsPrefix` is now `/`** rather than `/assets/`, which had
  produced `/assets/assets/...` because Vite's own asset directory is already
  named `assets`. Mount `AssetHandler` with no path stripping.
- **Environment prefix is `VITEKIT_`**; `BEAVER_VITE_` is still read as a
  fallback.
