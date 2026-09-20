package misc

import (
	"fdfz-filestore/internal/config"
	"net/http"

	"github.com/gin-gonic/gin"
)

func InitRoutes(api *gin.RouterGroup) {
	api.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "欢迎来到 FDFZ File Store，寻宝而来的收藏家！",
		})
	})

	api.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"version":    config.Version,
			"commit":     config.Commit,
			"build_time": config.BuildTime,
			"is_prod":    config.IsProd,
		})
	})
}
