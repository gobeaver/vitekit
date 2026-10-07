`vitekit` — Vite Integration for Beaver Kit

**Status:** Draft for discussion
**Target repo:** `github.com/gobeaver/beaver-kit` (new package: `vitekit`)
**Author:** Michael Osta
**Related work:** `go-beaver-tag-sanitization` — solves a different, unrelated problem (sanitizing untrusted user-authored pages) and is referenced in §5.4 only to explain why `vitekit` doesn't build on it for CSP/nonce handling

---

## 1. Summary

Add `vitekit` to Beaver Kit: a thread-safe, driver-agnostic Go package that bridges a Vite frontend build to a Go backend, plus a `beaver dev` CLI orchestrator that runs Vite and the Go server as one process. It follows Beaver Kit's existing conventions (`Init()` / `Service()` / `Reset()`, `BEAVER_` env prefix, `WithPrefix()` builder, minimal dependencies) and slots in next to `filekit`, `krypto`, and the forthcoming sanitization package rather than duplicating what they already do.

---

## 2. Motivation

Existing Go/Vite bridges — `olivere/vite` being the most widely used — cover the basic case well (parse `manifest.json`, emit `<script>`/`<link>` tags, optionally serve `dist/`) but stop there. Running real apps against them in Beaver-style deployments (containers, rolling Kubernetes updates, `go:embed` binaries, multi-framework routing) surfaces the same set of gaps repeatedly:

**Process management**
- Two terminals (`npm run dev` + `go run`), with no coordination — killing one leaves the other running.
- Vite's automatic port fallback (5173 → 5174 when busy) isn't detected, so the backend serves broken script tags.
- No scaffolding/`init` step — manually wiring `vite.config.js` paths, aliases, and manifest locations.

**Filesystem & manifest handling**
- Vite's manifest moved to `.vite/manifest.json`; some packages still assume the old root-level path and don't recover if the path is customized.
- Manifests are read once at startup. In a rolling deploy where assets rebuild mid-flight, the server either crashes or serves a stale manifest until restarted.
- No first-class support for `go:embed` — packages assume a real path on disk rather than an `fs.FS`.

**Static asset delivery**
- Tag generation only; serving the files, setting MIME types correctly for `.ts`/`.jsx`/chunked `.css`, and handling reverse-proxy subpaths is left to the developer.

**Advanced Vite features**
- No `modulepreload` support for code-split chunks (sequential instead of parallel fetches).
- CSS import chains beyond the top-level entry are frequently missed, causing flash-of-unstyled-content.
- No lookup path for `import.meta.url`-based dynamic asset URLs.

**Architecture gaps**
- No Inertia.js-style header handling, no SSR bridge, and no way to inject CSP nonces into generated tags — all of which are needed for anything beyond a toy app.

None of this is a knock on `olivere/vite` specifically — it does the manifest-to-tags job cleanly and is a reasonable dependency-free reference implementation. The gap is everything *around* that job: process lifecycle, hot-reload behavior, concurrency safety under rolling updates, and framework interoperability, which is exactly the space Beaver Kit already operates in.

---

## 3. Goals

1. **One-command dev loop.** `beaver dev` boots Vite and the Go server together, streams both logs, and guarantees Vite is killed on `Ctrl+C` — no zombie processes, no manual port bookkeeping.
2. **Correct, flicker-free asset output.** Recursively resolve the manifest's dependency graph so `<script>`, `<link rel="stylesheet">`, and `<link rel="modulepreload">` tags come out complete and in the right order.
3. **Drop-in interoperability.** Works with `net/http`, Gin, Echo, and Fiber without the host app rewriting its routing, and composes with `filekit` when assets live somewhere other than the local disk (S3, embedded, memory).

---

## 4. Fit With Beaver Kit's Existing Conventions

`vitekit` should look and feel like every other package in the kit:

```go
import "github.com/gobeaver/beaver-kit/vitekit"

if err := vitekit.Init(); err != nil {
    log.Fatal(err)
}

engine := vitekit.Service()
tags, err := engine.Tags(ctx, "src/main.tsx", vitekit.RenderOptions{Nonce: nonce})
```

| Environment Variable | Description | Default |
|---|---|---|
| `BEAVER_VITE_MANIFEST_PATH` | Path to `manifest.json` inside the build output (auto-detects `.vite/manifest.json` first) | `.vite/manifest.json` |
| `BEAVER_VITE_OUTPUT_DIR` | Build output directory to serve in production | `dist` |
| `BEAVER_VITE_DEV_COMMAND` | Command used to start the Vite dev server | `npm run dev` |
| `BEAVER_VITE_HOT_FILE` | Path to Vite's hot-reload marker file | `.vite/hot` |
| `BEAVER_VITE_ASSETS_PREFIX` | URL prefix assets are served/rewritten under (handles reverse-proxy subpaths) | `/assets/` |
| `BEAVER_VITE_ENTRY` | Default entry point passed to `Tags()` when none is specified | — |

