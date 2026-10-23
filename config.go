package main

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port          string
	DatabaseURL   string
	DBMaxConns    int32
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	// StampedeProtect: endpoint /api/cache memakai singleflight + lock Redis
	// saat miss. Matikan untuk mendemokan stampede, nyalakan untuk solusinya.
	StampedeProtect bool
	ProductTTL      time.Duration
	PopularTTL      time.Duration
	NotFoundTTL     time.Duration
}

func LoadConfig() Config {
	return Config{
		Port:            env("PORT", "8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://postgres@127.0.0.1:5432/shop?sslmode=disable"),
		DBMaxConns:      int32(envInt("DB_MAX_CONNS", 20)),
		RedisAddr:       env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword:   os.Getenv("REDIS_PASSWORD"),
		RedisDB:         envInt("REDIS_DB", 0),
		StampedeProtect: envBool("STAMPEDE_PROTECT", false),
		ProductTTL:      envDuration("PRODUCT_TTL", 10*time.Minute),
		PopularTTL:      envDuration("POPULAR_TTL", 60*time.Second),
		NotFoundTTL:     envDuration("NOT_FOUND_TTL", 30*time.Second),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
