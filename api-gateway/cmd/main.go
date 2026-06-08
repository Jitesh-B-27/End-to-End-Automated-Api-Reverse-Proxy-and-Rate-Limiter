package main

import (
	"log"

	"api-gateway/internal/auth"
	"api-gateway/internal/circuitbreaker"
	"api-gateway/internal/config"
	"api-gateway/internal/database"
	"api-gateway/internal/ipblocklist"
	"api-gateway/internal/logger"
	"api-gateway/internal/proxy"
	"api-gateway/internal/ratelimiter"
	"api-gateway/internal/repository"
	"api-gateway/internal/server"

	"go.uber.org/zap"
)

func main() {
	// Step 1 — Configuration
	// Must succeed before anything else. A missing JWT_SECRET or BACKEND_URL
	// is a deployment error — fail loudly rather than running misconfigured.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	// Step 2 — Logger
	l, err := logger.New(cfg.AppEnv)
	if err != nil {
		log.Fatalf("logger initialisation failed: %v", err)
	}
	defer l.Sync() //nolint:errcheck

	// Step 3 — Redis
	// Both databases are pinged inside NewRedisClients — if Redis is not
	// reachable the gateway refuses to start rather than failing at runtime.
	redisClients, err := database.NewRedisClients(cfg, l)
	if err != nil {
		l.Fatal("redis connection failed", zap.Error(err))
	}
	defer redisClients.Close()

	// Step 4 — Lua script + Repository
	// The script is compiled once here. The SHA is stored in Redis via
	// SCRIPT LOAD and reused on every request via EVALSHA.
	script := ratelimiter.NewScript()
	repo := repository.NewCacheRepository(
		redisClients.RateLimit,
		redisClients.Cache,
		script,
	)

	// Step 5 — Core components
	validator := auth.NewValidator(cfg.JWTSecret)

	limiter := ratelimiter.NewLimiter(
		repo,
		cfg.RateLimitWindow(),
		cfg.ThrottleDelay(),
		l,
	)

	breaker := circuitbreaker.New(
		"mock-backend",
		cfg.CircuitBreakerFailureThreshold,
		cfg.CircuitBreakerRecovery(),
		l,
	)

	bl := ipblocklist.New(repo, l)

	// Step 6 — Reverse proxy
	// Parses and validates BACKEND_URL at startup — invalid URLs cause
	// immediate exit rather than a runtime failure on the first request.
	proxyHandler, err := proxy.New(cfg.BackendURL, l)
	if err != nil {
		l.Fatal("reverse proxy initialisation failed",
			zap.String("backend_url", cfg.BackendURL),
			zap.Error(err),
		)
	}

	// Step 7 — Server
	deps := server.Dependencies{
		Redis:        redisClients,
		Validator:    validator,
		Limiter:      limiter,
		Breaker:      breaker,
		Blocklist:    bl,
		ProxyHandler: proxyHandler,
	}

	srv := server.New(cfg, l, deps)

	// Step 8 — Start (blocks until SIGINT or SIGTERM)
	if err := srv.Start(); err != nil {
		l.Fatal("server exited with error", zap.Error(err))
	}
}