// Command embed is the production deployment story: the whole frontend is
// compiled into the binary, so shipping is one file with no asset directory
// beside it.
//
//	cd example && go run ./embed
//
// Note what is *not* here. No manifest path, no output directory, no root
// directory. A go:embed pattern keeps its own prefix — the manifest is really
// at dist/.vite/manifest.json inside the embedded tree, which is not where a
// filesystem-backed build would put it — and vitekit locates it and infers the
// output directory from it.
package main

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/michael-amedaz/vitekit-gobeaver"
)

// The committed dist/ here is real (subset) Vite output, kept small and in
// version control so this example always compiles. A real project would build
// it as part of the release and gitignore it.
//
// "all:" matters: without it go:embed skips .vite, and the manifest with it.
//
//go:embed all:dist
var assets embed.FS

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>vitekit — single binary</title>
{{ .Tags }}
</head>
<body>
<div id="app"></div>
</body>
</html>`))

func main() {
	// No options: the embedded layout is discovered.
	if err := vitekit.Init(assets); err != nil {
		log.Fatal(err)
	}
	defer vitekit.Reset()
	engine := vitekit.Service()

	mux := http.NewServeMux()

	assetHandler, err := engine.AssetHandler()
	if err != nil {
		log.Print(err)
		return
	}
	// Served straight out of the binary, with immutable cache headers on every
	// content-hashed file.
	mux.Handle("/assets/", assetHandler)

	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		tags, err := engine.TagsFor([]string{"src/theme.css", "src/app.ts"}, vitekit.RenderOptions{})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(writer, map[string]any{"Tags": template.HTML(tags)})
	})

	// PORT is parsed rather than pasted into the address, so a malformed value
	// is rejected here instead of becoming a nonsense listen address — and
	// nothing from the environment reaches the log verbatim.
	port := 8080
	if value := os.Getenv("PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			log.Print("ignoring invalid PORT")
		} else {
			port = parsed
		}
	}
	// Timeouts rather than http.ListenAndServe: a server with none will hold a
	// connection open for a client that never finishes its request.
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("single-binary server on http://localhost:%d", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
	}
}
