package cfg

import (
	"fmt"
	"os"
	"strings"
	"time"
)

var C *Config

type Config struct {
	PostgresURL    string
	RedisURL       string
	APIKey         string
	TrustedProxies []string
	SharedDir      string
	DownloadTTL    time.Duration
}

func LoadConfig() error {
	cfg := &Config{}
	var err error

	if cfg.PostgresURL, err = requireEnv("POSTGRESQL_URL"); err != nil {
		return err
	}
	if cfg.RedisURL, err = requireEnv("REDIS_URL"); err != nil {
		return err
	}
	if cfg.APIKey, err = requireEnv("API_KEY"); err != nil {
		return err
	} else if len(cfg.APIKey) == 0 {
		return fmt.Errorf("API key not set")
	}

	for p := range strings.SplitSeq(env("TRUSTED_PROXIES", ""), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.TrustedProxies = append(cfg.TrustedProxies, p)
		}
	}

	cfg.SharedDir = env("SHARED_DIR", "./shared")
	cfg.DownloadTTL = envDur("DOWNLOAD_TTL", 5*time.Minute)

	C = cfg
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("load env: %s is required", key)
	}
	return v, nil
}
