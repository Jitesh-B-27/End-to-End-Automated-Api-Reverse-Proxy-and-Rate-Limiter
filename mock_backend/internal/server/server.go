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

	"mock-backend/internal/config"
	"mock-backend/internal/handlers"
	"mock-backend/internal/middleware"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

const (
	version            = "1.0.0"
	readTimeout        = 10 * time.Second
	writeTimeout       = 30 * time.Second // must be > SLOW_ENDPOINT_DELAY_MS when testing locally
	idleTimeout        = 60 * time.Second
	shutdownGracePeriod = 15 * time.Second
)

// Server owns the HTTP server, router, and all handler dependencies.
// Keeping everything here means main.go stays tiny and this struct is
// independently testable.
type Server struct {
	cfg    *config.Config
	log    *zap.Logger
	router *chi.Mux
	http   *http.Server
}

// New constructs the server, wires all middleware, registers all routes,
// and returns a ready-to-start Server. It does not start listening.
func New(cfg *config.Config, log *zap.Logger) *Server {
	s := &Server{cfg: cfg, log: log}
	s.router = s.buildRouter()
	s.http = &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      s.router,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
	return s
}

// buildRouter registers every middleware and route.
// Middleware order matters: RequestID must run before StructuredLogger
// so the logger can read the request ID from context.
func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()

	// --- Global middleware (runs on every request) ---

	// Recover from panics so a single handler crash does not take down the pod
	r.Use(chimiddleware.Recoverer)
	// Assign / honour trace ID before anything else logs
	r.Use(middleware.RequestID)
	// Structured JSON access log — feeds Grafana Loki
	r.Use(middleware.StructuredLogger(s.log))
	// Standard timeout: if a handler takes longer than writeTimeout, cancel context
	r.Use(chimiddleware.Timeout(writeTimeout))

	// --- Instantiate handlers with their dependencies ---
	health   := handlers.NewHealthHandler(s.cfg.AppName, version)
	records  := handlers.NewRecordsHandler()
	slow     := handlers.NewSlowHandler(s.cfg.SlowEndpointDelayMs, s.log)
	errH     := handlers.NewErrorHandler(s.log)
	unstable := handlers.NewUnstableHandler(s.cfg.UnstableEndpointFailureRate, s.log)
	echo     := handlers.NewEchoHandler()
	debug    := handlers.NewDebugHandler(s.cfg.AppName, version)

	// --- Routes ---

	// Health probes — used by Kubernetes liveness and readiness probes
	r.Get("/healthz", health.Liveness)
	r.Get("/readyz", health.Readiness)

	// Core API routes — these are the gateway's forwarding targets
	r.Route("/api/v1", func(r chi.Router) {
		// Normal operation — gateway forwards most traffic here
		r.Get("/records", records.List)

		// Circuit breaker triggers — used in load tests and demos
		r.Get("/slow", slow.Handle)
		r.Get("/error", errH.Handle)
		r.Get("/unstable", unstable.Handle)

		// Gateway inspection — confirms gateway header injection works
		r.Get("/echo", echo.Handle)
		r.Get("/echo", echo.Handle)
	})

	// Internal diagnostic — restrict to internal network in production
	r.Get("/debug", debug.Handle)

	return r
}

// Start begins listening and blocks until the process receives SIGINT or
// SIGTERM (standard Kubernetes termination signal). It then drains in-flight
// requests for up to shutdownGracePeriod before exiting.
//
// This graceful shutdown pattern ensures that during a rolling deployment,
// pods finish their current requests before the process exits — no dropped
// connections for clients.
func (s *Server) Start() error {
	// Channel that receives OS termination signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start listening in a goroutine so we can block on the signal channel
	serverErr := make(chan error, 1)
	go func() {
		s.log.Info("mock-backend starting",
			zap.String("addr", s.http.Addr),
			zap.String("version", version),
			zap.String("env", s.cfg.AppEnv),
		)
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until signal or server error
	select {
	case err := <-serverErr:
		return fmt.Errorf("server failed to start: %w", err)
	case sig := <-quit:
		s.log.Info("shutdown signal received — draining connections",
			zap.String("signal", sig.String()),
			zap.Duration("grace_period", shutdownGracePeriod),
		)
	}

	// Give in-flight requests time to complete
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer cancel()

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	s.log.Info("server shut down cleanly")
	return nil
}