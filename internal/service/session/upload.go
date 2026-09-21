package session

import (
	"context"
	"ffhub-filestore/internal/config"
	"ffhub-filestore/internal/db"
	"ffhub-filestore/internal/errs"
	"ffhub-filestore/internal/model"
	"fmt"
)

func UploadSessionCacheKey(token string) string {
	return fmt.Sprintf("filestore:session:upload:token:%s", token)
}

// ---------------------------------------------------------------------------
// 创建上传会话
// ---------------------------------------------------------------------------

func CreateUploadSession(ctx context.Context, s *model.UploadSession) *errs.Error {
	if err := db.SetRedis(ctx, UploadSessionCacheKey(s.Token), s, config.C.UploadTTL); err != nil {
		return err.AppendDetails("上传会话创建失败")
	}
	return nil
}

// ---------------------------------------------------------------------------
// 获取上传会话
// ---------------------------------------------------------------------------

func GetUploadSessionByToken(ctx context.Context, token string) (*model.UploadSession, *errs.Error) {
	s, err := db.GetRedis[model.UploadSession](ctx, UploadSessionCacheKey(token))
	if err != nil {
		return nil, err.AppendDetails("上传会话获取失败")
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// 删除上传会话
// ---------------------------------------------------------------------------

func DeleteUploadSessionByToken(ctx context.Context, token string) {
	db.R.Del(ctx, UploadSessionCacheKey(token))
}
