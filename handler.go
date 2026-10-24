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
//
// Masing-masing punya Stats sendiri supaya angkanya tidak tercampur.

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

// DebugHandler melayani /debug/*.
type DebugHandler struct {
	cfg          Config
	db           *pgxpool.Pool
	nocacheStats *Stats
	cacheStats   *Stats
}

// GET /debug/stats : angka untuk dibacakan saat demo.
func (h *DebugHandler) DebugStats(c *gin.Context) {
	ps := h.db.Stat()
	c.JSON(http.StatusOK, gin.H{
		"nocache": gin.H{
			"db_product_queries": h.nocacheStats.DBProductQueries.Load(),
			"db_popular_queries": h.nocacheStats.DBPopularQueries.Load(),
		},
		"cache": cacheStatsJSON(h.cacheStats, h.cfg.StampedeProtect),
		// Pool koneksi dipakai bersama kedua variasi.
		"db_pool": gin.H{
			"max_conns":      ps.MaxConns(),
			"acquired_conns": ps.AcquiredConns(),
			"idle_conns":     ps.IdleConns(),
			// Berapa kali goroutine harus MENUNGGU koneksi kosong.
			// Ini padanan "worker PHP-FPM penuh" di dunia Go.
			"empty_acquire_count": ps.EmptyAcquireCount(),
			"acquire_wait_ms":     ps.AcquireDuration().Milliseconds(),
		},
	})
}

func cacheStatsJSON(s *Stats, stampedeProtect bool) gin.H {
	hits, misses := s.CacheHits.Load(), s.CacheMisses.Load()
	var ratio float64
	if hits+misses > 0 {
		ratio = float64(hits) / float64(hits+misses)
	}
	return gin.H{
		"stampede_protect":   stampedeProtect,
		"cache_hits":         hits,
		"cache_misses":       misses,
		"cache_errors":       s.CacheErrors.Load(),
		"hit_ratio":          ratio,
		"db_product_queries": s.DBProductQueries.Load(),
		"db_popular_queries": s.DBPopularQueries.Load(),
		"singleflight_dup":   s.SingleflightDup.Load(),
		"lock_waits":         s.LockWaits.Load(),
	}
}

// POST /debug/stats/reset
func (h *DebugHandler) ResetStats(c *gin.Context) {
	h.nocacheStats.Reset()
	h.cacheStats.Reset()
	c.Status(http.StatusNoContent)
}

func serverError(c *gin.Context, err error) {
	log.Printf("error: %v", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
