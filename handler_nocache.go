package main

import (
	"errors"
	"net/http"

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
	id, ok := parseID(c)
	if !ok {
		return
	}

	p, err := h.repo.FindProduct(c.Request.Context(), id)
	if errors.Is(err, ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "produk tidak ditemukan"})
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

// GET /api/nocache/products/popular
func (h *NoCacheHandler) Popular(c *gin.Context) {
	items, err := h.repo.Popular(c.Request.Context(), 20)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}
