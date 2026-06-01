package handlers

import (
	"net/http"

	"mock-backend/internal/middleware"
	"mock-backend/pkg/response"
)

// RecordsHandler simulates a real downstream financial records API.
// In a real system this would query a database. Here it returns deterministic
// fake data so the gateway always has something meaningful to proxy to.
type RecordsHandler struct{}

func NewRecordsHandler() *RecordsHandler {
	return &RecordsHandler{}
}

// financialRecord mirrors the shape a real service might return.
// Using typed structs (not raw maps) ensures the JSON shape is predictable
// and documented — important when the gateway demo shows response bodies.
type financialRecord struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Amount      int    `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
}

var seedRecords = []financialRecord{
	{ID: 1, Type: "deposit", Amount: 50000, Currency: "USD", Description: "Salary credit"},
	{ID: 2, Type: "withdrawal", Amount: 1200, Currency: "USD", Description: "Rent payment"},
	{ID: 3, Type: "deposit", Amount: 3500, Currency: "USD", Description: "Freelance income"},
	{ID: 4, Type: "withdrawal", Amount: 450, Currency: "USD", Description: "Utility bills"},
	{ID: 5, Type: "transfer", Amount: 10000, Currency: "USD", Description: "Investment transfer"},
}

func (h *RecordsHandler) List(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	response.Success(w, http.StatusOK, map[string]any{
		"records": seedRecords,
		"count":   len(seedRecords),
	}, map[string]any{
		// served_by is critical for the Kubernetes demo: when HPA spins up
		// multiple backend pods, this field shows which pod handled the request,
		// visually proving that load balancing is working.
		"served_by":  "mock-backend",
		"request_id": requestID,
		"endpoint":   "records",
	})
}