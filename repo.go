package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("product not found")

type Category struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Product struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	Price            int64    `json:"price"`
	Stock            int32    `json:"stock"`
	Category         Category `json:"category"`
	ReviewsAvgRating *float64 `json:"reviews_avg_rating"`
	ReviewsCount     int64    `json:"reviews_count"`
}

type PopularItem struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Price        int64   `json:"price"`
	ReviewsCount int64   `json:"reviews_count_30d"`
	AvgRating    float64 `json:"avg_rating_30d"`
}

// Query detail produk: JOIN kategori + agregasi rating.
// Untuk produk promo yang punya ribuan review, agregasi inilah bagian mahalnya.
const productSQL = `
SELECT p.id, p.name, p.price, p.stock, c.id, c.name, r.avg_rating, r.cnt
FROM products p
JOIN categories c ON c.id = p.category_id
CROSS JOIN LATERAL (
    SELECT avg(rating)::float8 AS avg_rating, count(*) AS cnt
    FROM reviews
    WHERE product_id = p.id
) r
WHERE p.id = $1`

// Query produk terpopuler 30 hari terakhir: membaca ratusan ribu baris review.
// Sengaja berat, dipakai untuk mendemokan cache stampede.
const popularSQL = `
SELECT p.id, p.name, p.price, s.cnt, s.avg_rating
FROM (
    SELECT product_id, count(*) AS cnt, avg(rating)::float8 AS avg_rating
    FROM reviews
    WHERE created_at > now() - interval '30 days'
    GROUP BY product_id
    ORDER BY cnt DESC
    LIMIT $1
) s
JOIN products p ON p.id = s.product_id
ORDER BY s.cnt DESC`

type Repo struct {
	db *pgxpool.Pool
}

func (r *Repo) FindProduct(ctx context.Context, id int64) (Product, error) {
	var p Product
	err := r.db.QueryRow(ctx, productSQL, id).Scan(
		&p.ID, &p.Name, &p.Price, &p.Stock,
		&p.Category.ID, &p.Category.Name,
		&p.ReviewsAvgRating, &p.ReviewsCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (r *Repo) Popular(ctx context.Context, limit int) ([]PopularItem, error) {
	rows, err := r.db.Query(ctx, popularSQL, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[PopularItem])
}

// UpdatePrice memakai transaksi eksplisit. Invalidasi cache dilakukan
// pemanggil SETELAH fungsi ini kembali, yaitu setelah COMMIT.
func (r *Repo) UpdatePrice(ctx context.Context, id, price int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // no-op kalau sudah commit

	tag, err := tx.Exec(ctx,
		`UPDATE products SET price = $2, updated_at = now() WHERE id = $1`, id, price)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
