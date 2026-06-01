package handlers

import (
	"net/http"

	"mock-backend/internal/middleware"
	"mock-backend/pkg/response"

	"go.uber.org/zap"
)

// ErrorHandler always returns HTTP 500. Its sole purpose is to trip the
// circuit breaker in the gateway. Send enough requests here and the gateway
// will stop forwarding traffic to the backend entirely — that is the demo.
type ErrorHandler struct {
	log *zap.Logger
}

func NewErrorHandler(log *zap.Logger) *ErrorHandler {
	return &ErrorHandler{log: log}
}

func (h *ErrorHandler) Handle(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	h.log.Error("error endpoint called — returning deliberate 500",
		zap.String("request_id", requestID),
	)

	response.Fail(
		w,
		http.StatusInternalServerError,
		"SIMULATED_UPSTREAM_ERROR",
		"this endpoint always fails — it is used to trigger the circuit breaker",
	)
}