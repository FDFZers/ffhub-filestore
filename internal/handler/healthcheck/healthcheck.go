package healthcheck

import (
	"context"
	"fdfz-filestore/internal/db"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type HealthStatus struct {
	Status    string           `json:"status"` // "UP", "DOWN", "DEGRADED"
	Timestamp string           `json:"timestamp"`
	Checks    map[string]Check `json:"checks,omitempty"`
}

type Check struct {
	Status   string `json:"status"` // "UP" or "DOWN"
	Message  string `json:"message,omitempty"`
	Duration string `json:"duration,omitempty"`
}

var timeout = time.Second * 10

func check(ctx context.Context) HealthStatus {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := HealthStatus{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    make(map[string]Check),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	allUp := true

	// PostgreSQL
	wg.Go(func() {
		start := time.Now()
		check := Check{}

		if db.R.PDB == nil {
			check = Check{Status: "DOWN", Message: "postgres connection is nil"}
		} else {
			err := db.R.PDB.Ping(ctx)
			dur := time.Since(start)
			check.Duration = dur.String()
			if err != nil {
				check.Status = "DOWN"
				check.Message = err.Error()
			} else {
				check.Status = "UP"
			}
		}

		mu.Lock()
		result.Checks["postgres"] = check
		if check.Status != "UP" {
			allUp = false
		}
		mu.Unlock()
	})

	// Redis
	wg.Go(func() {
		start := time.Now()
		check := Check{}

		if db.R.RDB == nil {
			check = Check{Status: "DOWN", Message: "redis client is nil"}
		} else {
			err := db.R.RDB.Ping(ctx).Err()
			dur := time.Since(start)
			check.Duration = dur.String()
			if err != nil {
				check.Status = "DOWN"
				check.Message = err.Error()
			} else {
				check.Status = "UP"
			}
		}

		mu.Lock()
		result.Checks["redis"] = check
		if check.Status != "UP" {
			allUp = false
		}
		mu.Unlock()
	})

	wg.Wait()

	if allUp {
		result.Status = "UP"
	} else {
		result.Status = "DOWN"
	}

	return result
}

func RegisterHealthCheckRoutes(r *gin.Engine) {
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})

	r.GET("/healthz", func(c *gin.Context) {
		status := check(c.Request.Context())

		code := http.StatusOK
		if status.Status != "UP" {
			code = http.StatusServiceUnavailable
		}

		c.JSON(code, status)
	})
}
