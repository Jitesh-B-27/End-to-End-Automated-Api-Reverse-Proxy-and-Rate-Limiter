package response

import (
	"encoding/json"
	"net/http"
)

// Envelope is the standard response wrapper for every endpoint in this service.
// Using a consistent shape means the gateway and load tests can always predict
// the structure — no per-endpoint parsing surprises.
//
// Success:  { "status": "success", "data": { ... },  "meta": { ... } }
// Error:    { "status": "error",   "error": { ... }, "meta": { ... } }
type Envelope struct {
	Status string         `json:"status"`
	Data   any            `json:"data,omitempty"`
	Error  *ErrorPayload  `json:"error,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

// ErrorPayload carries structured error information.
// The Code field is a machine-readable string (e.g., "UPSTREAM_TIMEOUT")
// that the gateway can act on programmatically — not just a human message.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// JSON writes a JSON-encoded response with the given status code.
// It always sets Content-Type and never panics on encoding failure —
// it falls back to a plain-text 500 instead.
func JSON(w http.ResponseWriter, statusCode int, payload Envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// At this point the header is already written so we cannot change the
		// status code. Log the encoding failure upstream via the middleware layer.
		http.Error(w, `{"status":"error","error":{"code":"ENCODE_FAILURE","message":"failed to encode response"}}`, http.StatusInternalServerError)
	}
}

// Success is a convenience wrapper for 2xx responses.
func Success(w http.ResponseWriter, statusCode int, data any, meta map[string]any) {
	JSON(w, statusCode, Envelope{
		Status: "success",
		Data:   data,
		Meta:   meta,
	})
}

// Fail is a convenience wrapper for 4xx/5xx responses.
func Fail(w http.ResponseWriter, statusCode int, code, message string) {
	JSON(w, statusCode, Envelope{
		Status: "error",
		Error: &ErrorPayload{
			Code:    code,
			Message: message,
		},
	})
}