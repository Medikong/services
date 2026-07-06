package config

import platformconfig "github.com/Medikong/services/packages/go-platform/config"

const ServiceName = "user-service"

type Config struct {
	HTTPAddr    string
	DatabaseURL string
}

func Load() Config {
	return Config{
		HTTPAddr:    platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL: platformconfig.String("DATABASE_URL", ""),
	}
}
