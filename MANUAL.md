# ViteKit setup manual

This manual describes the implementation in this checkout. Start with the existing example to see it working, or follow the new-project instructions to connect your own Gin backend.

For the internal execution flow, see [WORKFLOW.md](WORKFLOW.md).

## 1. Prerequisites

- Go 1.25 or newer, as required by this repository's modules.
- Node.js and npm compatible with the Vite version in your frontend's `package.json`.
- A terminal with `go`, `node`, and `npm` on `PATH`.

Check your installation:

```sh
go version
node --version
npm --version
```

Commands below work in PowerShell unless marked otherwise. Run each section from the stated directory.

## 2. Run this repository's existing Gin example

From the **ViteKit repository root**:

```sh
npm --prefix ./example/frontend install
go run ./cmd/vitekit dev --dir ./example/frontend --backend "go run ./gin" --backend-dir ./example
```

Open **http://localhost:8080** after the backend and Vite are ready. Open the Go URL because Go renders the HTML page.

This command:

1. Starts Vite in `example/frontend`.
2. Starts the Gin application with `example` as its working directory.
3. Detects Vite's URL and writes `example/frontend/.vite/hot`.
4. Lets the Go watcher detect that URL and generate development script tags.
5. Stops the managed processes and removes the hot marker when you press Ctrl+C.

Vite startup and backend startup run concurrently. If the first page request arrives before the hot marker is available, refresh after Vite is ready.

Edit `example/frontend/src/main.jsx` to try frontend hot updates. The command does not provide automatic Go source rebuilding; restart it after backend changes.

### Try the example in production mode

Stop the development command first. From the **ViteKit repository root**:

```sh
npm --prefix ./example/frontend run build
cd example
go run ./gin
```

Open http://localhost:8080. With no `frontend/.vite/hot` present, Go uses `frontend/dist/.vite/manifest.json` and serves built files from `frontend/dist`.

## 3. Install the CLI from this checkout

From the **ViteKit repository root**:

```sh
go install ./cmd/vitekit
```

Ensure Go's executable install directory is on `PATH`. It is `GOBIN` when configured, otherwise the `bin` directory under `GOPATH`:

```sh
go env GOBIN GOPATH
vitekit version
```

If you do not install the CLI, you can run `go run ./cmd/vitekit ...` from this repository instead. The instructions below assume `vitekit` is available on `PATH`.

## 4. Create your own application

Create a **new application directory outside the ViteKit source directory**:

```sh
mkdir myapp
cd myapp
go mod init example.com/myapp
go get github.com/michael-amedaz/vitekit-gobeaver
go get github.com/michael-amedaz/vitekit-gobeaver/adapter/gin
```

These commands request the published modules. To use your local checkout instead, configure local replacements before running `go get`. Replace the example paths with your real absolute paths; forward slashes work in Go module paths on Windows:

```sh
go mod edit -replace "github.com/michael-amedaz/vitekit-gobeaver=C:/path/to/ViteKit"
go mod edit -replace "github.com/michael-amedaz/vitekit-gobeaver/adapter/gin=C:/path/to/ViteKit/adapter/gin"
go get github.com/michael-amedaz/vitekit-gobeaver
go get github.com/michael-amedaz/vitekit-gobeaver/adapter/gin
```

The adapter is a separate Go module, which is why it has its own dependency and optional replacement.

### Scaffold the frontend

From **myapp**:

```sh
vitekit init --dir ./frontend --entry src/main.js
npm --prefix ./frontend install
mkdir templates
```

Scaffolding creates `package.json`, `vite.config.js`, and the entry source file. Existing files are skipped, so check their contents when applying this to an existing frontend. The scaffold supplies vanilla JavaScript; choosing a `.jsx` filename alone does not install React or its Vite plugin.

Your application will have this structure:

```text
myapp/
    go.mod
    go.sum
    main.go
    templates/
        index.html
    frontend/
        package.json
        package-lock.json
        vite.config.js
        src/
            main.js
        .vite/
            hot                 # Created during development
        dist/                   # Created by the production build
            .vite/
                manifest.json
            assets/
                main-<hash>.js
```

## 5. Check the Vite configuration

The scaffold writes this setup in `frontend/vite.config.js`:

```js
import { defineConfig } from 'vite'

export default defineConfig({
  build: {
    manifest: true,
    outDir: 'dist',
    rollupOptions: {
      input: 'src/main.js',
    },
  },
  server: {
    cors: true,
  },
})
```

