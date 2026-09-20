package config

import "os"

var (
	Commit    = "dev"
	BuildTime = "dev"
	IsProd    = os.Getenv("APP_ENV") == "prod"
)
