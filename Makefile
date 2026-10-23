BIN := redis-cache-demo
DB  ?= postgres://postgres@127.0.0.1:5432/shop?sslmode=disable
VARIANT ?= cache

.PHONY: tidy build build-linux seed run run-sf stats reset load stampede

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

stats:
	curl -s localhost:8080/debug/stats | jq

reset:
	curl -s -X POST localhost:8080/debug/stats/reset
	redis-cli -a "$$REDIS_PASSWORD" --no-auth-warning FLUSHDB
	psql "$(DB)" -c "SELECT pg_stat_statements_reset();"

# make load VARIANT=nocache  atau  make load VARIANT=cache
load:
	k6 run -e BASE_URL=$(BASE_URL) -e VARIANT=$(VARIANT) k6/load-test.js

stampede:
	k6 run -e BASE_URL=$(BASE_URL) k6/stampede.js
