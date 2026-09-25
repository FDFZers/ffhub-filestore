package filemeta

import (
	"context"
	"errors"
	"ffhub-filestore/internal/db"
	"ffhub-filestore/internal/errs"
	"ffhub-filestore/internal/model"
	"fmt"
	"time"

	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"
)

const (
	FileMetaCacheDuration = 5 * time.Minute
)

func FileMetaCacheKey(id int64) string {
	return fmt.Sprintf("filestore:filemeta:id:%d", id)
}

func FileMetaIDCacheKey(slug string) string {
	return fmt.Sprintf("filestore:filemeta:slug:%s", slug)
}

// ---------------------------------------------------------------------------
// 创建文件元数据
// ---------------------------------------------------------------------------

const createFileMetaSQL = `
INSERT INTO file_metas (slug, sha512, filename, content_type, is_private, user_id, app_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at
`

func CreateFileMeta(ctx context.Context, f *model.FileMetadata) *errs.Error {
	err := db.R.Pool.QueryRow(ctx, createFileMetaSQL,
		f.Slug, f.SHA512, f.Filename, f.ContentType, f.IsPrivate, f.UserID, f.AppID,
	).Scan(&f.ID, &f.CreatedAt)
	if err != nil {
		return errs.CreatePGError(err, "文件元数据", "Failed to create file meta").AppendDetails("文件元数据记录失败")
	}

	db.SetRedis(ctx, FileMetaCacheKey(f.ID), f, FileMetaCacheDuration)
	if f.Slug.Valid {
		db.SetRedis(ctx, FileMetaIDCacheKey(f.Slug.String), f.ID, FileMetaCacheDuration)
	}

	return nil
}

// ---------------------------------------------------------------------------
// 获取文件元数据（按 ID）
// ---------------------------------------------------------------------------

const getFileMetaByIDSQL = `
SELECT id, user_id, app_id, slug, sha512, filename, content_type, is_private, ban_comment, created_at
FROM file_metas
WHERE id = $1
`

func GetFileMetaByID(ctx context.Context, id int64) (*model.FileMetadata, *errs.Error) {
	meta, err := db.GetCached(
		ctx,
		"文件元数据",
		FileMetaCacheKey(id),
		FileMetaCacheDuration,
		func() (*model.FileMetadata, error) {
			var f model.FileMetadata
			if err := db.R.Pool.QueryRow(ctx, getFileMetaByIDSQL, id).Scan(
				&f.ID, &f.UserID, &f.AppID, &f.Slug, &f.SHA512, &f.Filename,
				&f.ContentType, &f.IsPrivate, &f.BanComment, &f.CreatedAt,
			); err != nil {
				return nil, err
			}
			return &f, nil
		},
	)
	if err != nil {
		return nil, err.AppendDetails("文件元数据获取失败")
	}
	return meta, nil
}

// ---------------------------------------------------------------------------
// 获取文件元数据（按 Slug）
// ---------------------------------------------------------------------------

const getFileMetaIDBySlugSQL = `
SELECT id FROM file_metas
WHERE slug = $1
`

func GetFileMetaBySlug(ctx context.Context, slug string) (*model.FileMetadata, *errs.Error) {
	id, err := db.GetCached(
		ctx,
		"文件元数据 ID",
		FileMetaIDCacheKey(slug),
		FileMetaCacheDuration,
		func() (*int64, error) {
			var id int64
			err := db.R.Pool.QueryRow(ctx, getFileMetaIDBySlugSQL, slug).Scan(&id)
			if err != nil {
				return nil, err
			}
			return &id, nil
		},
	)
	if err != nil {
		return nil, err.AppendDetails("文件元数据 ID 获取失败")
	}
	if id == nil {
		return nil, errs.NotFoundError().AppendDetails("文件不存在")
	}

	meta, err := GetFileMetaByID(ctx, *id)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, errs.NotFoundError().AppendDetails("文件不存在")
	}
	return meta, nil
}

// ---------------------------------------------------------------------------
// 更新文件元数据（按 ID）
// ---------------------------------------------------------------------------

const updateFileMetaSQL = `
UPDATE file_metas
SET filename      = $2,
    content_type  = $3,
    slug          = $4,
    is_private    = $5,
    user_id       = $6,
    app_id        = $7
WHERE id = $1
`

