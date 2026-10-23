package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const notFoundMarker = "__not_found__"

// CachedHandler: endpoint /api/cache/...
// Pola cache-aside: cek Redis dulu, baru ke PostgreSQL kalau miss.
// Dengan STAMPEDE_PROTECT=true, miss dilindungi singleflight + lock Redis.
type CachedHandler struct {
	cfg   Config
	repo  *Repo
	rdb   *redis.Client
	cache *Cache
	stats *Stats
}

// GET /api/cache/products/:id
//
// Ditulis "telanjang" (tanpa helper) supaya alur cache-aside terlihat
// baris per baris: GET -> hit? kirim : query DB -> SET -> kirim.
func (h *CachedHandler) ShowProduct(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	key := fmt.Sprintf("product:%d", id)

	// 1. Cek Redis dulu.
	b, err := h.rdb.Get(ctx, key).Bytes()
	if err == nil {
		h.stats.CacheHits.Add(1)
		writeProductBytes(c, b)
		return
	}
	if errors.Is(err, redis.Nil) {
		h.stats.CacheMisses.Add(1)
	} else {
		h.stats.CacheErrors.Add(1)
		log.Printf("redis GET %s gagal, fallback ke DB: %v", key, err)
	}

	// 2. Miss: ambil dari PostgreSQL, 3. simpan ke Redis dengan TTL.
	load := func(ctx context.Context) ([]byte, error) {
		p, err := h.repo.FindProduct(ctx, id)
		if errors.Is(err, ErrNotFound) {
			// Negative caching: id yang tidak ada juga disimpan sebentar,
			// supaya request ke id palsu tidak terus menghantam database.
			h.rdb.Set(ctx, key, notFoundMarker, h.cfg.NotFoundTTL)
			return []byte(notFoundMarker), nil
		}
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		h.rdb.Set(ctx, key, b, withJitter(h.cfg.ProductTTL))
		return b, nil
	}

	if h.cache.protect {
		// Miss bersamaan untuk produk yang sama digabung jadi satu query.
		v, err, shared := h.cache.group.Do(key, func() (any, error) {
			return load(context.WithoutCancel(ctx))
		})
		if shared {
			h.stats.SingleflightDup.Add(1)
		}
		if err != nil {
			serverError(c, err)
			return
		}
		b = v.([]byte)
	} else {
		b, err = load(ctx)
		if err != nil {
			serverError(c, err)
			return
		}
	}

	writeProductBytes(c, b)
}

// Hit: byte JSON dari Redis dikirim apa adanya, tanpa unmarshal lalu marshal lagi.
func writeProductBytes(c *gin.Context, b []byte) {
	if string(b) == notFoundMarker {
		c.JSON(http.StatusNotFound, gin.H{"error": "produk tidak ditemukan"})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", b)
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
