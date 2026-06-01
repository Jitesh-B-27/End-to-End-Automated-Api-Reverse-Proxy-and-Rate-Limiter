package handlers

import (
	"net/http"

	"mock-backend/internal/middleware"
	"mock-backend/pkg/response"
)

// EchoHandler reflects back everything the gateway injected into the request:
// headers, trace IDs, forwarding metadata. This is the most useful debugging
// endpoint during gateway development — it lets you visually confirm that
// the gateway is correctly stripping auth headers, injecting X-Forwarded-For,
// and propagating X-Trace-ID end-to-end.
type EchoHandler struct{}

func NewEchoHandler() *EchoHandler {
	return &EchoHandler{}
}

func (h *EchoHandler) Handle(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	// Capture all incoming headers into a map.
	// In a real service you would never expose headers in responses —
	// this is a diagnostic-only endpoint that should be disabled in production.
	headers := make(map[string]string, len(r.Header))
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	// These are the specific headers your gateway will inject.
	// Confirming they appear here proves the gateway's Director function works.
	gatewayHeaders := map[string]string{
		"x_trace_id":      r.Header.Get("X-Trace-ID"),
		"x_forwarded_for": r.Header.Get("X-Forwarded-For"),
		"x_real_ip":       r.Header.Get("X-Real-IP"),
		// Authorization should NOT appear here — the gateway must strip it
		// before forwarding to protect tokens from leaking to downstream services.
		"authorization_stripped": func() string {
			if r.Header.Get("Authorization") == "" {
				return "yes — gateway correctly stripped auth header"
			}
			return "NO — gateway failed to strip auth header (bug!)"
		}(),
	}

	response.Success(w, http.StatusOK, map[string]any{
		"all_headers":     headers,
		"gateway_headers": gatewayHeaders,
		"method":          r.Method,
		"url":             r.URL.String(),
		"remote_addr":     r.RemoteAddr,
		"request_id":      requestID,
	}, map[string]any{
		"served_by": "mock-backend",
		"endpoint":  "echo",
		"note":      "diagnostic only — disable in production",
	})
}