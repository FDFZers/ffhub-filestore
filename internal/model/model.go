package model

import (
	"time"

	"github.com/guregu/null/v6"
)

type FileMetadata struct {
	ID          int64       `json:"id"`
	Slug        null.String `json:"slug"`
	SHA512      string      `json:"sha512"`
	Filename    string      `json:"filename"`
	ContentType string      `json:"content_type"`
	IsPrivate   bool        `json:"is_private"`
	UserID      null.Int64  `json:"user_id"`
	AppID       null.Int64  `json:"app_id"`
	BanComment  null.String `json:"ban_comment"`
	CreatedAt   time.Time   `json:"created_at"`
}

type DownloadSession struct {
	Token     string    `json:"token"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}
