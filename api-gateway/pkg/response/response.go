package response

import (
	"encoding/json"
	"net/http"
)

type Envelope struct {
	Status string        `json:"status"`
	Data   any           `json:"data,omitempty"`
	Error  *ErrorPayload `json:"error,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func JSON(w http.ResponseWriter, statusCode int, payload Envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func Success(w http.ResponseWriter, statusCode int, data any, meta map[string]any) {
	JSON(w, statusCode, Envelope{
		Status: "success",
		Data:   data,
		Meta:   meta,
	})
}

func Fail(w http.ResponseWriter, statusCode int, code, message string) {
	JSON(w, statusCode, Envelope{
		Status: "error",
		Error: &ErrorPayload{
			Code:    code,
			Message: message,
		},
	})
}