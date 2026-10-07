# Contributing

## The shape of the repository

Four published modules live here, and the split is deliberate: importing the
core must not drag Gin, Fiber and Echo into an application that uses none of
them. CI enforces that in the `dependencies` job.

| Path | Module |
|---|---|
| `.` | `github.com/michael-amedaz/vitekit-gobeaver` — core, plus `adapter/stdlib` |
| `adapter/gin` | Gin adapter |
| `adapter/fiber` | Fiber adapter |
| `adapter/echo` | Echo adapter |
| `example` | not published; runnable examples |

`cli/` holds the `dev`, `build` and `init` commands as importable functions, so
another project's CLI can offer them. `cmd/vitekit` is a thin shell over that
package and is the reference for wiring it into a command of your own.

`go.work` and the `replace` directives exist only so the working tree builds
against itself. Consumers never see either.

## Getting set up

```sh
make test     # every module, with -race
make lint     # golangci-lint across every module
make fmt
make build    # bin/vitekit
```

`make lint` needs the linter, pinned to the version CI uses:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
```

It runs `govet` and `gofmt` among its linters, so there is no separate step for
either. `.golangci.yml` documents why each `gosec` rule is excluded — every one
of them describes something this package exists to do, and each has a test
pinning the real property.

`make test` fans out across all five modules, because `go test ./...` at the
root only covers the core one.

The example frontend needs Node:

```sh
npm --prefix example/frontend install
make example
```

`example/embed` is the exception — its `dist/` fixture is committed, so it runs
with no Node toolchain at all:

```sh
cd example && go run ./embed
```

## Before you open a pull request

```sh
make tidy && make lint && make test
```

Run the fuzz targets if you touched manifest parsing, HTML rendering, the asset
handler or dev-command handling:

```sh
go test -run '^$' -fuzz FuzzAssetHandlerStaysInsideTheOutputDirectory -fuzztime=60s .
```

And the benchmarks if you touched anything on the request path — `Tags` and the
asset handler run on every page view:

```sh
go test -run '^$' -bench . .
```

## What the code should look like

Match what is already there rather than any general style guide.

- **Comments say why, not what.** The surrounding code explains the reason a
  thing is done the way it is — a platform quirk, a security property, an
  ordering constraint. A comment that restates the next line is noise.
- **Names are words.** `writer`, `request`, `manifestPath` — not `w`, `r`, `mp`.
- **Errors are wrapped with what was being attempted** and prefixed `vitekit:`
  at the boundary, not at every level.
- **Tests state the property being protected**, and their names read as
  sentences: `TestPreviewServerRendersTheEntry`, not `TestPreview2`.

## Testing expectations

New code needs tests. The core module sits around 85% statement coverage and
`cli` around 90%; a change that drops either meaningfully will be asked about.

Anything touching process handling needs to work on Linux, macOS and Windows —
that is the most platform-specific part of the package, which is why the test
matrix runs the suite on all three rather than just Linux. Use the existing
idiom for a long-running process:

```go
if runtime.GOOS == "windows" {
	command = "ping -n 60 127.0.0.1"
} else {
	command = "sleep 60"
}
```

Concurrency tests must actually overlap. A writer that finishes its rounds
before any reader is scheduled proves nothing, so wait for the readers to start
before beginning — see `concurrency_test.go`.

## Security

Do not open a public issue or pull request for a suspected vulnerability. See
[SECURITY.md](SECURITY.md).

## Releasing

See [RELEASING.md](RELEASING.md). The core module has to be tagged before any
adapter that requires it.
