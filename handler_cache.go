package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// CachedHandler: endpoint /api/cache/...
// Pola cache-aside: cek Redis dulu, baru ke PostgreSQL kalau miss.
// Dengan STAMPEDE_PROTECT=true, miss dilindungi singleflight + lock Redis.
type CachedHandler struct {
	cfg   Config
	repo  *Repo
	rdb   *redis.Client
	cache *Cache
}

// GET /api/cache/products/:id
//
// Alur cache-aside: 1. cek Redis -> hit? kirim : 2. query DB -> 3. simpan -> kirim.
func (h *CachedHandler) ShowProduct(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()

	key := fmt.Sprintf("product:%d", id)
	b, err := h.rdb.Get(ctx, key).Bytes() // 1. cek
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			log.Printf("redis error: %v", err) // fallback
		}

		p, err := h.repo.FindProduct(ctx, id) // 2. miss
		if errors.Is(err, ErrNotFound) {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}

		b, _ = json.Marshal(p)
		h.rdb.Set(ctx, key, b, h.cfg.ProductTTL) // 3. simpan
	}

	c.Data(200, "application/json", b)
}

// GET /api/cache/products/popular
//
// Memakai helper RememberJSON, padanan Cache::remember.
// Dengan STAMPEDE_PROTECT=true, helper ini otomatis memakai singleflight + lock Redis.
func (h *CachedHandler) Popular(c *gin.Context) {
	items, err := RememberJSON(c.Request.Context(), h.cache, "products:popular", h.cfg.PopularTTL,
		func(ctx context.Context) ([]PopularItem, error) {
			return h.repo.Popular(ctx, 20)
		})
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// PUT /api/cache/products/:id  body: {"price": 150000}
//
// Urutan penting: UPDATE di dalam transaksi -> COMMIT -> baru hapus cache.
// Kalau cache dihapus sebelum commit, request lain bisa membaca data lama
// lalu menyimpannya kembali ke cache.
func (h *CachedHandler) UpdatePrice(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var body struct {
		Price int64 `json:"price" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	err := h.repo.UpdatePrice(ctx, id, body.Price) // sudah COMMIT saat kembali
	if errors.Is(err, ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "produk tidak ditemukan"})
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}

	// Hapus, jangan perbarui. Request baca berikutnya akan mengisi ulang.
	h.cache.Forget(ctx, fmt.Sprintf("product:%d", id), "products:popular")
	c.JSON(http.StatusOK, gin.H{"id": id, "price": body.Price})
}
