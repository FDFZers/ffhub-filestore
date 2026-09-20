package file

import (
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strings"

	"fdfz-filestore/internal/errs"
	"fdfz-filestore/internal/service/filemeta"
	"fdfz-filestore/internal/service/session"
	"fdfz-filestore/internal/service/storage"

	"github.com/gin-gonic/gin"
)

func InitRoutes(api *gin.RouterGroup) {
	api.GET("/files/*slug", func(c *gin.Context) {
		serveFile(c, c.Param("slug"))
	})
}

func bearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if strings.HasPrefix(auth, prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return ""
}

func serveFile(c *gin.Context, slug string) {
	meta, err := filemeta.GetFileMetaBySlug(c.Request.Context(), slug)
	if err != nil {
		err.AppendDetails("文件访问失败").Respond(c)
		return
	}

	if meta.IsPrivate {
		token := bearerToken(c)
		if token == "" {
			errs.UnauthorizedError().AppendDetails("无法访问私密文件", "文件访问失败").Respond(c)
			return
		}
		if len(token) != 32 {
			errs.UnauthorizedError().AppendDetails("无效的下载令牌", "文件访问失败").Respond(c)
			return
		}
		s, err := session.GetDownloadSessionByToken(c.Request.Context(), token)
		if err != nil {
			err.AppendDetails("文件访问失败").Respond(c)
			return
		}
		if s == nil || s.Slug != slug {
			errs.UnauthorizedError().AppendDetails("无效的下载令牌", "文件访问失败").Respond(c)
			return
		}
		session.DeleteDownloadSessionByToken(c.Request.Context(), token)
	}

	f, fErr := os.Open(storage.FilePath(meta.SHA512))
	if fErr != nil {
		if errors.Is(fErr, fs.ErrNotExist) {
			errs.NotFoundError().AppendDetails("文件不存在", "文件访问失败").Respond(c)
			return
		}
		errs.NotFoundError().AppendDetails("文件读取失败", "文件访问失败").Respond(c)
		return
	}
	defer func(f *os.File) {
		if err := f.Close(); err != nil {
			slog.Warn("Failed to close file", "error", err)
		}
	}(f)

	c.Header("Content-Type", meta.ContentType)

	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": meta.Filename}))

	http.ServeContent(c.Writer, c.Request, meta.Filename, meta.CreatedAt, f)
}
