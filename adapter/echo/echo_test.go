package echo

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/michael-amedaz/vitekit-gobeaver"

	echopkg "github.com/labstack/echo/v4"
)

func newTestEngine(t *testing.T) *vitekit.Engine {
	t.Helper()
	fsys := fstest.MapFS{
		".vite/manifest.json": {Data: []byte(`{
			"src/main.js":{"file":"assets/main.js","isEntry":true}
		}`)},
	}
	if err := vitekit.Init(fsys); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vitekit.Reset)
	return vitekit.Service()
}

func TestMiddlewareWrapsContext(t *testing.T) {
	engine := newTestEngine(t)

	e := echopkg.New()
	e.Use(Middleware(engine))

	var isCustom bool
	e.GET("/", func(c echopkg.Context) error {
		_, isCustom = c.(*Context)
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if !isCustom {
		t.Fatal("expected context to be wrapped in echo.Context adapter")
	}
}

func TestContextViteRendersTags(t *testing.T) {
	engine := newTestEngine(t)

	e := echopkg.New()
	e.Use(Middleware(engine))

	var tags string
	var renderErr error
	e.GET("/", func(c echopkg.Context) error {
		vc := c.(*Context)
		tags, renderErr = vc.Vite("src/main.js", vitekit.RenderOptions{})
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if renderErr != nil {
		t.Fatalf("Vite() returned error: %v", renderErr)
	}
	if tags == "" {
		t.Fatal("expected non-empty tags from Vite()")
	}
}
