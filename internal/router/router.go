package router

import (
	"fdfz-filestore/internal/handler/download"
	"fdfz-filestore/internal/handler/file"
	"fdfz-filestore/internal/handler/healthcheck"
	"fdfz-filestore/internal/handler/misc"
	"fdfz-filestore/internal/handler/upload"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func InitRouter(r *gin.Engine) {
	apiCors := cors.Default()
	internalCors := cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			if origin == "https://fdfz.top" {
				return true
			}
			return strings.HasPrefix(origin, "https://") && strings.HasSuffix(origin, ".fdfz.top")
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Api-Key"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})

	r.Use(func(c *gin.Context) {
		switch {
		case under(c.Request.URL.Path, "/api/internal"):
			internalCors(c)
		case under(c.Request.URL.Path, "/api/v1"):
			apiCors(c)
		}
	})

	v1 := r.Group("/api/v1")
	internal := r.Group("/api/internal")

	misc.InitRoutes(v1)
	upload.InitRoutes(v1, internal)
	download.InitRoutes(internal)
	file.InitRoutes(v1)
}

func InitHealthCheckRouter(r *gin.Engine) {
	healthcheck.RegisterHealthCheckRoutes(r)
}
