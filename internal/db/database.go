package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"ffhub-filestore/internal/errs"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

type Repo struct {
	*PDB
	*RDB
}

var R *Repo

func GetRedis[T any](
	ctx context.Context,
	key string,
) (*T, *errs.Error) {
	// 获取 Redis 缓存值
	res, err := R.Get(ctx, key).Result()
	if err == nil {
		// 命中空值
		if res == RedisEmptyMark {
			return nil, nil
		}
		// 命中数据
		var data T
		if err := json.Unmarshal([]byte(res), &data); err == nil {
			return &data, nil
		} else {
			// 缓存数据损坏，返回解析错误
			return nil, errs.CreateAndLogInternalError(err, "Failed to parse cached data")
		}
	} else if !errors.Is(err, redis.Nil) {
		// Redis 错误
		return nil, errs.CreateAndLogInternalError(err, "Failed to get cached data")
	}
	// 缓存未命中
	return nil, nil
}

func SetRedis[T any](
	ctx context.Context,
	key string,
	value T,
	duration time.Duration,
) *errs.Error {
	// 序列化数据
	bytes, err := json.Marshal(&value)
	if err != nil {
		return errs.CreateAndLogInternalError(err, "Failed to marshal cached data")
	}
	// 设置 Redis 缓存值
	if err := R.Set(ctx, key, bytes, duration).Err(); err != nil {
		return errs.CreateAndLogInternalError(err, "Failed to set cached data")
	}
	return nil
}

func GetCached[T any](
	ctx context.Context,
	item, key string,
	duration time.Duration,
	queryFn func() (*T, error), // 缓存未命中时的回源查询函数
) (*T, *errs.Error) {
	// 获取 Redis 缓存值
	res, err := R.Get(ctx, key).Result()
	if err == nil {
		// 命中空值
		if res == RedisEmptyMark {
			return nil, nil
		}
		// 命中数据
		var data T
		if err := json.Unmarshal([]byte(res), &data); err == nil {
			return &data, nil
		} else {
			// 缓存数据损坏，回源查询
			slog.Warn("Failed to parse cached data", "error", err)
		}
	} else if !errors.Is(err, redis.Nil) {
		// Redis 错误，回源查询
		slog.Warn("Failed to get cached data", "error", err)
	}

	// 数据为空，回源查询
	data, err := queryFn()
	if err == nil {
		// 查询成功，缓存数据，无论是否成功
		if data == nil {
			R.Set(ctx, key, RedisEmptyMark, duration)
			return nil, nil
		}
		SetRedis(ctx, key, data, duration)
		return data, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		// 数据库错误
		return nil, errs.CreatePGError(err, item, "Failed to query database")
	}

	// 数据库无数据，缓存空值标记，无论是否成功
	R.Set(ctx, key, RedisEmptyMark, duration)
	return nil, nil
}
