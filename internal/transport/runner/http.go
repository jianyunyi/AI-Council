package runner

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func NewRouter() *gin.Engine {
	return NewRouterWithShutdown("", nil)
}

// NewRouterWithShutdown adds a local authenticated shutdown endpoint when a
// session token and callback are configured.
func NewRouterWithShutdown(token string, shutdown func(context.Context)) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	if token != "" && shutdown != nil {
		router.POST("/shutdown", func(c *gin.Context) {
			raw := strings.TrimSpace(c.GetHeader("Authorization"))
			if len(raw) < 7 || !strings.EqualFold(raw[:7], "bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(raw[7:])), []byte(token)) != 1 {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "valid bearer token required"})
				return
			}
			c.JSON(http.StatusAccepted, gin.H{"status": "shutting_down"})
			shutdown(c.Request.Context())
		})
	}
	return router
}
