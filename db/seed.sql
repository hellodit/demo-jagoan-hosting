-- Seed data: ~500rb produk, ~2jt review.
-- Distribusi sengaja timpang (seperti dunia nyata):
--   produk 1-100   = produk promo, masing-masing 10.000 review (total 1jt)
--   produk 101-500rb = ekor panjang, berbagi 1jt review secara acak (rata-rata 2)
-- Tanpa ketimpangan ini, agregasi rating terlalu murah dan demo tidak
-- menunjukkan beban database yang realistis.
-- Durasi di VPS 2 vCPU: sekitar 1-3 menit.

SET synchronous_commit = off;

INSERT INTO categories (name)
SELECT 'Kategori ' || g FROM generate_series(1, 20) g;

INSERT INTO products (category_id, name, price, stock)
SELECT (g % 20) + 1,
       'Produk ' || g,
       (10000 + floor(random() * 4990000))::bigint,
       (floor(random() * 500))::int
FROM generate_series(1, 500000) g;

-- Produk promo: 100 x 10.000 review
INSERT INTO reviews (product_id, rating, body, created_at)
SELECT p, 1 + floor(random() * 5)::int, 'Ulasan produk promo',
       now() - random() * interval '365 days'
FROM generate_series(1, 100) p, generate_series(1, 10000);

-- Ekor panjang: 1jt review acak
INSERT INTO reviews (product_id, rating, body, created_at)
SELECT 101 + floor(random() * 499900)::bigint, 1 + floor(random() * 5)::int, 'Ulasan',
       now() - random() * interval '365 days'
FROM generate_series(1, 1000000);

-- Index standar yang "seharusnya ada". Demo TIDAK sengaja dibuat lambat
-- dengan menghapus index: query tetap mahal karena volumenya, bukan karena salah desain.
CREATE INDEX reviews_product_id_idx ON reviews (product_id);
CREATE INDEX reviews_created_at_idx ON reviews (created_at);

VACUUM ANALYZE categories;
VACUUM ANALYZE products;
VACUUM ANALYZE reviews;
