package config

import platformconfig "github.com/Medikong/services/packages/go-platform/config"

const ServiceName = "auth-service"

type Config struct {
	HTTPAddr string
}

func Load() Config {
	return Config{
		HTTPAddr: platformconfig.String("HTTP_ADDR", ":8080"),
	}
}
