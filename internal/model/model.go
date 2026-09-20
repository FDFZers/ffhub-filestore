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
	CreatedAt   time.Time   `json:"created_at"`
}

type UploadSession struct {
	Token       string    `json:"token"`
	Slug        string    `json:"slug,omitempty"`
	SHA512      string    `json:"sha512"`
	Filename    string    `json:"filename,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	IsPrivate   bool      `json:"is_private"`
	MaxSize     int64     `json:"max_size"`
	Upsert      bool      `json:"upsert"`
	CreatedAt   time.Time `json:"created_at"`
}

type DownloadSession struct {
	Token     string    `json:"token"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}
