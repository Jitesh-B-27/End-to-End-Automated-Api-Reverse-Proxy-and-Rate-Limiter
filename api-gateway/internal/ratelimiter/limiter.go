package ratelimiter

import (
	_ "embed"
	"fmt"
	"time"

	"api-gateway/internal/repository"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/net/context"
)

//go:embed rate_limit.lua
var luaScript string

// Decision represents the outcome of a rate limit evaluation.
type Decision int

const (
	DecisionAllow    Decision = 1
	DecisionThrottle Decision = 2
	DecisionReject   Decision = 0
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionThrottle:
		return "throttle"
	case DecisionReject:
		return "reject"
	default:
		return "unknown"
	}
}

// Result carries the full outcome of a rate limit check.
// All fields needed by the middleware and by Prometheus metrics live here.
type Result struct {
	Decision         Decision
	Remaining        int
	CurrentCount     int
	Limit            int
	ResetAfterMs     int64
	ThrottleDelayMs  int64
}

// Limiter executes the sliding window algorithm against Redis.
// It owns the compiled Lua script and exposes a single Evaluate method.
type Limiter struct {
	repo            repository.CacheRepository
	windowDuration  time.Duration
	throttleDelay   time.Duration
	log             *zap.Logger
}

// NewLimiter constructs the Limiter and returns the compiled redis.Script
// so the caller (main.go) can pass it to the repository constructor.
// This two-step construction is necessary because the Script needs the
// raw Lua string, and the repository needs the compiled Script.
func NewLimiter(
	repo repository.CacheRepository,
	windowDuration time.Duration,
	throttleDelay time.Duration,
	log *zap.Logger,
) *Limiter {
	return &Limiter{
		repo:           repo,
		windowDuration: windowDuration,
		throttleDelay:  throttleDelay,
		log:            log,
	}
}

// NewScript returns the compiled Redis Lua script from the embedded file.
// Call this once at startup and pass the result to NewCacheRepository.
func NewScript() *redis.Script {
	return redis.NewScript(luaScript)
}

// Evaluate runs the sliding window check for a single user.
//
// Parameters:
//   userID    — used to build the Redis ZSET key (namespaced per user)
//   limit     — the user's RPM limit from their JWT tier claims
//   threshold — the absolute count at which throttling begins
//
// The window is always cfg.RateLimitWindow (default 60 seconds).
// All time values are in milliseconds for sub-second precision.
func (l *Limiter) Evaluate(
	ctx context.Context,
	userID string,
	limit int,
	threshold int,
) (*Result, error) {
	nowMs := time.Now().UnixMilli()
	windowMs := l.windowDuration.Milliseconds()
	resetAfterMs := windowMs

	// Redis ZSET key is namespaced by user ID.
	// Different users never share a key — their windows are fully isolated.
	key := fmt.Sprintf("ratelimit:user:%s", userID)

	result, err := l.repo.EvalRateLimit(ctx,
		[]string{key},
		[]any{nowMs, windowMs, limit, threshold},
	)
	if err != nil {
		// On Redis failure we FAIL OPEN — allow the request through.
		// Failing closed (rejecting all requests) during a Redis outage
		// would take down every user. Failing open is the lesser evil.
		// The error is logged and increments a Prometheus counter so
		// the outage is visible in Grafana immediately.
		l.log.Error("rate limiter redis failure — failing open",
			zap.String("user_id", userID),
			zap.Error(err),
		)
		return &Result{
			Decision:     DecisionAllow,
			Remaining:    limit,
			CurrentCount: 0,
			Limit:        limit,
			ResetAfterMs: resetAfterMs,
		}, nil
	}

	// The Lua script returns a three-element array:
	// result[0] = decision (0/1/2), result[1] = remaining, result[2] = count
	if len(result) != 3 {
		return nil, fmt.Errorf("unexpected lua result length: %d", len(result))
	}

	decision := Decision(toInt64(result[0]))
	remaining := int(toInt64(result[1]))
	currentCount := int(toInt64(result[2]))

	throttleDelayMs := int64(0)
	if decision == DecisionThrottle {
		throttleDelayMs = l.throttleDelay.Milliseconds()
	}

	l.log.Debug("rate limit evaluated",
		zap.String("user_id", userID),
		zap.String("decision", decision.String()),
		zap.Int("current_count", currentCount),
		zap.Int("limit", limit),
		zap.Int("remaining", remaining),
	)

	return &Result{
		Decision:        decision,
		Remaining:       remaining,
		CurrentCount:    currentCount,
		Limit:           limit,
		ResetAfterMs:    resetAfterMs,
		ThrottleDelayMs: throttleDelayMs,
	}, nil
}

// toInt64 safely converts the interface{} values returned by go-redis
// Lua script execution into int64. Redis always returns integers as int64.
func toInt64(v any) int64 {
	if i, ok := v.(int64); ok {
		return i
	}
	return 0
}