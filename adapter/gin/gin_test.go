package gin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/michael-amedaz/vitekit-gobeaver"

	ginpkg "github.com/gin-gonic/gin"
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

func TestMiddlewareSetsViteKey(t *testing.T) {
	ginpkg.SetMode(ginpkg.TestMode)
	engine := newTestEngine(t)

	var got interface{}
	router := ginpkg.New()
	router.Use(Middleware(engine))
	router.GET("/", func(c *ginpkg.Context) {
		got = c.MustGet("vite")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(w, req)

	if got != engine {
		t.Fatal("expected MustGet(\"vite\") to return the engine")
	}
}
