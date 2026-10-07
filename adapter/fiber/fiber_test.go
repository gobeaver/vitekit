package fiber

import (
	"html/template"
	"io"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/michael-amedaz/vitekit-gobeaver"

	fiberpkg "github.com/gofiber/fiber/v2"
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

func TestMiddlewareStoresEngineInLocals(t *testing.T) {
	engine := newTestEngine(t)

	app := fiberpkg.New()
	app.Use(Middleware(engine))

	var got interface{}
	app.Get("/", func(c *fiberpkg.Ctx) error {
		got = c.Locals("vite")
		return c.SendStatus(http.StatusOK)
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if got != engine {
		t.Fatal("expected Locals(\"vite\") to return the engine")
	}
}

func TestFuncMapReturnsWorkingFunction(t *testing.T) {
	engine := newTestEngine(t)
	fm := FuncMap(engine, vitekit.RenderOptions{})

	viteFn, ok := fm["vite"]
	if !ok {
		t.Fatal("FuncMap did not contain a \"vite\" key")
	}

	// The template function is variadic so a single call can render several
	// entries and deduplicate whatever they share.
	fn, ok := viteFn.(func(...string) (template.HTML, error))
	if !ok {
		t.Fatalf("unexpected vite func type: %T", viteFn)
	}

	result, err := fn("src/main.js")
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("expected non-empty HTML from vite template function")
	}
}
