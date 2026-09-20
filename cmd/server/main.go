package main

import (
	"context"
	"errors"
	"fdfz-filestore/internal/config"
	"fdfz-filestore/internal/db"
	"fdfz-filestore/internal/router"
	"fdfz-filestore/internal/service/storage"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/lmittmann/tint"
	"github.com/samber/slog-gin"
)

func main() {
	// 初始化日志记录器
	var logger *slog.Logger

	if config.IsProd {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
		gin.SetMode(gin.ReleaseMode)
	} else {
		logger = slog.New(tint.NewTextHandler(os.Stdout, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: "2006-01-02 15:04:05",
			NoColor:    false,
		}))
	}

	slog.SetDefault(logger)

	slog.Info("Starting filestore backend", "version", config.Version, "commit", config.Commit, "build_time", config.BuildTime, "is_prod", config.IsProd)

	// 读取 .env
	if !config.IsProd {
		if err := godotenv.Load(); err != nil {
			slog.Warn("Error loading .env file", "error", err)
		}
	}

	// 加载环境变量
	if err := config.LoadConfig(); err != nil {
		slog.Error("Error loading config", "error", err)
		os.Exit(1)
	}

	// 初始化存储目录
	if err := storage.EnsureDir(); err != nil {
		slog.Error("Error initializing storage directory", "error", err)
		os.Exit(1)
	}

	// 初始化数据库
	pdb, err := db.NewDB()
	if err != nil {
		slog.Error("Error initializing PostgreSQL database", "error", err)
		os.Exit(1)
	}
	slog.Info("PostgreSQL database initialized")

	rdb, err := db.NewRDB()
	if err != nil {
		slog.Error("Error initializing Redis database", "error", err)
		os.Exit(1)
	}
	slog.Info("Redis database initialized")

	db.R = &db.Repo{
		PDB: pdb,
		RDB: rdb,
	}

	// 运行数据库迁移
	if err := db.RunMigrations(); err != nil {
		slog.Error("Error running database migrations", "error", err)
		os.Exit(1)
	}
	slog.Info("Database migrations completed")

	// 启动服务器
	r, healthcheckR := gin.New(), gin.New()
	r.Use(sloggin.New(logger))
	healthcheckR.Use(sloggin.New(logger))

	if proxies := config.C.TrustedProxies; len(proxies) > 0 {
		if err := r.SetTrustedProxies(proxies); err != nil {
			slog.Error("Error setting trusted proxies", "error", err)
		}
	} else {
		if err := r.SetTrustedProxies(nil); err != nil {
			slog.Error("Error setting trusted proxies", "error", err)
		}
	}

	_ = healthcheckR.SetTrustedProxies(nil)

	router.InitRouter(r)
	router.InitHealthCheckRouter(healthcheckR)

	srv := &http.Server{
		Addr:    ":11410",
		Handler: r,
	}

	healthcheckSrv := &http.Server{
		Addr:    ":8080",
		Handler: healthcheckR,
	}

	go func() {
		slog.Info("Starting server on port 11410")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server startup failed", "error", err)
			os.Exit(1)
		}
	}()

	if config.IsProd {
		go func() {
			slog.Info("Starting healthcheck server on port 8080")
			if err := healthcheckSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("Healthcheck server startup failed", "error", err)
			}
		}()
	}

	// 优雅关闭服务器
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Filestore backend shutting down")

	slog.Info("Waiting for active connections to close")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Error shutting down server", "error", err)
	}
	slog.Info("Server shutdown completed")

	if err := healthcheckSrv.Shutdown(ctx); err != nil {
		slog.Error("Error shutting down healthcheck server", "error", err)
	}
	slog.Info("Healthcheck server shutdown completed")

	pdb.Close()
	slog.Info("PostgreSQL database connection pool closed")

	if err := rdb.Close(); err != nil {
		slog.Error("Error closing Redis connection pool", "error", err)
	}
	slog.Info("Redis database connection pool closed")

	slog.Info("Server exited gracefully")
}
