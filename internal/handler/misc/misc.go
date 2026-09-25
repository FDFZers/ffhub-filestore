package misc

import (
	"ffhub-filestore/internal/cfg"
	"net/http"

	"github.com/gin-gonic/gin"
)

func InitRoutes(api, internal *gin.RouterGroup) {
	api.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "欢迎来到 FDFZHub File Store！",
		})
	})

	internal.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "你来这里做什么？！",
		})
	})

	api.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"commit":     cfg.Commit,
			"build_time": cfg.BuildTime,
			"is_prod":    cfg.IsProd,
		})
	})
}
