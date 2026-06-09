package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// CacheRepository abstracts all Redis operations behind an interface.
// Nothing outside this package calls Redis directly — this boundary means
// you can swap Redis for a mock in tests without changing any other code.
type CacheRepository interface {
	// Blocklist operations — uses Redis SET (DB 0, rate limit database)
	IsIPBlocked(ctx context.Context, ip string) (bool, error)
	BlockIP(ctx context.Context, ip string, ttl time.Duration) error
	UnblockIP(ctx context.Context, ip string) error
	ListBlockedIPs(ctx context.Context) ([]string, error)

	// Rate limit operations — Lua script execution
	EvalRateLimit(ctx context.Context, keys []string, args []any) ([]any, error)

	// Generic cache operations — uses DB 1 (cache database)
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// redisCacheRepository is the concrete Redis-backed implementation.
type redisCacheRepository struct {
	rateLimitClient *redis.Client
	cacheClient     *redis.Client
	luaScript       *redis.Script
}

// NewCacheRepository constructs the repository with both Redis clients
// and pre-loads the Lua script SHA into Redis via SCRIPT LOAD.
// Using EVALSHA on every request is faster than EVAL because Redis
// does not need to recompile the script on each call.
func NewCacheRepository(
	rateLimitClient *redis.Client,
	cacheClient *redis.Client,
	luaScript *redis.Script,
) CacheRepository {
	return &redisCacheRepository{
		rateLimitClient: rateLimitClient,
		cacheClient:     cacheClient,
		luaScript:       luaScript,
	}
}

// --- Blocklist operations ---

// IsIPBlocked checks membership in the Redis Set keyed "blocklist:ips".
// SISMEMBER is O(1) — this is the fastest possible Redis lookup.
func (r *redisCacheRepository) IsIPBlocked(ctx context.Context, ip string) (bool, error) {
	// 1. First check if it is part of the master blocklist set
	inSet, err := r.rateLimitClient.SIsMember(ctx, blocklist_key, ip).Result()
	if err != nil || !inSet {
		return false, err
	}

	// 2. If it is in the set, check if a temporary expiry key exists
	expiryKey := fmt.Sprintf("blocklist:expiry:%s", ip)
	exists, err := r.rateLimitClient.Exists(ctx, expiryKey).Result()
	if err != nil {
		return false, fmt.Errorf("checking expiry key redis error: %w", err)
	}

	// 3. If there's no expiry key, but it's still in our set, it means the temporary ban expired!
	if exists == 0 {
		// Lazily clean up the orphaned member from the set in the background
		go func() {
			_ = r.rateLimitClient.SRem(context.Background(), blocklist_key, ip).Err()
		}()
		return false, nil
	}

	return true, nil
}

// BlockIP adds an IP to the blocklist.
// If ttl is 0 the entry is permanent — it survives Redis restarts
// if persistence is enabled. For temporary bans pass a positive TTL.
// We use a pipeline to atomically SADD and set an expiry on the member
// via a secondary key — Redis SETs do not support per-member TTLs natively.
func (r *redisCacheRepository) BlockIP(ctx context.Context, ip string, ttl time.Duration) error {
	pipe := r.rateLimitClient.Pipeline()
	pipe.SAdd(ctx, blocklist_key, ip)

	if ttl > 0 {
		// Store a separate expiry sentinel key. The blocklist middleware
		// checks this key alongside SISMEMBER to honour temporary bans.
		expiryKey := fmt.Sprintf("blocklist:expiry:%s", ip)
		pipe.Set(ctx, expiryKey, "1", ttl)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("BlockIP redis error: %w", err)
	}
	return nil
}

// UnblockIP removes an IP from the blocklist and cleans up its expiry key.
func (r *redisCacheRepository) UnblockIP(ctx context.Context, ip string) error {
	pipe := r.rateLimitClient.Pipeline()
	pipe.SRem(ctx, blocklist_key, ip)
	pipe.Del(ctx, fmt.Sprintf("blocklist:expiry:%s", ip))

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("UnblockIP redis error: %w", err)
	}
	return nil
}

// ListBlockedIPs returns all members of the blocklist set.
// Used by the admin endpoint to show currently blocked IPs in the demo.
func (r *redisCacheRepository) ListBlockedIPs(ctx context.Context) ([]string, error) {
	ips, err := r.rateLimitClient.SMembers(ctx, blocklist_key).Result()
	if err != nil {
		return nil, fmt.Errorf("ListBlockedIPs redis error: %w", err)
	}
	return ips, nil
}

// --- Rate limit operations ---

// EvalRateLimit executes the pre-loaded Lua script using EVALSHA.
// Keys[1] is the user's ZSET key. Args carry window config and limits.
// The Lua script runs atomically — Redis guarantees no other command
// executes between any two operations inside the script.
func (r *redisCacheRepository) EvalRateLimit(
	ctx context.Context,
	keys []string,
	args []any,
) ([]any, error) {
	result, err := r.luaScript.Run(ctx, r.rateLimitClient, keys, args...).Slice()
	if err != nil {
		return nil, fmt.Errorf("EvalRateLimit lua error: %w", err)
	}
	return result, nil
}

// --- Generic cache operations ---

func (r *redisCacheRepository) Get(ctx context.Context, key string) (string, error) {
	val, err := r.cacheClient.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache Get error: %w", err)
	}
	return val, nil
}

func (r *redisCacheRepository) Set(
	ctx context.Context,
	key string,
	value any,
	ttl time.Duration,
) error {
	if err := r.cacheClient.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache Set error: %w", err)
	}
	return nil
}

func (r *redisCacheRepository) Delete(ctx context.Context, key string) error {
	if err := r.cacheClient.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("cache Delete error: %w", err)
	}
	return nil
}

// blocklist_key is the Redis Set key that holds all blocked IP addresses.
// Defined as a package-level constant so it is never mistyped elsewhere.
const blocklist_key = "blocklist:ips"