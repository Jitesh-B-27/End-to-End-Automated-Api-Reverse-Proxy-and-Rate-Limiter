package database

import (
	"context"
	"fmt"
	"time"

	"api-gateway/internal/config"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisClients holds two separate logical Redis clients.
// Separating rate limit and cache into different DBs means:
// - A FLUSHDB on the cache never wipes rate limit counters
// - Different eviction policies can be set per DB in redis.conf
// - Grafana can show separate metrics per logical database
type RedisClients struct {
	RateLimit *redis.Client
	Cache     *redis.Client
}

// NewRedisClients constructs and validates both Redis connections.
// The function pings both databases before returning — if Redis is not
// reachable, the gateway refuses to start rather than failing at runtime
// on the first request. Fail-fast startup is always preferable.
func NewRedisClients(cfg *config.Config, log *zap.Logger) (*RedisClients, error) {
	rateLimitClient, err := newClient(cfg, cfg.RedisRateLimitDB)
	if err != nil {
		return nil, fmt.Errorf("rate limit redis (db %d): %w", cfg.RedisRateLimitDB, err)
	}

	cacheClient, err := newClient(cfg, cfg.RedisCacheDB)
	if err != nil {
		return nil, fmt.Errorf("cache redis (db %d): %w", cfg.RedisCacheDB, err)
	}

	log.Info("redis connections established",
		zap.String("addr", cfg.RedisAddr),
		zap.Int("rate_limit_db", cfg.RedisRateLimitDB),
		zap.Int("cache_db", cfg.RedisCacheDB),
	)

	return &RedisClients{
		RateLimit: rateLimitClient,
		Cache:     cacheClient,
	}, nil
}

func newClient(cfg *config.Config, db int) (*redis.Client, error) {
	// PoolSize 0 tells go-redis to use its own default which is
	// 10 * runtime.GOMAXPROCS(0) — meaning 10 connections per available
	// CPU core. On a 4-core machine that is 40. On an 8-core machine
	// that is 80. This scales automatically with the host without any
	// hardcoded value.
	poolSize := cfg.RedisPoolSize

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           db,
		DialTimeout:  time.Duration(cfg.RedisDialTimeoutSeconds) * time.Second,
		ReadTimeout:  time.Duration(cfg.RedisReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.RedisWriteTimeoutSeconds) * time.Second,
		PoolSize:     poolSize,
		MinIdleConns: cfg.RedisMinIdleConns,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping failed: %w", err)
	}

	return client, nil
}

// Close shuts down both Redis connections cleanly.
// Called during graceful server shutdown to drain in-flight commands.
func (r *RedisClients) Close() error {
	if err := r.RateLimit.Close(); err != nil {
		return fmt.Errorf("closing rate limit redis: %w", err)
	}
	if err := r.Cache.Close(); err != nil {
		return fmt.Errorf("closing cache redis: %w", err)
	}
	return nil
}