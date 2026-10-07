// Command gin is the fullest of the examples: a React frontend on a Gin
// backend, rendered through html/template with vitekit's FuncMap, plus a small
// JSON API so the page has a backend to talk to.
//
//	cd example && go run ./gin
//
// It is also the one that exercises React Fast Refresh through a Go-served
// document, via {{ viteReactPreamble }} in templates/index.html.
package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/michael-amedaz/vitekit-gobeaver"
	vitegin "github.com/michael-amedaz/vitekit-gobeaver/adapter/gin"

	"github.com/gin-gonic/gin"
)

type activity struct {
	ID        int    `json:"id"`
	Service   string `json:"service"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	Duration  string `json:"duration"`
	Timestamp string `json:"timestamp"`
}

type consoleState struct {
	sync.RWMutex
	maintenance bool
	activities  []activity
}

func newState() *consoleState {
	now := time.Now()
	return &consoleState{activities: []activity{
		{1, "edge-router", "Health check", "healthy", "42ms", now.Add(-2 * time.Minute).Format(time.RFC3339)},
		{2, "asset-pipeline", "Manifest reload", "healthy", "18ms", now.Add(-8 * time.Minute).Format(time.RFC3339)},
		{3, "billing-api", "Rate limit probe", "warning", "231ms", now.Add(-14 * time.Minute).Format(time.RFC3339)},
		{4, "event-stream", "Connection restored", "healthy", "96ms", now.Add(-21 * time.Minute).Format(time.RFC3339)},
		{5, "search-index", "Snapshot persisted", "healthy", "1.2s", now.Add(-31 * time.Minute).Format(time.RFC3339)},
	}}
}

func main() {
	// The frontend lives beside this program rather than under it, so the
	// fs.FS is rooted at frontend/ and WithRootDir points the watcher there.
	fsys := os.DirFS("frontend")
	if err := vitekit.Init(fsys,
		vitekit.WithDevMode(),
		vitekit.WithEntry("src/main.jsx"),
		vitekit.WithRootDir("frontend"),
	); err != nil {
		log.Fatalf("failed to initialize vitekit: %v", err)
	}
	engine := vitekit.Service()
	state := newState()
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), vitegin.Middleware(engine))
	tmpl := template.Must(template.New("index").Funcs(engine.FuncMap(vitekit.RenderOptions{})).ParseFiles("templates/index.html"))
	router.SetHTMLTemplate(tmpl)

	router.GET("/", func(c *gin.Context) { c.HTML(http.StatusOK, "index.html", gin.H{"title": "Launch Console"}) })
	router.GET("/api/overview", func(c *gin.Context) {
		state.RLock()
		maintenance := state.maintenance
		state.RUnlock()
		mode := "production"
		if _, ok := engine.DevURL(); ok {
			mode = "development"
		}
		c.JSON(http.StatusOK, gin.H{"mode": mode, "maintenance": maintenance, "uptime": "14d 06h 22m", "requests": 12842, "successRate": 99.97, "deploy": "v2.8.4"})
	})
	router.GET("/api/activity", func(c *gin.Context) {
		state.RLock()
		items := append([]activity(nil), state.activities...)
		state.RUnlock()
		c.JSON(http.StatusOK, items)
	})
	router.POST("/api/maintenance", func(c *gin.Context) {
		state.Lock()
		state.maintenance = !state.maintenance
		maintenance := state.maintenance
		state.Unlock()
		c.JSON(http.StatusOK, gin.H{"maintenance": maintenance})
	})
	// No StripPrefix: the default assets prefix reproduces Vite's own layout,
	// so /assets/main-BnOOO20u.js maps to dist/assets/main-BnOOO20u.js.
	if assets, err := engine.AssetHandler(); err == nil {
		router.Any("/assets/*filepath", gin.WrapH(assets))
	}

	port := 8080
	if configuredPort, err := strconv.Atoi(os.Getenv("VITEKIT_PORT")); err == nil && configuredPort > 0 {
		port = configuredPort
	}
	fmt.Printf("Launch Console running at http://localhost:%d\n", port)
	if err := router.Run(fmt.Sprintf(":%d", port)); err != nil {
		log.Fatal(err)
	}
}
