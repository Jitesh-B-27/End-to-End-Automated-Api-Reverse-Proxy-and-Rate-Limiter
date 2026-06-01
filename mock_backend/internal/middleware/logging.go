package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// responseRecorder wraps http.ResponseWriter to capture the status code
// after the handler writes it. The standard ResponseWriter does not expose
// the status code after WriteHeader is called, so we intercept it here.
type responseRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK, // default if handler never calls WriteHeader
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

// StructuredLogger produces one JSON log line per request containing all
// fields that matter for observability: trace ID, method, path, status,
// latency, and response size.
//
// In production this output feeds directly into Grafana Loki where you can
// build dashboards and alerts on any individual field without regex parsing.
func StructuredLogger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := newResponseRecorder(w)

			next.ServeHTTP(rec, r)

			latency := time.Since(start)
			requestID := GetRequestID(r.Context())

			// Choose log level based on status code so errors are immediately
			// visible in Grafana without building a filter every time.
			logFn := log.Info
			if rec.statusCode >= 500 {
				logFn = log.Error
			} else if rec.statusCode >= 400 {
				logFn = log.Warn
			}

			logFn("request completed",
				zap.String("request_id", requestID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
				zap.Int("status_code", rec.statusCode),
				zap.Int("bytes_written", rec.bytesWritten),
				zap.Duration("latency", latency),
				zap.Int64("latency_ms", latency.Milliseconds()),
				// These headers are injected by the gateway before forwarding.
				// Logging them here lets you confirm the gateway is doing its job.
				zap.String("x_forwarded_for", r.Header.Get("X-Forwarded-For")),
				zap.String("x_real_ip", r.Header.Get("X-Real-IP")),
			)
		})
	}
}