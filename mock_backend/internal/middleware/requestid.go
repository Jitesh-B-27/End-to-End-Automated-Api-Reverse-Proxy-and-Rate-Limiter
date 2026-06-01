package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// contextKey is an unexported type for context keys in this package.
// Using a named type prevents key collisions with other packages that
// also store values in the request context.
type contextKey string

const RequestIDKey contextKey = "request_id"

// RequestID injects a unique trace identifier into every request.
//
// Priority order:
//  1. Use X-Trace-ID if the upstream gateway already assigned one
//     (this is exactly what your gateway will do — it injects X-Trace-ID
//     before forwarding, so the mock backend honours it end-to-end)
//  2. Use X-Request-ID as a fallback for direct calls
//  3. Generate a fresh UUID if neither header is present
//
// The ID is stored in the request context AND echoed back as a response
// header so the caller can correlate their request in logs and traces.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Trace-ID")
		if requestID == "" {
			requestID = r.Header.Get("X-Request-ID")
		}
		if requestID == "" {
			requestID = uuid.NewString()
		}

		ctx := context.WithValue(r.Context(), RequestIDKey, requestID)
		w.Header().Set("X-Trace-ID", requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID retrieves the request ID from context.
// Returns an empty string if not set — callers must handle this gracefully.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}