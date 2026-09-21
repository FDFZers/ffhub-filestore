package download

import (
	"net/http"
	"time"

	"ffhub-filestore/internal/config"
	"ffhub-filestore/internal/errs"
	"ffhub-filestore/internal/middleware"
	"ffhub-filestore/internal/model"
	"ffhub-filestore/internal/service/session"
	"ffhub-filestore/internal/util/crypto"

	"github.com/gin-gonic/gin"
)

func InitRoutes(internal *gin.RouterGroup) {
	internal.POST("/download/init", middleware.RequireAPIKey(), createDownloadHandler)
}

type createDownloadReq struct {
	Slug string `json:"slug,omitempty"`
}

func createDownloadHandler(c *gin.Context) {
	var req createDownloadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.BadRequestError().AppendDetails("请求解析失败", "下载会话创建失败").Respond(c)
		return
	}

	token, err := crypto.GenerateCode(32)
	if err != nil {
		errs.CreateAndLogInternalError(err, "Failed to generate download token").AppendDetails("令牌生成失败", "下载会话创建失败").Respond(c)
		return
	}

	if err := session.CreateDownloadSession(c.Request.Context(), &model.DownloadSession{
		Token:     token,
		Slug:      req.Slug,
		CreatedAt: time.Now(),
	}); err != nil {
		err.AppendDetails("下载会话创建失败").Respond(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"ttl":   config.C.DownloadTTL,
	})
}
