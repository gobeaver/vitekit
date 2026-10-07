// Command echo shows the Echo adapter, which extends Echo's context with a
// Vite method so handlers can render tags without reaching for a global.
//
//	cd example && go run ./echo
package main

import (
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/michael-amedaz/vitekit-gobeaver"
	viteecho "github.com/michael-amedaz/vitekit-gobeaver/adapter/echo"
)

func main() {
	engine, cancel, err := vitekit.WithPrefix("EXAMPLE_VITE_").New(
		os.DirFS("frontend"),
		vitekit.WithRootDir("frontend"),
		vitekit.WithDevMode(),
	)
	if err != nil {
		log.Fatal(err)
	}
	// WithPrefix builds an engine that owns its own watcher rather than using
	// the package-level singleton, which is what an app with more than one
	// Vite build needs. cancel stops that watcher.
	defer cancel()

	router := echo.New()
	router.HideBanner = true
	router.Use(viteecho.Middleware(engine))

	router.GET("/", func(context echo.Context) error {
		// The middleware swapped in a context that knows about Vite.
		viteContext, ok := context.(*viteecho.Context)
		if !ok {
			return echo.NewHTTPError(http.StatusInternalServerError, "vite middleware is not installed")
		}
		nonce, err := vitekit.NewNonce()
		if err != nil {
			return err
		}
		tags, err := viteContext.Vite("src/app.ts", vitekit.RenderOptions{Nonce: nonce})
		if err != nil {
			return err
		}
		context.Response().Header().Set("Content-Security-Policy", engine.ContentSecurityPolicy(nonce))
		return context.HTML(http.StatusOK, `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>vitekit — Echo</title>`+tags+`</head>
<body><div id="app"></div></body></html>`)
	})

	if assets, err := engine.AssetHandler(); err == nil {
		router.GET("/assets/*", echo.WrapHandler(assets))
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := router.Start(":" + port); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
	}
}
