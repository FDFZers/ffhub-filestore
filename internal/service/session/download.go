package session

import (
	"context"
	"ffhub-filestore/internal/config"
	"ffhub-filestore/internal/db"
	"ffhub-filestore/internal/errs"
	"ffhub-filestore/internal/model"
	"fmt"
)

func DownloadSessionCacheKey(token string) string {
	return fmt.Sprintf("filestore:session:download:token:%s", token)
}

// ---------------------------------------------------------------------------
// 创建下载会话
// ---------------------------------------------------------------------------

func CreateDownloadSession(ctx context.Context, s *model.DownloadSession) *errs.Error {
	if err := db.SetRedis(ctx, DownloadSessionCacheKey(s.Token), s, config.C.DownloadTTL); err != nil {
		return err.AppendDetails("下载会话创建失败")
	}
	return nil
}

// ---------------------------------------------------------------------------
// 获取下载会话
// ---------------------------------------------------------------------------

func GetDownloadSessionByToken(ctx context.Context, token string) (*model.DownloadSession, *errs.Error) {
	s, err := db.GetRedis[model.DownloadSession](ctx, DownloadSessionCacheKey(token))
	if err != nil {
		return nil, err.AppendDetails("下载会话获取失败")
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// 删除下载会话
// ---------------------------------------------------------------------------

func DeleteDownloadSessionByToken(ctx context.Context, token string) {
	db.R.Del(ctx, DownloadSessionCacheKey(token))
}
