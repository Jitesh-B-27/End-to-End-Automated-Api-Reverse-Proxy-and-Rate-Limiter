package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"api-gateway/internal/metrics"
	"api-gateway/internal/middleware"

	"go.uber.org/zap"
)

const backendName = "mock-backend"

// New constructs and returns a fully configured reverse proxy handler.
//
// The proxy is responsible for:
//   1. Stripping the Authorization header (tokens must not reach the backend)
//   2. Injecting forwarding headers (X-Forwarded-For, X-Real-IP, X-Trace-ID)
//   3. Forwarding the request over a pre-warmed connection pool
//   4. Streaming the backend response back to the client transparently
//   5. Recording backend latency and error metrics for Prometheus
func New(backendURL string, log *zap.Logger) (http.Handler, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL %q: %w", backendURL, err)
	}

	rp := httputil.NewSingleHostReverseProxy(target)

	// Replace the default transport with one tuned for gateway workloads.
	// The default http.DefaultTransport has conservative timeouts and a
	// small connection pool — inadequate for high-concurrency proxying.
	rp.Transport = newTransport()

	// Director mutates the outgoing request before it is sent to the backend.
	// This is where we strip sensitive headers and inject tracing metadata.
	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalDirector(req)
		mutateOutboundRequest(req)
	}

	// ModifyResponse runs after the backend responds but before we write
	// to the client. We use it to inject backend timing headers.
	rp.ModifyResponse = func(resp *http.Response) error {
		injectResponseHeaders(resp)
		return nil
	}

	// ErrorHandler is called when the backend is unreachable or times out.
	// Without this, the default behaviour writes a plain-text 502 with no
	// JSON body — inconsistent with the rest of the gateway's responses.
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		requestID := middleware.GetRequestID(r.Context())

		log.Error("proxy backend error",
			zap.String("request_id", requestID),
			zap.String("backend", backendName),
			zap.String("backend_url", backendURL),
			zap.Error(err),
		)

		// Classify the error type for Prometheus.
		errType := classifyError(err)
		metrics.ProxyErrors.WithLabelValues(backendName, errType).Inc()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"status":"error","error":{"code":"BACKEND_UNAVAILABLE","message":"upstream service is unreachable"}}`)
	}

	// Wrap the reverse proxy in a timing middleware that records backend
	// latency to Prometheus without modifying the proxy's internal logic.
	return newTimingHandler(rp, log), nil
}

// mutateOutboundRequest modifies the request that will be sent to the backend.
func mutateOutboundRequest(req *http.Request) {
	// Strip the Authorization header — the backend must never receive client
	// tokens. If a backend logs all headers, this prevents token leakage.
	req.Header.Del("Authorization")

	// Inject X-Forwarded-For with the original client IP.
	// If the header already exists (set by an upstream ingress), append to it.
	clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		clientIP = req.RemoteAddr
	}
	if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
		req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
	} else {
		req.Header.Set("X-Forwarded-For", clientIP)
	}

	// X-Real-IP carries the original client IP without the proxy chain.
	if req.Header.Get("X-Real-IP") == "" {
		req.Header.Set("X-Real-IP", clientIP)
	}

	// X-Trace-ID threads a single trace identifier from the client through
	// the gateway and into the backend. The mock backend echoes this header
	// back in its responses and logs — end-to-end trace correlation.
	if traceID := req.Header.Get("X-Trace-ID"); traceID != "" {
		req.Header.Set("X-Trace-ID", traceID)
	}

	// Tell the backend which gateway instance is forwarding this request.
	// Visible in the echo endpoint response during demos.
	req.Header.Set("X-Forwarded-By", "api-gateway")
}

// injectResponseHeaders adds metadata to the backend response before it
// reaches the client. These headers are visible in browser DevTools and
// in the k6 load test response inspection.
func injectResponseHeaders(resp *http.Response) {
	resp.Header.Set("X-Served-By", backendName)
	resp.Header.Set("X-Gateway-Version", "1.0.0")
}

// classifyError maps proxy transport errors to short label strings
// suitable for use as Prometheus metric labels.
func classifyError(err error) string {
	if err == nil {
		return "none"
	}
	switch {
	case isTimeoutError(err):
		return "timeout"
	case isConnectionRefused(err):
		return "connection_refused"
	default:
		return "unknown"
	}
}

func isTimeoutError(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

func isConnectionRefused(err error) bool {
	return err != nil && contains(err.Error(), "connection refused")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// newTransport builds an http.Transport tuned for high-concurrency proxying.
// Key differences from http.DefaultTransport:
//   - Larger connection pool (MaxIdleConns, MaxIdleConnsPerHost)
//   - Shorter TLS handshake timeout (backends are internal, not internet)
//   - KeepAlives enabled to reuse TCP connections across requests
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false, // backends are internal HTTP/1.1 services
	}
}

// timingHandler wraps the reverse proxy to record backend latency metrics.
type timingHandler struct {
	proxy  http.Handler
	log    *zap.Logger
}

func newTimingHandler(proxy http.Handler, log *zap.Logger) *timingHandler {
	return &timingHandler{proxy: proxy, log: log}
}

func (t *timingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	t.proxy.ServeHTTP(rec, r)

	duration := time.Since(start)
	statusCode := fmt.Sprintf("%d", rec.statusCode)

	metrics.ProxyRequestDuration.WithLabelValues(backendName, statusCode).Observe(duration.Seconds())

	t.log.Debug("proxy request completed",
		zap.String("request_id", middleware.GetRequestID(r.Context())),
		zap.String("backend", backendName),
		zap.Duration("duration", duration),
		zap.Int("status_code", rec.statusCode),
	)
}

// statusRecorder captures the status code written by the proxy.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.statusCode = code
	s.ResponseWriter.WriteHeader(code)
}