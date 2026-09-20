package upload

import (
	"context"
	"errors"
	"fdfz-filestore/internal/service/filemeta"
	"fdfz-filestore/internal/service/storage"
	"fdfz-filestore/internal/util/file"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"fdfz-filestore/internal/config"
	"fdfz-filestore/internal/errs"
	"fdfz-filestore/internal/middleware"
	"fdfz-filestore/internal/model"
	"fdfz-filestore/internal/service/session"
	"fdfz-filestore/internal/util/crypto"

	"github.com/gin-gonic/gin"
	"github.com/guregu/null/v6"
)

func InitRoutes(api *gin.RouterGroup, internal *gin.RouterGroup) {
	api.POST("/upload/:token", uploadHandler)
	internal.POST("/upload/init", middleware.RequireAPIKey(), createUploadHandler)
}

type createUploadReq struct {
	Slug        string `json:"slug,omitempty"`
	SHA512      string `json:"sha512,omitempty"`
	FileName    string `json:"original_name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	IsPrivate   bool   `json:"is_private"`
	MaxSize     int64  `json:"max_size"`
	Upsert      bool   `json:"upsert"`
}

func createUploadHandler(c *gin.Context) {
	var req createUploadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.BadRequestError().AppendDetails("请求解析失败", "上传会话创建失败").Respond(c)
		return
	}
	if !strings.HasPrefix(req.Slug, "/") {
		errs.BadRequestError().AppendDetails("无效的Slug", "上传会话创建失败").Respond(c)
		return
	}
	if len(req.SHA512) != 128 {
		errs.BadRequestError().AppendDetails("缺少 SHA512", "上传会话创建失败").Respond(c)
		return
	}

	if exists, err := filemeta.CheckSHA512Exists(c.Request.Context(), req.SHA512); err != nil {
		err.AppendDetails("上传会话创建失败").Respond(c)
		return
	} else if exists {
		oldSHA512, err := filemeta.UpsertFileMetaBySlug(c.Request.Context(), &model.FileMetadata{
			Slug:        null.StringFrom(req.Slug),
			SHA512:      req.SHA512,
			Filename:    req.FileName,
			ContentType: req.ContentType,
			IsPrivate:   req.IsPrivate,
		})
		if err != nil {
			err.AppendDetails("文件上传失败").Respond(c)
			return
		}
		if oldSHA512 != "" && oldSHA512 != req.SHA512 {
			fileutils.RemoveOrphanBlob(oldSHA512)
		}
		c.JSON(http.StatusCreated, gin.H{})
		return
	}

	if !req.Upsert {
		if exists, err := filemeta.CheckSlugExists(c.Request.Context(), req.Slug); err != nil {
			err.AppendDetails("上传会话创建失败").Respond(c)
			return
		} else if exists {
			errs.ConflictError().AppendDetails("Slug 已存在", "上传会话创建失败").Respond(c)
			return
		}
	}

	token, err := crypto.GenerateCode(32)
	if err != nil {
		errs.CreateAndLogInternalError(err, "Failed to generate upload token").AppendDetails("令牌生成失败", "上传会话创建失败").Respond(c)
		return
	}

	if err := session.CreateUploadSession(c.Request.Context(), &model.UploadSession{
		Token:       token,
		Slug:        req.Slug,
		SHA512:      req.SHA512,
		Filename:    req.FileName,
		ContentType: req.ContentType,
		IsPrivate:   req.IsPrivate,
		MaxSize:     req.MaxSize,
		Upsert:      req.Upsert,
		CreatedAt:   time.Now(),
	}); err != nil {
		err.AppendDetails("上传会话创建失败").Respond(c)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"ttl":   config.C.UploadTTL,
	})
}

func uploadHandler(c *gin.Context) {
	token := c.Param("token")
	if len(token) != 32 {
		errs.BadRequestError().AppendDetails("无效的上传令牌", "文件上传失败").Respond(c)
		return
	}

	s, err := session.GetUploadSessionByToken(c.Request.Context(), token)
	if err != nil {
		err.AppendDetails("文件上传失败").Respond(c)
		return
	} else if s == nil {
		errs.NotFoundError().AppendDetails("上传会话不存在", "文件上传失败").Respond(c)
		return
	}
	defer session.DeleteUploadSessionByToken(context.Background(), token)

	if s.MaxSize > 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.MaxSize)
	}

	fHeader, fErr := c.FormFile("file")
	if fErr != nil {
		var mbe *http.MaxBytesError
		if errors.As(fErr, &mbe) {
			errs.RequestTooLargeError().AppendDetails("文件大小超出限制").Respond(c)
			return
		}
		errs.BadRequestError().AppendDetails("文件内容解析失败", "文件上传失败").Respond(c)
		return
	}

	src, fErr := fHeader.Open()
	if fErr != nil {
		errs.CreateAndLogInternalError(fErr, "Failed to open uploaded file").AppendDetails("文件打开失败", "文件上传失败").Respond(c)
		return
	}
	defer func(src multipart.File) {
		if err := src.Close(); err != nil {
			slog.Warn("Failed to close file", "error", err)
		}
	}(src)

	tw, fErr := fileutils.NewTempWriter(storage.FilePath(s.SHA512), s.SHA512)
	if fErr != nil {
		errs.CreateAndLogInternalError(fErr, "Failed to create temp writer").AppendDetails("创建临时写入器失败", "文件上传失败").Respond(c)
		return
	}
	defer tw.Abort()

	_, fErr = tw.WriteFrom(src)
	if fErr != nil {
		var mbe *http.MaxBytesError
		if errors.As(fErr, &mbe) {
			errs.RequestTooLargeError().AppendDetails("文件大小超出限制").Respond(c)
			return
		}
		errs.CreateAndLogInternalError(fErr, "Failed to write to temp file").AppendDetails("文件写入失败", "文件上传失败").Respond(c)
		return
	}

	if commitErr := tw.Commit(); commitErr != nil {
		if errors.Is(commitErr, fileutils.ErrSHA512Mismatch) {
			errs.BadRequestError().AppendDetails("SHA512 校验失败", "文件上传失败").Respond(c)
			return
		}
		errs.CreateAndLogInternalError(commitErr, "Failed to commit temp file").AppendDetails("文件提交失败", "文件上传失败").Respond(c)
		return
	}

	meta := &model.FileMetadata{
		Slug:        null.StringFrom(s.Slug),
		SHA512:      s.SHA512,
		Filename:    s.Filename,
		ContentType: s.ContentType,
		IsPrivate:   s.IsPrivate,
	}

	var metaErr *errs.Error
	orphanSHA512 := ""
	if s.Upsert {
		var oldSHA512 string
		oldSHA512, metaErr = filemeta.UpsertFileMetaBySlug(c.Request.Context(), meta)
		if metaErr == nil && oldSHA512 != s.SHA512 {
			orphanSHA512 = oldSHA512
		}
	} else {
		metaErr = filemeta.CreateFileMeta(c.Request.Context(), meta)
	}
	if metaErr != nil {
		fileutils.RemoveOrphanBlob(s.SHA512)
		metaErr.AppendDetails("文件上传失败").Respond(c)
		return
	}
	if orphanSHA512 != "" {
		fileutils.RemoveOrphanBlob(orphanSHA512)
	}

	c.JSON(http.StatusOK, gin.H{})
}
