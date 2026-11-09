package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmeta"
	"github.com/gin-gonic/gin"
)

// RequestIngress captures the TCP peer independently of existing client-IP
// compatibility settings. Never retain gin.Context in asynchronous log tasks.
func RequestIngress(policy func() *requestmeta.Policy) gin.HandlerFunc {
	return func(c *gin.Context) {
		value := policy().Capture(c.Request)
		c.Request = c.Request.WithContext(requestmeta.WithIngress(c.Request.Context(), value))
		c.Next()
	}
}
