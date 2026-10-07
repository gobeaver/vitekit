# Releasing

The repository holds four published Go modules:

| Module | Import path |
|---|---|
| core (includes `adapter/stdlib`) | `github.com/michael-amedaz/vitekit-gobeaver` |
| Gin adapter | `github.com/michael-amedaz/vitekit-gobeaver/adapter/gin` |
| Fiber adapter | `github.com/michael-amedaz/vitekit-gobeaver/adapter/fiber` |
| Echo adapter | `github.com/michael-amedaz/vitekit-gobeaver/adapter/echo` |

They are separate modules on purpose: importing the core must not drag Gin,
Fiber and Echo — and their transitive dependencies — into an application that
uses none of them. CI enforces this in the `dependencies` job.

`go.work` and the `replace` directives in the adapter modules exist only so the
working tree builds against itself. Consumers never see either: Go ignores
`go.work` outside this repository, and a `replace` in a dependency module has no
effect on the module that requires it.

## Setting the module path

The path is a placeholder until the package has a permanent home. Changing it
touches four `go.mod` files (module lines plus the adapters' `require` and
`replace` directives), every import in every module, and the docs, so it is
scripted rather than done by hand:

```sh
scripts/set-module-path.sh github.com/your-org/vitekit
```

The script rewrites the tracked files, tidies every module, and then builds,
vets and tests all five to prove the result is coherent before you commit.

## Order of operations

The adapters depend on the core module, so the core has to be tagged first —
its tag must exist on the proxy before an adapter that requires it is tagged.

1. Verify everything, across every module:

   ```sh
   make tidy && make vet && make test
   ```

2. Tag and push the core module:

   ```sh
   git tag v0.1.0
   git push origin v0.1.0
   ```

3. Point each adapter at that released version, replacing the placeholder
   requirement and dropping the local `replace`.

   The version is set with `go mod edit -require` rather than `go get`. Once
   the `replace` is gone, the placeholder `v0.0.0` in the requirement is a
   version that never existed, and `go get` fails trying to resolve it before
   it can replace it. `go mod edit` rewrites the line without consulting the
   network, and the following `go mod tidy` fills in the checksums:

   ```sh
   for module in adapter/gin adapter/fiber adapter/echo; do
     (cd "$module" \
       && go mod edit -dropreplace github.com/michael-amedaz/vitekit-gobeaver \
       && go mod edit -require=github.com/michael-amedaz/vitekit-gobeaver@v0.1.0 \
       && GOWORK=off go mod tidy)
   done
   git commit -am "Point adapters at core v0.1.0"
   git push
   ```

4. Tag the adapters. A module in a subdirectory is tagged with that
   subdirectory as the prefix:

   ```sh
   git tag adapter/gin/v0.1.0
   git tag adapter/fiber/v0.1.0
   git tag adapter/echo/v0.1.0
   git push origin --tags
   ```

5. Restore the local `replace` directives so the working tree keeps building
   against itself rather than the released version:

   ```sh
   for module in adapter/gin adapter/fiber adapter/echo; do
     (cd "$module" && go mod edit -replace github.com/michael-amedaz/vitekit-gobeaver=../..)
   done
   git commit -am "Restore local replace directives"
   ```

## Compatibility

- Every module builds with Go 1.25 and later. The adapters have always
  required it because Gin, Fiber and Echo do; the core module joined them when
  `golang.org/x/sys` — which the Windows process handling needs — raised its own
  floor past 1.23.
- `VITEKIT_*` environment variables are the supported configuration surface.
  `BEAVER_VITE_*` is read as a fallback for applications that adopted vitekit
  as part of Beaver Kit, and will keep working for the whole v0 and v1 line.
