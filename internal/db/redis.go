package db

import (
	"context"
	"ffhub-filestore/internal/config"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const RedisEmptyMark = `<{NULL}>`

type RDB struct {
	*redis.Client
}

func NewRDB() (*RDB, error) {
	// 从环境变量获取 URL
	url := config.C.RedisURL
	if url == "" {
		return nil, fmt.Errorf("redis: environment variable REDIS_URL is not set")
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse URL: %w", err)
	}

	// 配置连接池
	opts.PoolSize = 20
	opts.MinIdleConns = 5
	opts.MaxIdleConns = 10
	opts.ConnMaxIdleTime = 1 * time.Minute
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second
	opts.DialTimeout = 5 * time.Second

	client := redis.NewClient(opts)

	// 验证连通性
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		if err := client.Close(); err != nil {
			slog.Warn("Failed to close Redis client ping connection", "error", err)
		}
		return nil, fmt.Errorf("redis: failed to ping: %w", err)
	}

	return &RDB{Client: client}, nil
}
