# redis-cache-demo-go

Aplikasi demo untuk Tech Corner Jagoan Hosting: **Redis caching untuk backend Go di bawah beban**.
Go + Gin, PostgreSQL 16 (pgx), Redis 7 (go-redis v9), k6.

Satu binary, dua variasi endpoint yang berjalan bersamaan:

| Prefix | Perilaku |
|---|---|
| `/api/nocache` | Setiap request langsung query ke PostgreSQL |
| `/api/cache` | Cache-aside: cek Redis, kalau miss query lalu `SET` dengan TTL |

Bandingkan keduanya tanpa restart, cukup ganti URL. `STAMPEDE_PROTECT=true` menambahkan proteksi stampede di `/api/cache/products/popular`: `singleflight` di dalam proses, lock `SET NX` antar proses (perlu restart).

## Endpoint

| Method | Path | Keterangan |
|---|---|---|
| GET | `/` dan `/healthz` | Health check JSON: ping PostgreSQL dan Redis, 503 kalau salah satu down |
| GET | `/api/nocache/products/:id` | Detail produk: JOIN kategori + agregasi rating, langsung ke DB |
| GET | `/api/cache/products/:id` | Sama, dengan cache-aside yang ditulis langsung di handler (lihat di bawah) |
| GET | `/api/nocache/products/popular` | 20 produk terpopuler 30 hari. Query berat (~150-200 ms) |
| GET | `/api/cache/products/popular` | Sama, memakai helper `RememberJSON`. Dipakai untuk demo stampede |
| PUT | `/api/cache/products/:id` | Ubah harga dalam transaksi, hapus cache **setelah commit**. Hanya ada di variasi cache supaya cache tidak basi |

## Alur cache-aside

`CachedHandler.ShowProduct` ditulis tanpa helper supaya tiga langkahnya terlihat baris per baris:

```go
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
```

- `redis.Nil` berarti key belum ada (miss biasa). Error lain berarti Redis bermasalah: dicatat ke log, request tetap dilayani dari PostgreSQL.
- Hit mengirim byte JSON dari Redis apa adanya, tanpa unmarshal lalu marshal lagi.
- Endpoint ini tidak memakai proteksi stampede. Untuk itu lihat `/api/cache/products/popular` yang memakai `RememberJSON`.

## Struktur kode

| File | Isi |
|---|---|
| `main.go` | Wiring: pgxpool (`DB_MAX_CONNS`), go-redis dengan timeout pendek, router Gin, graceful shutdown |
| `handler.go` | Helper bersama, `HealthHandler` untuk `/` dan `/healthz` |
| `handler_nocache.go` | `NoCacheHandler` (`/api/nocache`): setiap request langsung ke PostgreSQL |
| `handler_cache.go` | `CachedHandler` (`/api/cache`). `ShowProduct` = cache-aside ditulis langsung, `Popular` = `RememberJSON`, `UpdatePrice` = invalidasi setelah commit |
| `cache.go` | `Cache.Remember`, `RememberJSON[T]`, singleflight, lock Redis, TTL jitter |
| `repo.go` | Query SQL ke PostgreSQL |
| `config.go` | Konfigurasi dari env |
| `db/` | Skema dan seed (~500rb produk, ~2jt review, distribusi timpang) |
| `k6/` | `load-test.js` (closed model, sama dengan slide), `arrival-rate.js` (open model), `stampede.js` |
| `Dockerfile`, `docker-compose.yml` | Image app (distroless, nonroot) dan Redis |
| `deploy/` | `nginx.conf` (upstream keepalive ke kontainer app) |

## Menjalankan di lokal

```bash
go mod tidy                 # wajib sekali di awal: mengisi go.sum
make seed                   # butuh PostgreSQL dengan pg_stat_statements aktif
go run .
curl localhost:8080/api/nocache/products/42
curl localhost:8080/api/cache/products/42
```

`pg_stat_statements` harus ada di `shared_preload_libraries` (restart PostgreSQL), baru `CREATE EXTENSION` di `schema.sql` berfungsi.

## Deploy ke VPS

Susunan di server: **nginx dan PostgreSQL di host**, **app dan Redis di Docker Compose**.
Docker dan repo dijalankan oleh user `deploy` (anggota grup `docker`), bukan root.
Di dalam kontainer, app berjalan sebagai `nonroot` (distroless) dan Redis sebagai `redis`.

```
internet :443 ─▶ nginx (host) ─▶ 172.28.0.10:8080 app ─▶ 172.28.0.11 redis
                                         └──────────▶ 172.28.0.1:5432 PostgreSQL (host)
```

Tidak ada port Docker yang di-publish. nginx memanggil IP kontainer langsung, dan
ufw hanya membuka 22, 80, 443, plus 5432 untuk subnet `172.28.0.0/16`.

Setup awal (sekali, sebagai root):

1. `apt install docker.io docker-compose-v2 postgresql nginx certbot python3-certbot-nginx ufw`
2. User `deploy` + `usermod -aG docker deploy`
3. PostgreSQL: `shared_preload_libraries = 'pg_stat_statements'`, `listen_addresses = '*'`,
   role dan database `shop`, baris `pg_hba.conf`:
   `host shop shop 172.28.0.0/16 scram-sha-256`
4. `deploy/nginx.conf` ke `/etc/nginx/sites-available/`, lalu `certbot --nginx -d [domain]`

Sebagai user `deploy`:

```bash
git clone https://github.com/hellodit/demo-jagoan-hosting && cd demo-jagoan-hosting
cp .env.example .env && chmod 600 .env    # isi password
docker compose up -d --build
make seed                                 # memakai DATABASE_URL dari .env via 127.0.0.1
make deploy                               # update berikutnya: git pull + build ulang
```

Nyalakan/matikan proteksi stampede: edit `STAMPEDE_PROTECT` di `.env`, lalu `docker compose up -d`.

## Urutan demo

```bash
# Sebelum setiap babak
make reset                                     # FLUSHDB, pg_stat_statements_reset
# Babak 1: tanpa cache
k6 run -e BASE_URL=https://[domain] -e VARIANT=nocache k6/load-test.js
# Babak 2: dengan cache, parameter k6 sama, tanpa restart
k6 run -e BASE_URL=https://[domain] -e VARIANT=cache k6/load-test.js
# Babak 3 (stampede): POPULAR_TTL=5s, bandingkan STAMPEDE_PROTECT=false vs true
k6 run -e BASE_URL=https://[domain] k6/stampede.js
```

Yang dibandingkan setelah setiap babak:

- RPS dan p95/p99 di ringkasan k6.
- Kolom `calls` di `pg_stat_statements`: berapa query yang benar-benar sampai ke database.

```bash
sudo -u postgres psql shop -c "SELECT calls, round(mean_exec_time::numeric, 1) AS avg_ms, left(query, 60) AS query
  FROM pg_stat_statements WHERE query ILIKE '%reviews%' ORDER BY calls DESC LIMIT 5;"
```

`/api/nocache` dan `/api/cache` menjalankan SQL yang sama, jadi `pg_stat_statements` tidak bisa membedakan keduanya. Karena itu `make reset` wajib dijalankan di antara babak.

## Catatan

- Handler `/api/nocache` sengaja ditulis seminimal mungkin: id yang tidak valid dan error database tidak ditangani maupun dicatat ke log. Error database tetap dibalas 200, jadi angka `status 200` di k6 babak 1 bisa terlihat lebih baik dari kenyataannya.
- k6 jalankan dari mesin terpisah, idealnya VPS kedua di data center yang sama. Lihat analisa di dokumen `analisa-demo-go.md` di project Speakers.
