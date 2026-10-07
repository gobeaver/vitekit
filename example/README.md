# ViteKit examples

Five programs sharing one Vite project, each showing a different way to wire it
to a Go server. Run them all from *this* directory:

```sh
cd example
npm --prefix frontend install
npm --prefix frontend run build     # needed by everything except the dev loop
go run ./stdlib
```

| Example | What it shows |
|---|---|
| [`stdlib/`](stdlib) | The smallest integration: `net/http`, one entry, no framework, no template files. |
| [`gin/`](gin) | React with Fast Refresh through a Go-served document, `html/template` + `FuncMap`, the Gin adapter, and a JSON API. |
| [`echo/`](echo) | The Echo adapter's extended context (`ctx.Vite(...)`), and `WithPrefix` for an engine that owns its own watcher instead of the package singleton. |
| [`multientry/`](multientry) | Several entries in one render with a shared chunk emitted once, a CSP nonce, and preload hints sent as a `Link` header. |
| [`embed/`](embed) | The production deployment: the whole frontend compiled into the binary with `go:embed`, configured with no options at all. |

Each listens on `:8080`; set `PORT` to change it.

## The shared frontend

[`frontend/`](frontend) is one Vite project with four entries, so the examples
can show more than a single script tag:

| Entry | Why it exists |
|---|---|
| `src/main.jsx` | React, for the Fast Refresh path |
| `src/app.ts` | TypeScript with no framework |
| `src/admin.ts` | A second entry that imports the same module as `app.ts`, so Vite emits a shared chunk |
| `src/theme.css` | A standalone CSS entry, which renders as `<link rel="stylesheet">` rather than a module script |

ViteKit only reads Vite's output, so the framework is Vite's business. React,
TypeScript, Vue and Svelte builds all resolve the same way.

## Development, with hot module replacement

From the repository root, one command runs Vite and a Go server together and
stops both on Ctrl+C:

```sh
go run ./cmd/vitekit dev --dir ./example/frontend --backend "go run ./gin" --backend-dir ./example
```

Or drive the two yourself:

```sh
npm --prefix frontend run dev    # terminal 1
go run ./gin                     # terminal 2
```

Either way the Go server finds the dev server on its own by watching
`frontend/.vite/hot`, including when Vite falls back off port 5173.

## Production

```sh
npm --prefix frontend run build
go run ./gin
```

With no `.vite/hot` present the engine switches to the manifest and serves
`frontend/dist/`: hashed filenames, the full CSS chain, `modulepreload` hints
for code-split chunks, and immutable cache headers. No code changes between the
two modes.

## A note on `embed/dist`

That directory is committed, which a real project would not do. It is genuine
(trimmed) Vite output, kept in version control so the example compiles and is
tested without needing a Node toolchain — `go:embed` fails at build time if its
pattern matches nothing. In your own project, build `dist/` as part of the
release and leave it gitignored.
