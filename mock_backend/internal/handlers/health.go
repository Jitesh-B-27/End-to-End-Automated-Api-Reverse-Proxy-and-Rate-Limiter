package handlers

import (
	"net/http"
	"runtime"
	"time"

	"mock-backend/pkg/response"
)

// serviceStartTime is recorded once at startup so the uptime field in the
// health response is always accurate without any external state.
var serviceStartTime = time.Now()

// HealthHandler handles liveness and readiness probes.
//
// Kubernetes calls /healthz (liveness) to decide if the pod should be
// restarted. It calls /readyz (readiness) to decide if the pod should
// receive traffic. Keeping them on separate endpoints lets you signal
// "I am alive but not yet ready" during startup — important for graceful
// rolling deployments.
type HealthHandler struct {
	serviceName string
	version     string
}

func NewHealthHandler(serviceName, version string) *HealthHandler {
	return &HealthHandler{
		serviceName: serviceName,
		version:     version,
	}
}

// Liveness answers: "Is this process alive and not deadlocked?"
// It should never depend on external services. If Redis or PostgreSQL are
// down, the pod is still alive — just not ready.
func (h *HealthHandler) Liveness(w http.ResponseWriter, r *http.Request) {
	response.Success(w, http.StatusOK, map[string]any{
		"status":  "alive",
		"service": h.serviceName,
		"version": h.version,
	}, nil)
}

// Readiness answers: "Is this process ready to serve production traffic?"
// For the mock backend this is trivially true as long as the process is up,
// since there are no external dependencies. When you add database connections
// later (in the control plane), readiness checks will include a DB ping.
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	response.Success(w, http.StatusOK, map[string]any{
		"status":       "ready",
		"service":      h.serviceName,
		"version":      h.version,
		"uptime":       time.Since(serviceStartTime).String(),
		"goroutines":   runtime.NumGoroutine(),
		"memory_alloc": memStats.Alloc,
	}, nil)
}