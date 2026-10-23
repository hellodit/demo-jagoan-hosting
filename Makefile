BIN := redis-cache-demo
VARIANT ?= cache

# Di server, .env milik docker compose dipakai ulang. Dari host, PostgreSQL
# dijangkau lewat 127.0.0.1: alamat 172.28.0.1 hanya berlaku dari dalam kontainer.
-include .env
DB  ?= $(or $(subst @172.28.0.1:,@127.0.0.1:,$(DATABASE_URL)),postgres://postgres@127.0.0.1:5432/shop?sslmode=disable)
# App di kontainer (lihat docker-compose.yml). Lokal: make stats APP=http://localhost:8080
APP ?= http://172.28.0.10:8080

.PHONY: tidy build build-linux seed run run-sf deploy stats reset load stampede

tidy:
	go mod tidy

build:
	go build -o bin/$(BIN) .

# Build dari Mac untuk VPS Linux amd64
build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/$(BIN)-linux-amd64 .

seed:
	psql "$(DB)" -f db/schema.sql
	psql "$(DB)" -f db/seed.sql

run:
	go run .

# /api/cache dengan proteksi stampede
run-sf:
	STAMPEDE_PROTECT=true go run .

# Di server sebagai user deploy: tarik kode terbaru lalu build ulang app.
deploy:
	git pull --ff-only
	docker compose up -d --build

stats:
	curl -s $(APP)/debug/stats | jq

reset:
	curl -s -X POST $(APP)/debug/stats/reset
	docker compose exec redis redis-cli --no-auth-warning FLUSHDB
	psql "$(DB)" -c "SELECT pg_stat_statements_reset();"

# make load VARIANT=nocache  atau  make load VARIANT=cache
load:
	k6 run -e BASE_URL=$(BASE_URL) -e VARIANT=$(VARIANT) k6/load-test.js

stampede:
	k6 run -e BASE_URL=$(BASE_URL) k6/stampede.js
