package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"api-gateway/internal/auth"
	"api-gateway/internal/circuitbreaker"
	"api-gateway/internal/config"
	"api-gateway/internal/database"
	"api-gateway/internal/handlers"
	"api-gateway/internal/ipblocklist"
	"api-gateway/internal/middleware"
	"api-gateway/internal/ratelimiter"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

const (
	readTimeout         = 10 * time.Second
	writeTimeout        = 35 * time.Second
	idleTimeout         = 60 * time.Second
	shutdownGracePeriod = 15 * time.Second
)

// Server owns every dependency and wires the complete request pipeline.
type Server struct {
	cfg     *config.Config
	log     *zap.Logger
	router  *chi.Mux
	http    *http.Server
}

// Dependencies is a flat struct carrying every constructed component
// into the server. Keeps New() readable — one argument instead of eight.
type Dependencies struct {
	Redis       *database.RedisClients
	Validator   *auth.Validator
	Limiter     *ratelimiter.Limiter
	Breaker     *circuitbreaker.Breaker
	Blocklist   *ipblocklist.Blocklist
	ProxyHandler http.Handler
}

// New constructs the server, builds the router, and wires all middleware.
// It does not start listening — call Start() for that.
func New(cfg *config.Config, log *zap.Logger, deps Dependencies) *Server {
	s := &Server{cfg: cfg, log: log}
	s.router = s.buildRouter(deps)
	s.http = &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.AppPort),
		Handler:      s.router,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
	return s
}

// buildRouter registers every route with its correct middleware chain.
//
// Route protection tiers:
//
//   Public (no auth, no rate limit):
//     GET /healthz
//     GET /readyz
//     GET /metrics
//
//   Admin (static token only):
//     POST   /admin/blocklist
//     DELETE /admin/blocklist
//     GET    /admin/blocklist
//
//   Protected (full 7-layer chain):
//     ALL /api/v1/*  → proxied to mock backend
func (s *Server) buildRouter(deps Dependencies) *chi.Mux {
	r := chi.NewRouter()

	// --- Global middleware (every request, no exceptions) ---
	r.Use(middleware.Recovery(s.log))
	r.Use(middleware.RequestID)
	r.Use(middleware.Observability(s.log))

	// --- Instantiate handlers ---
	healthHandler  := handlers.NewHealthHandler(s.cfg.AppVersion, deps.Redis, deps.Breaker)
	metricsHandler := handlers.NewMetricsHandler()
	adminHandler   := handlers.NewAdminHandler(deps.Blocklist, s.cfg.AdminToken, s.log)

	// --- Public routes — no auth, no rate limiting ---
	// Kubernetes calls /healthz and /readyz constantly.
	// Prometheus calls /metrics every 15 seconds.
	// None of these should consume rate limit quota.
	r.Get("/healthz", healthHandler.Liveness)
	r.Get("/readyz", healthHandler.Readiness)
	r.Get("/metrics", metricsHandler.Handle)

	// --- Admin routes — static token auth only ---
	r.Route("/admin", func(r chi.Router) {
		r.Post("/blocklist", adminHandler.BlockIP)
		r.Delete("/blocklist", adminHandler.UnblockIP)
		r.Get("/blocklist", adminHandler.ListBlockedIPs)
	})

	// --- Protected API routes — full middleware chain ---
	// This is the only route group that the load tester hits.
	// Every layer runs in this exact order:
	//   IPBlocklist → Auth → RateLimit → CircuitBreaker → Proxy
	r.Group(func(r chi.Router) {
		r.Use(middleware.IPBlocklist(deps.Blocklist, s.log))
		r.Use(middleware.Authenticate(deps.Validator, s.log))
		r.Use(middleware.RateLimit(deps.Limiter, s.log))
		r.Use(middleware.CircuitBreaker(deps.Breaker, s.log))

		// Catch-all: every path under /api/v1/ is proxied to the mock backend.
		// The mock backend owns these routes:
		//   GET /api/v1/records   — normal response
		//   GET /api/v1/slow      — delayed response (circuit breaker trigger)
		//   GET /api/v1/error     — always 500  (circuit breaker trigger)
		//   GET /api/v1/unstable  — random failures (half-open state demo)
		//   GET /api/v1/echo      — reflects gateway headers (injection verify)
		r.Handle("/api/v1/*", deps.ProxyHandler)
		r.Handle("/api/v1", deps.ProxyHandler)
	})

	return r
}

// Start begins listening for requests and blocks until SIGINT or SIGTERM.
// On signal it drains in-flight requests gracefully before exiting.
// Kubernetes sends SIGTERM during pod shutdown — this ensures no requests
// are dropped during rolling deployments.
func (s *Server) Start() error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		s.log.Info("api-gateway starting",
			zap.String("addr", s.http.Addr),
			zap.String("version", s.cfg.AppVersion),
			zap.String("env", s.cfg.AppEnv),
			zap.String("backend_url", s.cfg.BackendURL),
		)
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server failed to start: %w", err)
	case sig := <-quit:
		s.log.Info("shutdown signal received",
			zap.String("signal", sig.String()),
			zap.Duration("grace_period", shutdownGracePeriod),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer cancel()

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	s.log.Info("api-gateway shutdown complete")
	return nil
}