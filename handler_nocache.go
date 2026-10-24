package main

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
)

// NoCacheHandler: endpoint /api/nocache/...
// Setiap request langsung menyentuh PostgreSQL. Ini baseline pembanding.
//
// Sengaja tidak ada PUT di sini: kalau harga bisa diubah lewat jalur yang
// tidak menghapus cache, /api/cache akan menyajikan harga lama sampai TTL habis.
type NoCacheHandler struct {
	repo *Repo
}

// GET /api/nocache/products/:id
func (h *NoCacheHandler) ShowProduct(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()

	p, err := h.repo.FindProduct(ctx, id)
	if errors.Is(err, ErrNotFound) {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, p)
}

// GET /api/nocache/products/popular
func (h *NoCacheHandler) Popular(c *gin.Context) {
	ctx := c.Request.Context()

	items, _ := h.repo.Popular(ctx, 20)
	c.JSON(200, gin.H{"data": items})
}
