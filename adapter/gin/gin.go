package gin

import (
	"github.com/michael-amedaz/vitekit-gobeaver"

	"github.com/gin-gonic/gin"
)

// Middleware stores the engine under the conventional "vite" key.
func Middleware(engine *vitekit.Engine) gin.HandlerFunc {
	return func(context *gin.Context) {
		context.Set("vite", engine)
		context.Next()
	}
}
