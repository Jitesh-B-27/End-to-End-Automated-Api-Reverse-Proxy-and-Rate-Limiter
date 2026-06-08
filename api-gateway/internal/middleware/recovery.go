package middleware

import (
	"net/http"

	"go.uber.org/zap"
)

// responseRecorder is defined here and shared across the middleware package.
// circuitbreaker.go and observability.go both use it.
type responseRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytesWritten += n
	return n, err
}

// Recovery catches panics in any handler and returns a clean 500
// instead of crashing the pod.
func Recovery(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					requestID := GetRequestID(r.Context())
					log.Error("panic recovered",
						zap.String("request_id", requestID),
						zap.Any("panic", err),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"status":"error","error":{"code":"INTERNAL_ERROR","message":"an unexpected error occurred"}}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}