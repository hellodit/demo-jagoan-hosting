// Demo Tech Corner: Redis caching untuk backend Go di bawah beban.
//
// Satu binary, dua variasi endpoint yang berjalan bersamaan:
//
//	/api/nocache/...  setiap request langsung ke PostgreSQL
//	/api/cache/...    cache-aside dengan Redis
//
// STAMPEDE_PROTECT=true menambahkan singleflight + lock Redis di /api/cache.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := LoadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// PostgreSQL: pool koneksi. MaxConns adalah "pintu" ke database.
	// Goroutine boleh ribuan, tapi yang bisa query bersamaan hanya MaxConns.
	pgCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	pgCfg.MaxConns = cfg.DBMaxConns
	db, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		log.Fatalf("postgres: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
		// Timeout pendek: Redis lambat tidak boleh menggantung request.
		DialTimeout:  time.Second,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
	})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}

	// Stats dipisah per variasi supaya angka /nocache dan /cache tidak tercampur.
	nocacheStats, cacheStats := &Stats{}, &Stats{}
	nocache := &NoCacheHandler{repo: &Repo{db: db, stats: nocacheStats}}
	cached := &CachedHandler{
		cfg:   cfg,
		repo:  &Repo{db: db, stats: cacheStats},
		rdb:   rdb,
		cache: &Cache{rdb: rdb, protect: cfg.StampedeProtect, stats: cacheStats},
		stats: cacheStats,
	}
	debug := &DebugHandler{cfg: cfg, db: db, nocacheStats: nocacheStats, cacheStats: cacheStats}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	// Daftarkan /products/popular sebelum /products/:id.
	nc := r.Group("/api/nocache")
	nc.GET("/products/popular", nocache.Popular)
	nc.GET("/products/:id", nocache.ShowProduct)

	ca := r.Group("/api/cache")
	ca.GET("/products/popular", cached.Popular)
	ca.GET("/products/:id", cached.ShowProduct)
	ca.PUT("/products/:id", cached.UpdatePrice)

	r.GET("/debug/stats", debug.DebugStats)
	r.POST("/debug/stats/reset", debug.ResetStats)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("listening :%s, STAMPEDE_PROTECT=%t, DB_MAX_CONNS=%d", cfg.Port, cfg.StampedeProtect, cfg.DBMaxConns)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
