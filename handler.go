package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Dua variasi endpoint berjalan bersamaan di satu proses:
//
//	/api/nocache/... : NoCacheHandler (handler_nocache.go), selalu ke PostgreSQL
//	/api/cache/...   : CachedHandler  (handler_cache.go), cache-aside dengan Redis

// parseID membaca :id dari URL. Kalau tidak valid, response 400 sudah ditulis.
func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id tidak valid"})
		return 0, false
	}
	return id, true
}

// HealthHandler melayani GET / dan GET /healthz.
type HealthHandler struct {
	db  *pgxpool.Pool
	rdb *redis.Client
}

// Ping PostgreSQL dan Redis dengan timeout pendek. 503 kalau salah satu down.
// Detail error hanya ditulis ke log, tidak dikirim ke client.
func (h *HealthHandler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()

	code, status := http.StatusOK, "ok"
	checks := gin.H{"postgres": "ok", "redis": "ok"}
	if err := h.db.Ping(ctx); err != nil {
		log.Printf("health: postgres: %v", err)
		code, status, checks["postgres"] = http.StatusServiceUnavailable, "degraded", "down"
	}
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		log.Printf("health: redis: %v", err)
		code, status, checks["redis"] = http.StatusServiceUnavailable, "degraded", "down"
	}
	c.JSON(code, gin.H{"status": status, "service": "redis-cache-demo", "checks": checks})
}

func serverError(c *gin.Context, err error) {
	log.Printf("error: %v", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
