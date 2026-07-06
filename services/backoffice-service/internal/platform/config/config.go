package config

import platformconfig "github.com/Medikong/services/packages/go-platform/config"

const ServiceName = "backoffice-service"

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	CouponServiceURL string
}

func Load() Config {
	return Config{
		HTTPAddr:         platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL:      platformconfig.String("DATABASE_URL", ""),
		CouponServiceURL: platformconfig.String("COUPON_SERVICE_URL", "http://coupon-service:8080"),
	}
}
