// Command multientry shows the parts of vitekit that only matter once a page
// is more than one script: several entries in one render, a CSP nonce, and
// preload hints sent as an HTTP header.
//
//	cd example && go run ./multientry
//
// The two TypeScript entries import the same module, so Vite splits it into a
// shared chunk. Rendering them together emits that chunk once — concatenating
// two separate Tags calls would emit it twice and make the browser fetch the
// same module under two hints.
package main

import (
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

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>vitekit — multiple entries</title>
{{ .Tags }}
</head>
<body>
<div id="app"></div>
<div id="admin"></div>
</body>
</html>`))

// A standalone stylesheet alongside two scripts. vitekit renders the CSS entry
// as <link rel="stylesheet">, not as a module script.
var entries = []string{"src/theme.css", "src/app.ts", "src/admin.ts"}

func main() {
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
	if assets, err := engine.AssetHandler(); err == nil {
		mux.Handle("/assets/", assets)
	}

	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}

		// A fresh nonce per response. The same value goes on every generated
		// tag and into the policy header.
		nonce, err := vitekit.NewNonce()
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}

		tags, err := engine.TagsFor(entries, vitekit.RenderOptions{Nonce: nonce})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}

		// Preload hints as a header let the browser start fetching before it
		// has parsed any of the document. Empty in development, where Vite
		// discovers modules itself.
		if link, err := engine.PreloadHeader(entries...); err == nil && link != "" {
			writer.Header().Set("Link", link)
		}

		writer.Header().Set("Content-Security-Policy", engine.ContentSecurityPolicy(nonce))
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
	log.Printf("listening on http://localhost:%d", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
	}
}