| Setting | Purpose |
| --- | --- |
| `manifest: true` | Generate the source-to-built-file mapping used by Go. |
| `outDir: 'dist'` | Put build output in `frontend/dist`. |
| `input: 'src/main.js'` | Build the same entry that the Go template requests. |
| `cors: true` | Allow the Go-served page to fetch development modules from Vite's origin. |

The frontend package needs `dev` and `build` scripts running `vite` and `vite build`. The scaffold creates both.

For an existing frontend, preserve its framework plugins and merge these build settings into its configuration. The Go template owns the HTML document; this setup builds a JavaScript entry rather than relying on Vite to serve `index.html`.

## 6. Create the Go backend

Save this complete program as **myapp/main.go**:

```go
package main

import (
    "html/template"
    "log"
    "net/http"
    "os"

    "github.com/gin-gonic/gin"
    vitekit "github.com/michael-amedaz/vitekit-gobeaver"
    vitegin "github.com/michael-amedaz/vitekit-gobeaver/adapter/gin"
)

func main() {
    options := []vitekit.Option{
        vitekit.WithRootDir("frontend"),
        vitekit.WithEntry("src/main.js"),
        vitekit.WithManifestPath("dist/.vite/manifest.json"),
        vitekit.WithOutputDir("dist"),
    }
    if os.Getenv("APP_ENV") == "development" {
        options = append(options, vitekit.WithDevMode())
    }

    if err := vitekit.Init(os.DirFS("frontend"), options...); err != nil {
        log.Fatal(err)
    }
    engine := vitekit.Service()

    router := gin.Default()
    router.Use(vitegin.Middleware(engine))

    tmpl := template.Must(template.New("index").
        Funcs(engine.FuncMap(vitekit.RenderOptions{})).
        ParseFiles("templates/index.html"))
    router.SetHTMLTemplate(tmpl)

    router.GET("/", func(c *gin.Context) {
        c.HTML(http.StatusOK, "index.html", gin.H{"title": "My ViteKit app"})
    })

    assets, err := engine.AssetHandler()
    if err != nil {
        log.Fatal(err)
    }
    router.Any("/assets/*filepath", gin.WrapH(assets))

    log.Fatal(router.Run("127.0.0.1:8080"))
}
```

The explicit manifest path also tells the file watcher which manifest location to monitor. Both that path and `OutputDir` are relative to `os.DirFS("frontend")`.

`APP_ENV` is an application-level switch introduced by this example, not a built-in ViteKit setting. It permits startup before the first build during development. Without it, a missing manifest and missing hot marker cause startup to fail.

The asset route deliberately retains `/assets/`: `/assets/main-<hash>.js` maps to `frontend/dist/assets/main-<hash>.js`. Do not strip `/assets/` with this layout.

This example binds to localhost. Change the listener address when your deployment requires access from outside the machine.

## 7. Create the HTML template and frontend entry

Save as **myapp/templates/index.html**:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ .title }}</title>
  {{ vite "src/main.js" }}
</head>
<body>
  <h1>{{ .title }}</h1>
  <div id="app"></div>
</body>
</html>
```

The scaffold's **frontend/src/main.js** contains:

```js
const app = document.querySelector('#app')
if (app) {
  app.textContent = 'Vite and Go are connected.'
}
```

The `vite` helper supplies development or production tags automatically. Do not hardcode hashed filenames into the template.

From **myapp**, resolve the Go dependencies:

```sh
go mod tidy
```

## 8. Start development

From **myapp**, in PowerShell:

```powershell
$env:APP_ENV = "development"
vitekit dev --dir ./frontend --entry src/main.js --backend "go run ."
```

On a POSIX shell:

```sh
APP_ENV=development vitekit dev --dir ./frontend --entry src/main.js --backend "go run ."
```

Open **http://localhost:8080** after both processes are ready. You should see the page title and “Vite and Go are connected.” Edit the frontend entry to confirm updates reach the browser.

The CLI writes `frontend/.vite/hot`; no hot-file plugin is required. Running plain `npm run dev` separately does not write that marker with the scaffolded configuration, so use the orchestrated command above.

In browser developer tools, development script URLs should point to Vite, for example `http://localhost:5173/@vite/client` and `/src/main.js` on that origin. The actual Vite port may differ.

To preview only the frontend without your own backend:

```sh
vitekit dev --dir ./frontend --entry src/main.js
```

Use the preview URL printed by the CLI. `--port` configures this built-in preview server; it does not change your custom backend's listener.

## 9. Build and run in production mode

Stop development with Ctrl+C. From **myapp**, in PowerShell:

```powershell
$env:APP_ENV = "production"
vitekit build --dir ./frontend
go run .
```

