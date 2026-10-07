# Security

## Reporting a vulnerability

Please report security issues privately rather than in a public issue.

Use GitHub's private vulnerability reporting — **Security → Report a
vulnerability** on this repository — which opens a channel visible only to the
maintainers.

<!-- Replace with a monitored address if you prefer email reports. -->

Please include the version or commit, the deployment mode (`embed.FS` or
`os.DirFS`), and the smallest reproduction you have. You will get an
acknowledgement within a week.

Do not open a public issue, discussion or pull request for a suspected
vulnerability until a fix is released.

## Supported versions

vitekit is pre-1.0. Fixes land on the latest minor release; there are no
backports to earlier ones.

## What is in scope

vitekit resolves a build manifest into HTML and serves the files that build
produced. The parts worth attacking are:

- **`Engine.AssetHandler`** — the only path that turns a request into a file
  read. Anything that serves a file outside the configured output directory, or
  exposes Vite's `.vite` build metadata, is a vulnerability.
- **`Engine.Tags` and friends** — everything rendered into HTML is escaped. A
  manifest value that reaches an attribute or a script body unescaped is a
  vulnerability.
- **`Engine.ContentSecurityPolicy`** — a policy that authorizes more than the
  tags the engine actually emits is a vulnerability.
- **`DevOrchestrator` and `vitekit/cli`** — these run commands and bind ports.
  Anything that runs a command the operator did not configure, or exposes a
  development server more widely than requested, is a vulnerability.

These properties are covered by fuzz tests in `fuzz_test.go`: path traversal
against the asset handler, HTML escaping of manifest values, and the port
injection that rewrites a dev command.

## Deployment considerations

### `os.DirFS` does not stop symlinks leaving the directory

If you serve a build from disk:

```go
vitekit.Init(os.DirFS("./frontend"))
```

then a symlink *inside* that directory pointing outside it will be followed, and
the asset handler will serve whatever it points at. This is [documented
behavior of `os.DirFS`][dirfs], which is not a chroot: it guarantees only that
the paths it opens begin with the given prefix.

This matters when something other than you can write into the build output — a
compromised or careless build step, a dependency's `postinstall`, or files
copied verbatim out of Vite's `public/` directory.

Two ways to avoid it:

**Embed the build.** `go:embed` does not follow symlinks out of the module, so a
single-binary deployment is not affected at all:

```go
//go:embed all:frontend/dist
var assets embed.FS

vitekit.Init(assets)
```

**Or open the directory as a root** (Go 1.24+), which refuses any path that
escapes it:

```go
root, err := os.OpenRoot("./frontend")
if err != nil {
	log.Fatal(err)
}
defer root.Close()

vitekit.Init(root.FS())
```

vitekit works with either unchanged.

[dirfs]: https://pkg.go.dev/os#DirFS

### The development server is not hardened

`vitekit dev` and `cli.Dev` exist to serve one developer on one machine. The
built-in preview server binds `127.0.0.1` for that reason. Pass `--preview-host`
(or `DevOptions.PreviewHost`) only when something outside the machine genuinely
has to reach it, and never expose it to an untrusted network: it has no
authentication and renders whatever the entry point produces.

The dev command itself is executed through the system shell. It is configuration
you supply — treat `VITEKIT_DEV_COMMAND` and `--cmd` with the same care as any
other value that becomes a command line.

### Content Security Policy

`Engine.ContentSecurityPolicy` is deliberately looser in development: the Vite
client needs its own origin, an HMR websocket, and `'unsafe-inline'` for the
style elements its plugin pipeline injects. Serve the production policy in
production — that happens automatically, because the engine leaves development
mode when the `.vite/hot` marker is gone.

Pass a fresh nonce per response from `vitekit.NewNonce`, and pass the *same*
nonce to both the header and `RenderOptions`.