Multi-instance support follows the same `WithPrefix()` builder pattern used by `database` and `cache`, which matters for apps that build more than one Vite app (admin panel + public site) against a single Go binary:

```go
adminVite, err := vitekit.WithPrefix("ADMIN_VITE_").New()
publicVite, err := vitekit.WithPrefix("PUBLIC_VITE_").New()
```

Testing story matches the rest of the kit: `vitekit.Reset()` for test teardown, and a `memory`-style manifest source for unit tests that don't want to touch disk.

---

## 5. Architecture

### 5.1 Core engine (concurrency & memory safety)

```go
type Engine struct {
    mu       sync.RWMutex
    manifest atomic.Pointer[Manifest]
    fsys     fs.FS
    devURL   atomic.Pointer[string] // nil when not in dev mode
}
```

- Reads (`Tags()`, `Preloads()`) take `RLock()` and are safe under heavy concurrent traffic.
- Manifest reloads build a fresh map off to the side, then swap it in with `atomic.Pointer`, so a rolling deploy that rebuilds assets mid-flight never blocks readers or serves a half-written manifest.
- The engine is initialized from an `io/fs.FS`, not a hardcoded path — the same code reads a local folder, a `go:embed` binary, or (via `filekit`) an S3/GCS-backed filesystem. This is deliberately consistent with how `filekit` already abstracts storage backends elsewhere in the kit.

### 5.2 Manifest graph resolution

- Vite's manifest is a DAG (entry → imports → nested imports/CSS). `vitekit` walks it depth-first from the requested entry point, tracking visited nodes per-request so shared chunks aren't emitted twice.
- Output ordering: `modulepreload` links for non-entry JS chunks, `stylesheet` links for every CSS file in the chain (not just the top-level one), then the entry `<script>` last — this is the fix for the FOUC/layout-shift problem existing packages have.
- Dynamic asset lookups (`import.meta.url`-style hashed assets) are exposed as a small `Asset(name string) (string, bool)` helper so templates can resolve hashed filenames without bypassing Vite.

### 5.3 Dev-mode detection

