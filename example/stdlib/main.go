// Command stdlib is the smallest useful vitekit integration: net/http, one
// entry, no framework and no template files.
//
//	cd example && go run ./stdlib
//
// It works in both modes without a code change. With Vite running it serves
// the dev server's modules; with only a build present it serves dist/.
package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/michael-amedaz/vitekit-gobeaver"
)

// The Go server owns the document, so the page is a Go template and Vite never
// sees an index.html at all.
var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .Title }}</title>
{{ .Tags }}
</head>
<body>
<div id="app"></div>
</body>
</html>`))

func main() {
	// os.DirFS("frontend") is the build output's root. WithRootDir tells the
	// watcher where that is on disk so it can see .vite/hot appear; WithDevMode
	// lets the server boot before any production build exists.
	if err := vitekit.Init(
		os.DirFS("frontend"),
		vitekit.WithRootDir("frontend"),
		vitekit.WithDevMode(),
	); err != nil {
		log.Fatal(err)
	}
	defer vitekit.Reset()
	engine := vitekit.Service()

	mux := http.NewServeMux()

	// The asset handler is only reached in production; in development the
	// browser loads modules straight from Vite. Mounted with no path stripping,
	// because the default assets prefix reproduces Vite's own layout.
	assets, err := engine.AssetHandler()
	if err != nil {
		log.Print(err)
		return
	}
	mux.Handle("/assets/", assets)

	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		tags, err := engine.Tags("src/app.ts")
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(writer, map[string]any{
			"Title": "vitekit — net/http",
			// Tags is already escaped markup, so it is passed as template.HTML.
			"Tags": template.HTML(tags),
		})
	})

	server := &http.Server{
		Addr:              addr(),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	log.Printf("listening on http://localhost:%d", listenPort())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
	}
}

// listenPort parses PORT rather than pasting it into the address, so a
// malformed value is rejected here instead of becoming a nonsense listen
// address — and nothing from the environment reaches the log verbatim.
func listenPort() int {
	value := os.Getenv("PORT")
	if value == "" {
		return 8080
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 65535 {
		log.Print("ignoring invalid PORT")
		return 8080
	}
	return parsed
}

func addr() string {
	return fmt.Sprintf(":%d", listenPort())
}
