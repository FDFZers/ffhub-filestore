package middleware

import (
	"crypto/subtle"
	"fdfz-filestore/internal/config"
	"fdfz-filestore/internal/errs"

	"github.com/gin-gonic/gin"
)

func RequireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(key), []byte(config.C.APIKey)) != 1 {
			errs.UnauthorizedError().AppendDetails("无效的 API Key").Respond(c)
			c.Abort()
			return
		}
		c.Next()
	}
}
