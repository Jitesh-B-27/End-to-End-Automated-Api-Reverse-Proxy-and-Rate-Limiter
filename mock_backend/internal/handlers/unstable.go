package handlers

import (
	"math/rand"
	"net/http"

	"mock-backend/internal/middleware"
	"mock-backend/pkg/response"

	"go.uber.org/zap"
)

// UnstableHandler randomly fails at a configurable rate.
// This is more realistic than always-fail (error.go) — it simulates a
// degraded service: most requests succeed, but some fail unpredictably.
//
// Use case in demos: run the k6 load test against this endpoint and watch
// the circuit breaker's HALF-OPEN state in Grafana — it will oscillate
// between allowing and rejecting traffic as the failure rate fluctuates.
type UnstableHandler struct {
	failureRatePercent int
	log                *zap.Logger
}

func NewUnstableHandler(failureRatePercent int, log *zap.Logger) *UnstableHandler {
	return &UnstableHandler{
		failureRatePercent: failureRatePercent,
		log:                log,
	}
}

func (h *UnstableHandler) Handle(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	// rand.Intn is not cryptographically secure, which is intentional here —
	// we do not need security, we need speed and simplicity for simulation.
	if rand.Intn(100) < h.failureRatePercent {
		h.log.Warn("unstable endpoint — simulating failure",
			zap.String("request_id", requestID),
			zap.Int("failure_rate_percent", h.failureRatePercent),
		)
		response.Fail(
			w,
			http.StatusInternalServerError,
			"SIMULATED_RANDOM_FAILURE",
			"this request was randomly selected to fail",
		)
		return
	}

	response.Success(w, http.StatusOK, map[string]any{
		"message":    "lucky — this request succeeded",
		"request_id": requestID,
	}, map[string]any{
		"served_by":            "mock-backend",
		"endpoint":             "unstable",
		"failure_rate_percent": h.failureRatePercent,
	})
}