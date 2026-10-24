package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// Cache membungkus go-redis dengan pola cache-aside.
// Kalau protect = true (STAMPEDE_PROTECT), miss dilindungi dari stampede:
//   - singleflight: di dalam satu proses, miss bersamaan untuk key yang sama
//     hanya menjalankan satu query.
//   - lock Redis (SET NX): antar instance/proses, hanya satu yang mengisi ulang.
type Cache struct {
	rdb     *redis.Client
	protect bool
	group   singleflight.Group
}

type LoadFunc func(ctx context.Context) ([]byte, error)

// Remember: cek Redis, kalau miss jalankan load, simpan, lalu kembalikan.
// Padanan Cache::remember di Laravel, versi []byte.
func (c *Cache) Remember(ctx context.Context, key string, ttl time.Duration, load LoadFunc) ([]byte, error) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, redis.Nil) {
		// Redis bermasalah: jangan ikut menjatuhkan request, langsung ke DB.
		log.Printf("redis GET %s gagal, fallback ke DB: %v", key, err)
		return load(ctx)
	}

	if !c.protect {
		return c.fill(ctx, key, ttl, load)
	}

	// singleflight: semua goroutine dengan key sama menunggu satu hasil.
	// WithoutCancel: kalau client pertama memutus koneksi, request lain
	// yang ikut menunggu tidak ikut gagal.
	v, err, _ := c.group.Do(key, func() (any, error) {
		return c.fillWithLock(context.WithoutCancel(ctx), key, ttl, load)
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// RememberJSON adalah helper generik: hasil load di-encode JSON sebelum disimpan.
func RememberJSON[T any](ctx context.Context, c *Cache, key string, ttl time.Duration,
	load func(ctx context.Context) (T, error)) (T, error) {

	var out T
	b, err := c.Remember(ctx, key, ttl, func(ctx context.Context) ([]byte, error) {
		v, err := load(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

// Forget menghapus key. Panggil SETELAH transaksi database commit.
func (c *Cache) Forget(ctx context.Context, keys ...string) {
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		log.Printf("redis DEL %v gagal: %v", keys, err)
	}
}

func (c *Cache) fill(ctx context.Context, key string, ttl time.Duration, load LoadFunc) ([]byte, error) {
	b, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.rdb.Set(ctx, key, b, withJitter(ttl)).Err(); err != nil {
		log.Printf("redis SET %s gagal: %v", key, err)
	}
	return b, nil
}

// Lepas lock hanya kalau masih milik kita (token sama).
var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0`)

func (c *Cache) fillWithLock(ctx context.Context, key string, ttl time.Duration, load LoadFunc) ([]byte, error) {
	lockKey := "lock:" + key
	token := randomToken()

	ok, err := c.rdb.SetNX(ctx, lockKey, token, 10*time.Second).Result()
	if err != nil {
		return c.fill(ctx, key, ttl, load)
	}
	if ok {
		defer unlockScript.Run(ctx, c.rdb, []string{lockKey}, token)

		// Cek ulang: mungkin instance lain baru saja mengisi.
		if b, err := c.rdb.Get(ctx, key).Bytes(); err == nil {
			return b, nil
		}
		return c.fill(ctx, key, ttl, load)
	}

	// Instance lain sedang mengisi. Tunggu sebentar, lalu baca hasilnya.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if b, err := c.rdb.Get(ctx, key).Bytes(); err == nil {
			return b, nil
		}
	}
	// Terlalu lama: ambil sendiri daripada user menunggu tanpa batas.
	return c.fill(ctx, key, ttl, load)
}

// withJitter menambah 0-10% acak supaya banyak key tidak kedaluwarsa bersamaan.
func withJitter(ttl time.Duration) time.Duration {
	return ttl + time.Duration(rand.Int64N(int64(ttl)/10+1))
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}
