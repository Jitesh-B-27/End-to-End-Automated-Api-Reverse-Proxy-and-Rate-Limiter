package handlers

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"api-gateway/internal/circuitbreaker"
	"api-gateway/internal/database"
	"api-gateway/pkg/response"
)

var gatewayStartTime = time.Now()

// HealthHandler serves Kubernetes liveness and readiness probes.
// These endpoints deliberately bypass all auth and rate limit middleware —
// Kubernetes probes do not carry JWT tokens and must always get a response.
type HealthHandler struct {
	version    string
	redis      *database.RedisClients
	breaker    *circuitbreaker.Breaker
}

func NewHealthHandler(
	version string,
	redis *database.RedisClients,
	breaker *circuitbreaker.Breaker,
) *HealthHandler {
	return &HealthHandler{
		version: version,
		redis:   redis,
		breaker: breaker,
	}
}

// Liveness answers: is this process alive?
// Never checks external dependencies — if the process is running,
// liveness is true. Kubernetes restarts the pod only if this returns non-200.
func (h *HealthHandler) Liveness(w http.ResponseWriter, r *http.Request) {
	response.Success(w, http.StatusOK, map[string]any{
		"status":  "alive",
		"service": "api-gateway",
		"version": h.version,
		"uptime":  time.Since(gatewayStartTime).String(),
	}, nil)
}

// Readiness answers: is this pod ready to receive traffic?
// Checks Redis connectivity — if Redis is down the gateway cannot rate-limit
// so it should be removed from the load balancer rotation until Redis recovers.
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Ping both Redis databases to confirm connectivity.
	redisStatus := "ok"
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.redis.RateLimit.Ping(ctx).Err(); err != nil {
		redisStatus = "unreachable"
	}
	if err := h.redis.Cache.Ping(ctx).Err(); err != nil {
		redisStatus = "unreachable"
	}

	if redisStatus != "ok" {
		response.Fail(w, http.StatusServiceUnavailable,
			"DEPENDENCY_UNAVAILABLE",
			"redis is unreachable — gateway is not ready",
		)
		return
	}

	response.Success(w, http.StatusOK, map[string]any{
		"status":  "ready",
		"service": "api-gateway",
		"version": h.version,
		"uptime":  time.Since(gatewayStartTime).String(),
		"dependencies": map[string]any{
			"redis":           redisStatus,
			"circuit_breaker": h.breaker.CurrentState().String(),
		},
		"runtime": map[string]any{
			"goroutines":        runtime.NumGoroutine(),
			"memory_alloc_bytes": memStats.Alloc,
		},
	}, nil)
}