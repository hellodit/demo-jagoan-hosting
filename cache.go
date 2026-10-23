package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// Stats dihitung di sisi aplikasi supaya hit ratio per fitur terlihat,
// tidak tercampur key lain di Redis.
type Stats struct {
	CacheHits        atomic.Int64
	CacheMisses      atomic.Int64
	CacheErrors      atomic.Int64
	DBProductQueries atomic.Int64
	DBPopularQueries atomic.Int64
	SingleflightDup  atomic.Int64 // request yang "nebeng" hasil request lain
	LockWaits        atomic.Int64 // request yang menunggu instance lain mengisi cache
}

func (s *Stats) Reset() {
	for _, c := range []*atomic.Int64{
		&s.CacheHits, &s.CacheMisses, &s.CacheErrors, &s.DBProductQueries,
		&s.DBPopularQueries, &s.SingleflightDup, &s.LockWaits,
	} {
		c.Store(0)
	}
}

// Cache membungkus go-redis dengan pola cache-aside.
// Kalau protect = true (STAMPEDE_PROTECT), miss dilindungi dari stampede:
//   - singleflight: di dalam satu proses, miss bersamaan untuk key yang sama
//     hanya menjalankan satu query.
//   - lock Redis (SET NX): antar instance/proses, hanya satu yang mengisi ulang.
type Cache struct {
	rdb     *redis.Client
	protect bool
	group   singleflight.Group
	stats   *Stats
}

type LoadFunc func(ctx context.Context) ([]byte, error)

// Remember: cek Redis, kalau miss jalankan load, simpan, lalu kembalikan.
// Padanan Cache::remember di Laravel, versi []byte.
func (c *Cache) Remember(ctx context.Context, key string, ttl time.Duration, load LoadFunc) ([]byte, error) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		c.stats.CacheHits.Add(1)
		return b, nil
	case errors.Is(err, redis.Nil):
		c.stats.CacheMisses.Add(1)
	default:
		// Redis bermasalah: jangan ikut menjatuhkan request, langsung ke DB.
		c.stats.CacheErrors.Add(1)
		log.Printf("redis GET %s gagal, fallback ke DB: %v", key, err)
		return load(ctx)
	}

	if !c.protect {
		return c.fill(ctx, key, ttl, load)
	}

	// singleflight: semua goroutine dengan key sama menunggu satu hasil.
	// WithoutCancel: kalau client pertama memutus koneksi, request lain
	// yang ikut menunggu tidak ikut gagal.
	v, err, shared := c.group.Do(key, func() (any, error) {
		return c.fillWithLock(context.WithoutCancel(ctx), key, ttl, load)
	})
	if shared {
		c.stats.SingleflightDup.Add(1)
	}
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
	c.stats.LockWaits.Add(1)
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
