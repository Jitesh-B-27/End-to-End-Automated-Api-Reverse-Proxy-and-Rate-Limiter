package handlers

import (
	"context"
	"net/http"
	"time"

	"mock-backend/internal/middleware"
	"mock-backend/pkg/response"

	"go.uber.org/zap"
)

// SlowHandler simulates a backend service under high load or with a slow
// database query. Its purpose is to deliberately trigger the circuit breaker
// in the gateway during demos and load tests.
//
// Why context-aware sleep instead of time.Sleep?
// time.Sleep cannot be interrupted. If the gateway closes the connection
// (timeout or client disconnect), this goroutine would keep sleeping and
// waste server resources. The select pattern below wakes up whichever
// event fires first — the delay completing or the client going away.
type SlowHandler struct {
	delayMs int
	log     *zap.Logger
}

func NewSlowHandler(delayMs int, log *zap.Logger) *SlowHandler {
	return &SlowHandler{delayMs: delayMs, log: log}
}

func (h *SlowHandler) Handle(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())
	delay := time.Duration(h.delayMs) * time.Millisecond

	h.log.Info("slow endpoint called — introducing artificial delay",
		zap.String("request_id", requestID),
		zap.Duration("delay", delay),
	)

	select {
	case <-time.After(delay):
		// Delay completed normally — respond with success
		response.Success(w, http.StatusOK, map[string]any{
			"message":    "response after deliberate delay",
			"delay_ms":   h.delayMs,
			"request_id": requestID,
		}, map[string]any{
			"served_by": "mock-backend",
			"endpoint":  "slow",
		})

	case <-r.Context().Done():
		// Client disconnected or gateway timed out before the delay completed.
		// Log this — it is useful evidence that the gateway's timeout is working.
		err := context.Cause(r.Context())
		h.log.Warn("client disconnected before slow response completed",
			zap.String("request_id", requestID),
			zap.Error(err),
		)
		// At this point the client is gone — writing to w is a no-op,
		// but we do it anyway to keep the handler's contract consistent.
		response.Fail(w, http.StatusGatewayTimeout, "CLIENT_DISCONNECTED", "client closed connection")
	}
}