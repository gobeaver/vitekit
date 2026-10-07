// Package stdlib wires vitekit into net/http without pulling in a web
// framework. It is part of the core module and adds no dependencies.
package stdlib

import (
	"context"
	"net/http"

	"github.com/michael-amedaz/vitekit-gobeaver"
)

type contextKey struct{}

// Middleware makes the engine available through the request context.
func Middleware(engine *vitekit.Engine) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			request = request.WithContext(context.WithValue(request.Context(), contextKey{}, engine))
			next.ServeHTTP(writer, request)
		})
	}
}

// FromContext returns the engine installed by Middleware.
func FromContext(ctx context.Context) (*vitekit.Engine, bool) {
	engine, ok := ctx.Value(contextKey{}).(*vitekit.Engine)
	return engine, ok
}
