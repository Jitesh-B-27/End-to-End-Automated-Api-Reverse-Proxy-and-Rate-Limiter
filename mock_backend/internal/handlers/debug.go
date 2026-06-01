package handlers

import (
	"net/http"
	"runtime"
	"time"

	"mock-backend/pkg/response"
)

// DebugHandler exposes internal runtime statistics.
// During load tests this endpoint lets you observe whether the mock backend
// itself is under memory or goroutine pressure — helpful for distinguishing
// "the gateway is the bottleneck" from "the backend is the bottleneck".
type DebugHandler struct {
	startTime   time.Time
	serviceName string
	version     string
}

func NewDebugHandler(serviceName, version string) *DebugHandler {
	return &DebugHandler{
		startTime:   time.Now(),
		serviceName: serviceName,
		version:     version,
	}
}

func (h *DebugHandler) Handle(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	response.Success(w, http.StatusOK, map[string]any{
		"service":     h.serviceName,
		"version":     h.version,
		"environment": r.Header.Get("X-Environment"),
		"uptime":      time.Since(h.startTime).String(),
		"runtime": map[string]any{
			"go_version":      runtime.Version(),
			"num_goroutines":  runtime.NumGoroutine(),
			"num_cpu":         runtime.NumCPU(),
			"os":              runtime.GOOS,
			"arch":            runtime.GOARCH,
		},
		"memory": map[string]any{
			"alloc_bytes":       mem.Alloc,
			"total_alloc_bytes": mem.TotalAlloc,
			"sys_bytes":         mem.Sys,
			"num_gc":            mem.NumGC,
			"heap_in_use_bytes": mem.HeapInuse,
		},
	}, map[string]any{
		"served_by": h.serviceName,
		"endpoint":  "debug",
		"note":      "expose this only to internal networks in production",
	})
}