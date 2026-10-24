# redis-cache-demo-go

Aplikasi demo untuk Tech Corner Jagoan Hosting: **Redis caching untuk backend Go di bawah beban**.
Go + Gin, PostgreSQL 16 (pgx), Redis 7 (go-redis v9), k6.

Satu binary, dua variasi endpoint yang berjalan bersamaan:

| Prefix | Perilaku |
|---|---|
| `/api/nocache` | Setiap request langsung query ke PostgreSQL |
| `/api/cache` | Cache-aside: cek Redis, kalau miss query lalu `SET` dengan TTL |

Bandingkan keduanya tanpa restart, cukup ganti URL. `STAMPEDE_PROTECT=true` menambahkan proteksi stampede di `/api/cache`: `singleflight` di dalam proses, lock `SET NX` antar proses (perlu restart).

## Endpoint

| Method | Path | Keterangan |
|---|---|---|
| GET | `/` dan `/healthz` | Health check JSON: ping PostgreSQL dan Redis, 503 kalau salah satu down |
| GET | `/api/nocache/products/:id` | Detail produk: JOIN kategori + agregasi rating, langsung ke DB |
| GET | `/api/cache/products/:id` | Sama, dengan cache-aside yang ditulis eksplisit di handler |
| GET | `/api/nocache/products/popular` | 20 produk terpopuler 30 hari. Query berat (~150-200 ms) |
| GET | `/api/cache/products/popular` | Sama, memakai helper `RememberJSON`. Dipakai untuk demo stampede |
| PUT | `/api/cache/products/:id` | Ubah harga dalam transaksi, hapus cache **setelah commit**. Hanya ada di variasi cache supaya cache tidak basi |
| GET | `/debug/stats` | Statistik terpisah per variasi (`nocache`, `cache`) plus pool koneksi DB bersama |
| POST | `/debug/stats/reset` | Reset statistik aplikasi |

## Struktur kode

| File | Isi |
|---|---|
| `main.go` | Wiring: pgxpool (`DB_MAX_CONNS`), go-redis dengan timeout pendek, router Gin, graceful shutdown |
| `handler.go` | Helper bersama, `DebugHandler` untuk `/debug/*` |
| `handler_nocache.go` | `NoCacheHandler` (`/api/nocache`): setiap request langsung ke PostgreSQL |
| `handler_cache.go` | `CachedHandler` (`/api/cache`). `ShowProduct` = cache-aside "telanjang", `UpdatePrice` = invalidasi setelah commit |
| `cache.go` | `Cache.Remember`, `RememberJSON[T]`, singleflight, lock Redis, TTL jitter, statistik |
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
curl localhost:8080/debug/stats
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
make reset                                     # stats app, FLUSHDB, pg_stat_statements_reset
# Babak 1: tanpa cache
k6 run -e BASE_URL=https://[domain] -e VARIANT=nocache k6/load-test.js
curl -s https://[domain]/debug/stats | jq .nocache
# Babak 2: dengan cache, parameter k6 sama, tanpa restart
k6 run -e BASE_URL=https://[domain] -e VARIANT=cache k6/load-test.js
curl -s https://[domain]/debug/stats | jq .cache
# Babak 3 (stampede): POPULAR_TTL=5s, bandingkan STAMPEDE_PROTECT=false vs true
k6 run -e BASE_URL=https://[domain] k6/stampede.js
```

Yang dibacakan dari `/debug/stats`:

- `nocache.db_product_queries` vs `cache.db_product_queries` dan `cache.hit_ratio`: berapa request yang benar-benar sampai ke database.
- `db_pool.empty_acquire_count`: berapa kali goroutine harus antre karena semua koneksi DB terpakai. Ini padanan "worker PHP-FPM penuh" di Go.
- `cache.db_popular_queries` dan `cache.singleflight_dup`: bukti stampede tertahan di babak 3.

## Catatan

- k6 jalankan dari mesin terpisah, idealnya VPS kedua di data center yang sama. Lihat analisa di dokumen `analisa-demo-go.md` di project Speakers.
- `/debug/*` hanya untuk demo. Jangan dibuka ke publik di aplikasi sungguhan.
