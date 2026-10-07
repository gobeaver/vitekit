package echo

import (
	"github.com/michael-amedaz/vitekit-gobeaver"

	"github.com/labstack/echo/v4"
)

// Context extends Echo's context with direct Vite tag rendering.
type Context struct {
	echo.Context
	engine *vitekit.Engine
}

// Vite renders the tags for a Vite entry.
func (context *Context) Vite(entry string, options vitekit.RenderOptions) (string, error) {
	return context.engine.TagsWithOptions(entry, options)
}

// Middleware exposes the extended context to Echo handlers.
func Middleware(engine *vitekit.Engine) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(context echo.Context) error {
			return next(&Context{Context: context, engine: engine})
		}
	}
}