- A background watcher polls (or `fsnotify`-watches, reusing `filekit`'s existing watch primitives where possible) for `.vite/hot`.
- Presence → dev mode; the file's contents are the live dev server URL, so a port bump from 5173 to 5174 is picked up automatically instead of hardcoded.
- Absence → production mode, serving from the manifest.

### 5.4 CSP nonces

Earlier drafts of this RFC assumed the in-progress sanitization package (`go-beaver-tag-sanitization`) would own nonce generation and CSP header construction for `vitekit`. Having seen that package's actual scope, that assumption doesn't hold — and not because of a gap, but because it's solving a different problem:

`go-beaver-tag-sanitization` protects **untrusted, user-authored** pages. Its threat model is deliberately extreme — `script-src 'none'` is a non-negotiable invariant enforced at construction time, and the only "script" that ever runs is a hashed, shell-owned declarative runtime interpreting `data-gb-*` attributes. There is no nonce concept in that design at all, because the whole point is that user or AI content is never allowed to become executable script, nonce or not.

`vitekit` sits on the opposite side of the trust boundary: it renders `<script>`/`<link>` tags for **first-party build output the developer owns and controls** (their own bundled JS/CSS from Vite). A CSP nonce here is about letting the app's *own* legitimate scripts run under a strict policy — the reverse problem from what the sanitization package solves. Reusing it here would mean either weakening its `script-src: 'none'` invariant (which it explicitly refuses to allow any profile to do) or building something adjacent to it that doesn't fit its API shape. Neither is a good outcome, and the two use cases genuinely don't overlap enough to be worth forcing into one package.

Given that, `vitekit` should own the nonce lifecycle itself:

- **Nonce generation.** A `vitekit.NewNonce()` helper built on `krypto.GenerateSecureToken()` (already exists, already audited) — no new crypto code, just a thin wrapper with a sane default length.
- **Per-request nonce, not per-tag.** One nonce per HTTP request/render call, reused across every `<script>`/`<link>` tag on that page.
- **`RenderOptions.Nonce`** stays a plain string field so callers can supply their own nonce from elsewhere (e.g. framework middleware that already sets one) instead of `vitekit`'s generator.
- **`CSPHeader(nonce string) string` helper (optional, small).** Builds a minimal `Content-Security-Policy: script-src 'nonce-XYZ'` value for the developer's own asset script-src directive — intentionally narrow, not a general policy builder, and definitely not a rehash of `usercontent/csp/`'s locked-down builder, which solves a different directive shape entirely.
- **No sanitization responsibilities.** Worth stating explicitly since these two packages will sit side by side in the same org: `vitekit` never touches user-generated content and isn't a substitute for `go-beaver-tag-sanitization` — if an app renders untrusted HTML anywhere, that's still that package's job, unrelated to anything `vitekit` does.

This is a small, self-contained addition to `vitekit`'s scope — not new security surface borrowed from elsewhere, since there was nothing to borrow.

### 5.5 Framework adapters

A `vitekit/adapter` subpackage, mirroring how `filekit/driver/*` is organized:

| Adapter | What it does |
|---|---|
| `adapter/stdlib` | `Middleware(e *Engine) func(http.Handler) http.Handler`, injects the engine into request context |
| `adapter/gin` | `gin.HandlerFunc` that calls `c.Set("vite", engine)` |
| `adapter/fiber` | Binds the `template.FuncMap` into Fiber's view engine |
| `adapter/echo` | Extends Echo's context with a `.Vite("src/main.tsx")` method |

All four wrap the same core `Engine` — no adapter reimplements graph traversal or manifest parsing.

### 5.6 CLI orchestration (`beaver dev`)

Handled in the Beaver CLI, not the library itself, so `vitekit` stays usable standalone:

- `cmd.Start()` launches `npm run dev` (or the configured `BEAVER_VITE_DEV_COMMAND`) asynchronously, then boots the Go router — no blocking, no manual second terminal.
- `runtime.GOOS == "windows"` is detected and the command is wrapped in `cmd /c` automatically.
- `signal.NotifyContext` catches `Ctrl+C` and force-kills the Vite child process before the Go process exits, eliminating orphaned Vite processes holding ports open.

---

## 6. Non-goals (v1)

- **Full SSR execution.** Bridging to a Node/Bun sidecar for server-rendered React/Vue is real work and deserves its own RFC once the core engine ships; v1 only guarantees the manifest/tag layer is SSR-friendly (correct entry resolution, nonce support) so an SSR bridge can be layered on later without redesigning the engine.
- **Inertia.js-specific middleware.** v1 exposes what Inertia needs (entry resolution, headers passthrough via adapters) but a dedicated Inertia helper package can come after real usage shows what's actually needed.

---

## 7. Proposed Milestones

| Phase | Deliverable |
|---|---|
| 1 | `Engine` struct, `RWMutex`/atomic-pointer manifest store, `io/fs.FS`-based `Init()` |
| 2 | DFS manifest walker + tag formatting, unit tests against a mocked nested manifest |
| 3 | `RenderOptions`/nonce support (`NewNonce()`, optional `CSPHeader()`), `template.FuncMap` bridge |
| 4 | `adapter/stdlib`, `adapter/gin`, `adapter/fiber`, `adapter/echo` |
| 5 | `.vite/hot` detector, dev/prod state switching |
| 6 | `beaver dev` CLI command: async subprocess, Windows shell handling, signal-based cleanup |

Each phase ships with its own `README.md` and test coverage, consistent with how `filekit`/`filevalidator` are documented today.

---

## 8. Open Questions

1. Package name — `vitekit` (matches `filekit`) vs. `vite` (matches `olivere/vite` naming, but collides awkwardly if someone imports both).
2. Should manifest reload be push-based (`fsnotify` on `manifest.json`) or pull-based (checked lazily on request with a debounce)? Push is more consistent with `filekit`'s `Watch()` support; pull is simpler and avoids a background goroutine per instance.
3. Where should the Inertia/SSR helpers eventually live — inside `vitekit` behind a build tag, or as separate `vitekit-inertia` / `vitekit-ssr` packages once there's real demand?
4. **Nonce/CSP scope, now settled.** With `go-beaver-tag-sanitization`'s actual scope confirmed (untrusted-content sanitization with a `script-src 'none'` invariant, no nonce concept), it's clear `vitekit` should own `NewNonce()` + `CSPHeader()` directly rather than wait on or borrow from that package — the two solve different problems on opposite sides of the trust boundary. No longer blocked on that package's roadmap.
5. Should `vitekit`'s minimal `CSPHeader()` helper live in `vitekit` itself, or is it small/generic enough (nonce + `script-src`) to belong in `krypto` alongside `GenerateSecureToken()`, since it has nothing Vite-specific about it? Leaning toward keeping it in `vitekit` for now since it's the only consumer, but worth a second opinion before it ships.

---

## 9. License

Apache-2.0, matching `beaver-kit`.