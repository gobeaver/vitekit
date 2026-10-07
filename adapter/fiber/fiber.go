package fiber

import (
	"html/template"

	"github.com/michael-amedaz/vitekit-gobeaver"

	"github.com/gofiber/fiber/v2"
)

// Middleware stores the engine in Fiber's locals for handlers and views.
func Middleware(engine *vitekit.Engine) fiber.Handler {
	return func(context *fiber.Ctx) error {
		context.Locals("vite", engine)
		return context.Next()
	}
}

// FuncMap returns functions suitable for Fiber view engines that support
// template.FuncMap registration.
func FuncMap(engine *vitekit.Engine, options vitekit.RenderOptions) template.FuncMap {
	return engine.FuncMap(options)
}