func UpdateFileMeta(ctx context.Context, f *model.FileMetadata) *errs.Error {
	oldMeta, getErr := GetFileMetaByID(ctx, f.ID)
	if getErr != nil {
		return getErr.AppendDetails("文件元数据更新失败")
	}

	if _, err := db.R.Pool.Exec(ctx, updateFileMetaSQL,
		f.ID, f.Filename, f.ContentType, f.Slug, f.IsPrivate, f.UserID, f.AppID,
	); err != nil {
		return errs.CreatePGError(err, "文件元数据", "Failed to update file meta").AppendDetails("文件元数据更新失败")
	}
	db.R.Del(ctx, FileMetaCacheKey(f.ID))
	if oldMeta.Slug.Valid && oldMeta.Slug.String != f.Slug.String {
		db.R.Del(ctx, FileMetaIDCacheKey(oldMeta.Slug.String))
	}
	if f.Slug.Valid {
		db.R.Del(ctx, FileMetaIDCacheKey(f.Slug.String))
	}

	return nil
}

// ---------------------------------------------------------------------------
// Upsert 文件元数据（按 Slug）
// ---------------------------------------------------------------------------

const getFileMetaSHA512BySlugSQL = `
SELECT sha512 FROM file_metas
WHERE slug = $1
`

const upsertFileMetaBySlugSQL = `
INSERT INTO file_metas (slug, sha512, filename, content_type, is_private, user_id, app_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (slug) WHERE slug IS NOT NULL
DO UPDATE SET
    sha512        = EXCLUDED.sha512,
    content_type  = EXCLUDED.content_type,
    filename      = EXCLUDED.filename,
    is_private    = EXCLUDED.is_private,
    user_id       = COALESCE(EXCLUDED.user_id, file_metas.user_id),
    app_id        = COALESCE(EXCLUDED.app_id,  file_metas.app_id)
RETURNING id, created_at
`

func UpsertFileMetaBySlug(ctx context.Context, f *model.FileMetadata) (string, *errs.Error) {
	if !f.Slug.Valid {
		return "", errs.BadRequestError().AppendDetails("未提供 Slug", "文件元数据设置失败")
	}

	var oldSHA512 string
	if err := db.R.Pool.QueryRow(ctx, getFileMetaSHA512BySlugSQL, f.Slug).Scan(&oldSHA512); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", errs.CreatePGError(err, "文件元数据", "Failed to query old file meta").AppendDetails("文件元数据设置失败")
	}

	if err := db.R.Pool.QueryRow(ctx, upsertFileMetaBySlugSQL,
		f.Slug, f.SHA512, f.Filename, f.ContentType, f.IsPrivate, f.UserID, f.AppID,
	).Scan(&f.ID, &f.CreatedAt); err != nil {
		return "", errs.CreatePGError(err, "文件元数据", "Failed to upsert file meta by slug").AppendDetails("文件元数据设置失败")
	}

	db.R.Del(ctx, FileMetaCacheKey(f.ID))
	db.R.Del(ctx, FileMetaIDCacheKey(f.Slug.String))

	return oldSHA512, nil
}

// ---------------------------------------------------------------------------
// 删除文件元数据
// ---------------------------------------------------------------------------

const deleteFileMetaSQL = `
DELETE FROM file_metas
WHERE id = $1
RETURNING slug
`

func DeleteFileMeta(ctx context.Context, id int64) *errs.Error {
	var slug null.String
	err := db.R.QueryRow(ctx, deleteFileMetaSQL, id).Scan(&slug)
	if err != nil {
		return errs.CreatePGError(err, "文件元数据", "Failed to delete file meta").AppendDetails("文件元数据删除失败")
	}

	db.R.Del(ctx, FileMetaCacheKey(id))
	if slug.Valid {
		db.R.Del(ctx, FileMetaIDCacheKey(slug.String))
	}

	return nil
}

// ---------------------------------------------------------------------------
// 校验 SHA512 是否存在
// ---------------------------------------------------------------------------

const checkSHA512ExistsSQL = `
SELECT EXISTS(SELECT 1 FROM file_metas WHERE sha512 = $1)
`

func CheckSHA512Exists(ctx context.Context, sha512 string) (bool, *errs.Error) {
	var exists bool
	if err := db.R.Pool.QueryRow(ctx, checkSHA512ExistsSQL, sha512).Scan(&exists); err != nil {
		return false, errs.CreatePGError(err, "文件元数据", "Failed to check SHA512 existence").AppendDetails("SHA512 存在性检查失败")
	}
	return exists, nil
}

// ---------------------------------------------------------------------------
// 校验 Slug 是否存在
// ---------------------------------------------------------------------------

const checkSlugExistsSQL = `
SELECT EXISTS(SELECT 1 FROM file_metas WHERE slug = $1)
`

func CheckSlugExists(ctx context.Context, slug string) (bool, *errs.Error) {
	var exists bool
	if err := db.R.Pool.QueryRow(ctx, checkSlugExistsSQL, slug).Scan(&exists); err != nil {
		return false, errs.CreatePGError(err, "文件元数据", "Failed to check slug existence").AppendDetails("Slug 存在性检查失败")
	}
	return exists, nil
}
