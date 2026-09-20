package config

import "os"

var (
	Version   = "dev"
	Commit    = "dev"
	BuildTime = "dev"
	IsProd    = os.Getenv("APP_ENV") == "prod"
)
