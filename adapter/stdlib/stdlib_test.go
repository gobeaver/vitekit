package stdlib

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/michael-amedaz/vitekit-gobeaver"
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

func TestMiddlewareInjectsEngine(t *testing.T) {
	engine := newTestEngine(t)

	var got *vitekit.Engine
	var ok bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = FromContext(r.Context())
	})

	handler := Middleware(engine)(inner)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !ok {
		t.Fatal("FromContext returned false after Middleware")
	}
	if got != engine {
		t.Fatal("FromContext returned a different engine")
	}
}

func TestFromContextWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, ok := FromContext(req.Context())
	if ok {
		t.Fatal("FromContext should return false when middleware was not used")
	}
}
