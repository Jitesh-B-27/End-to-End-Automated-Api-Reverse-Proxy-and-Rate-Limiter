package handlers

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler exposes the Prometheus /metrics scrape endpoint.
// Prometheus calls this endpoint on every scrape interval (default 15s).
// All metrics registered via promauto in the metrics package are
// automatically included — no manual registration needed here.
type MetricsHandler struct{}

func NewMetricsHandler() *MetricsHandler {
	return &MetricsHandler{}
}

func (h *MetricsHandler) Handle(w http.ResponseWriter, r *http.Request) {
	promhttp.Handler().ServeHTTP(w, r)
}