On a POSIX shell:

```sh
vitekit build --dir ./frontend
APP_ENV=production go run .
```

`vitekit build` runs the frontend build command. It does not build the Go binary.

Verify:

- `frontend/dist/.vite/manifest.json` exists.
- `frontend/.vite/hot` is absent after the development command stops.
- The page loads from http://localhost:8080.
- Browser script requests use `/assets/<built-filename>.js` on the Go origin.

Production requests follow two steps:

```text
GET /                         -> template reads loaded manifest -> HTML with asset URL
GET /assets/main-<hash>.js     -> AssetHandler reads built file -> JavaScript bytes
```

`APP_ENV=production` does not override a hot marker: the marker still controls tag rendering. If a crashed session left one behind, stop the old development processes and remove only the stale `frontend/.vite/hot` file before starting production.

### Deploy files alongside the binary

Build a Windows executable from **myapp**:

```sh
go build -o myapp.exe .
```

Deploy these together:

```text
deployment/
    myapp.exe
    templates/index.html
    frontend/dist/
```

Run the executable with `deployment` as its working directory. Do not deploy the development hot marker. Node.js is needed to produce the frontend build, but this Go runtime setup serves the already-built files without Node.js.

On other platforms, build with `go build -o myapp .` and run the resulting platform-specific binary.

### Optional: embed the frontend and template

For a production binary that carries its files, add the `embed` import and these package-level declarations to `main.go`:

```go
//go:embed all:frontend/dist
var builtAssets embed.FS

//go:embed templates/index.html
var pageTemplates embed.FS
```

Replace the disk-based options and initialization block with:

```go
if err := vitekit.Init(builtAssets,
    vitekit.WithManifestPath("frontend/dist/.vite/manifest.json"),
    vitekit.WithOutputDir("frontend/dist"),
    vitekit.WithEntry("src/main.js"),
); err != nil {
    log.Fatal(err)
}
```

Replace `ParseFiles("templates/index.html")` with `ParseFS(pageTemplates, "templates/index.html")`, and remove the now-unused `os` import if nothing else uses it.

Build the frontend **before** compiling Go. The `all:` prefix includes the hidden `.vite` manifest directory. This variant serves the files captured at compile time; keep the disk-based setup for frontend development.

## 10. React and other frameworks

The simplest React starting point is the repository's [Gin example](example/gin/main.go), [frontend configuration](example/frontend/vite.config.js), and [frontend package](example/frontend/package.json).

For React integration, configure React and its Vite plugin, use the actual `.jsx` or `.tsx` entry in both build input and template, and put the refresh preamble before the entry tags:

```gotemplate
{{ viteReactPreamble }}
{{ vite "src/main.jsx" }}
```

Use a matching mount element such as `<div id="root"></div>` for the React entry. The preamble is empty in production.

For other frameworks, keep their Vite plugins and configure the matching entry. ViteKit consumes the manifest and output files; the frontend compiler remains Vite's responsibility.

## 11. Common problems

| Symptom | What to check |
| --- | --- |
| `vitekit` command not found | Install the CLI and add Go's executable directory to `PATH`. |
| Initial manifest load fails | Build the frontend, or enable the manual's development option before launching the dev loop. Check the working directory. |
| `entry ... not found in manifest` | Match the template entry to Vite's build input and manifest key, such as `src/main.js`. Rebuild after changing it. |
| Page loads but built JavaScript returns 404 | Check the asset route, output directory, and deployed files. Keep `/assets/` in the path with this layout. |
| Browser still requests localhost Vite URLs in production | Check for a stale `frontend/.vite/hot` file. |
| Development renders from the manifest | Start through `vitekit dev` and check the hot marker under the filesystem root used by Go. |
| First development request reports no manifest | Wait for Vite readiness and refresh; backend startup can precede hot-marker publication. |
| Development scripts fail with CORS errors | Check Vite's `server.cors` setting and the requested Vite URL. |
| React reports a missing refresh preamble | Add `viteReactPreamble` before the React entry tags. |
| Go edits do not take effect | Restart the backend; the orchestrator does not itself watch and rebuild Go source. |
| `/favicon.ico` is missing | This example mounts only `/assets/`; add a route for root-level public files if your app needs them. |

## 12. Files to keep out of version control

For the disk-based example, add these entries to your application's `.gitignore`:

```gitignore
/frontend/node_modules/
/frontend/dist/
/frontend/.vite/hot
/myapp.exe
```

Commit source files, templates, Go module files, and the frontend lockfile. Regenerate `dist` during the release build, including before compilation when using `go:embed`.
