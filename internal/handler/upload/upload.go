package upload

import (
	"errors"
	"ffhub-filestore/internal/cfg"
	"ffhub-filestore/internal/errs"
	"ffhub-filestore/internal/middleware"
	"ffhub-filestore/internal/model"
	"ffhub-filestore/internal/service/filemeta"
	"ffhub-filestore/internal/service/storage"
	fileutils "ffhub-filestore/internal/util/file"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/guregu/null/v6"
)

func InitRoutes(internal *gin.RouterGroup) {
	internal.POST("/upload/store", middleware.RequireAPIKey(), storeFileHandler)
}

type storeFileReq struct {
	Slug         string      `json:"slug,omitempty"`
	FilePath     string      `json:"file_path,omitempty"`
	FileName     string      `json:"file_name,omitempty"`
	ContentType  null.String `json:"content_type"`
	SHA512       null.String `json:"sha512"`
	IsPrivate    bool        `json:"is_private"`
	ShouldUpsert bool        `json:"should_upsert"`
	ShouldCopy   bool        `json:"should_copy"`
	UserID       null.Int64  `json:"user_id"`
	AppID        null.Int64  `json:"app_id"`
	BanComment   null.String `json:"ban_comment"`
}

func storeFileHandler(c *gin.Context) {
	var req storeFileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.BadRequestError().AppendDetails("请求解析失败", "文件上传失败").Respond(c)
		return
	}
	if !strings.HasPrefix(req.Slug, "/") {
		errs.BadRequestError().AppendDetails("无效的Slug", "文件上传失败").Respond(c)
		return
	}

	src, err := fileutils.SafeJoin(cfg.C.SharedDir, path.Clean(req.FilePath))
	if err != nil {
		errs.BadRequestError().AppendDetails("无效的文件路径", "文件上传失败").Respond(c)
		return
	}

	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			errs.NotFoundError().AppendDetails("无法找到文件", "文件上传失败").Respond(c)
			return
		}
		errs.CreateAndLogInternalError(err, "Failed to stat source file").
			AppendDetails("文件读取失败", "文件上传失败").Respond(c)
		return
	}

	var sha512 string
	if !req.SHA512.Valid {
		sha512, err = fileutils.ComputeSHA512(src)
		if err != nil {
			errs.CreateAndLogInternalError(err, "Failed to compute SHA512").AppendDetails("SHA512 计算失败", "文件上传失败").Respond(c)
			return
		}
	} else {
		sha512 = req.SHA512.String
	}

	if !req.ContentType.Valid {
		contentType, err := fileutils.DetectContentType(src)
		if err != nil {
			errs.CreateAndLogInternalError(err, "Failed to detect content type").AppendDetails("内容类型检测失败", "文件上传失败").Respond(c)
			return
		}
		req.ContentType = null.StringFrom(contentType)
	}

	exists, sErr := filemeta.CheckSHA512Exists(c.Request.Context(), sha512)
	if sErr != nil {
		sErr.AppendDetails("文件上传失败").Respond(c)
		return
	}

	if !exists {
		dst := storage.FilePath(sha512)
		if err := os.MkdirAll(path.Dir(dst), 0o755); err != nil {
			errs.CreateAndLogInternalError(err, "Failed to create directory").AppendDetails("目录创建失败", "文件上传失败").Respond(c)
			return
		}
		if req.ShouldCopy {
			if err := fileutils.CopyFile(src, dst); err != nil {
				errs.CreateAndLogInternalError(err, "Failed to copy file").AppendDetails("文件复制失败", "文件上传失败").Respond(c)
				return
			}
		} else {
			if err := fileutils.MoveFile(src, dst); err != nil {
				errs.CreateAndLogInternalError(err, "Failed to move file").AppendDetails("文件移动失败", "文件上传失败").Respond(c)
				return
			}
		}
	}

	var creationErr *errs.Error
	meta := &model.FileMetadata{
		Slug:        null.StringFrom(req.Slug),
		SHA512:      sha512,
		Filename:    req.FileName,
		ContentType: req.ContentType.String,
		IsPrivate:   req.IsPrivate,
		UserID:      req.UserID,
		AppID:       req.AppID,
		BanComment:  req.BanComment,
	}
	if req.ShouldUpsert {
		var oldSHA512 string
		oldSHA512, creationErr = filemeta.UpsertFileMetaBySlug(c.Request.Context(), meta)
		fileutils.RemoveOrphanBlob(oldSHA512)
	} else {
		creationErr = filemeta.CreateFileMeta(c.Request.Context(), meta)
	}
	if creationErr != nil {
		creationErr.AppendDetails("文件上传失败").Respond(c)
		fileutils.RemoveOrphanBlob(sha512)
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}
