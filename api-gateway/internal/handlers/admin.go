package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"api-gateway/internal/ipblocklist"
	"api-gateway/pkg/response"

	"go.uber.org/zap"
)

// AdminHandler manages the IP blocklist via HTTP.
// All admin endpoints require the static admin token passed as
// Authorization: Bearer <ADMIN_TOKEN> — separate from user JWT tokens.
//
// These endpoints are the live demo controls:
//   POST   /admin/blocklist  — block an IP instantly
//   DELETE /admin/blocklist  — unblock an IP instantly
//   GET    /admin/blocklist  — list all currently blocked IPs
type AdminHandler struct {
	blocklist  *ipblocklist.Blocklist
	adminToken string
	log        *zap.Logger
}

func NewAdminHandler(
	bl *ipblocklist.Blocklist,
	adminToken string,
	log *zap.Logger,
) *AdminHandler {
	return &AdminHandler{
		blocklist:  bl,
		adminToken: adminToken,
		log:        log,
	}
}

// blockRequest is the expected JSON body for POST /admin/blocklist.
type blockRequest struct {
	IP  string `json:"ip"`
	TTL int    `json:"ttl_seconds"` // 0 means permanent
}

// BlockIP adds an IP to the Redis blocklist immediately.
// Effect is instant — the next request from that IP returns 403.
func (h *AdminHandler) BlockIP(w http.ResponseWriter, r *http.Request) {
	if !h.isAuthorised(r) {
		response.Fail(w, http.StatusUnauthorized,
			"UNAUTHORISED", "valid admin token required")
		return
	}

	var req blockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Fail(w, http.StatusBadRequest,
			"INVALID_BODY", "request body must be valid JSON with an ip field")
		return
	}

	if req.IP == "" {
		response.Fail(w, http.StatusBadRequest,
			"MISSING_IP", "ip field is required")
		return
	}

	ttl := time.Duration(req.TTL) * time.Second

	if err := h.blocklist.Block(r.Context(), req.IP, ttl); err != nil {
		h.log.Error("failed to block ip",
			zap.String("ip", req.IP),
			zap.Error(err),
		)
		response.Fail(w, http.StatusInternalServerError,
			"BLOCK_FAILED", "failed to block IP address")
		return
	}

	h.log.Info("ip blocked via admin endpoint",
		zap.String("ip", req.IP),
		zap.Duration("ttl", ttl),
	)

	response.Success(w, http.StatusOK, map[string]any{
		"ip":          req.IP,
		"ttl_seconds": req.TTL,
		"permanent":   req.TTL == 0,
	}, nil)
}

// unblockRequest is the expected JSON body for DELETE /admin/blocklist.
type unblockRequest struct {
	IP string `json:"ip"`
}

// UnblockIP removes an IP from the blocklist immediately.
func (h *AdminHandler) UnblockIP(w http.ResponseWriter, r *http.Request) {
	if !h.isAuthorised(r) {
		response.Fail(w, http.StatusUnauthorized,
			"UNAUTHORISED", "valid admin token required")
		return
	}

	var req unblockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Fail(w, http.StatusBadRequest,
			"INVALID_BODY", "request body must be valid JSON with an ip field")
		return
	}

	if req.IP == "" {
		response.Fail(w, http.StatusBadRequest,
			"MISSING_IP", "ip field is required")
		return
	}

	if err := h.blocklist.Unblock(r.Context(), req.IP); err != nil {
		h.log.Error("failed to unblock ip",
			zap.String("ip", req.IP),
			zap.Error(err),
		)
		response.Fail(w, http.StatusInternalServerError,
			"UNBLOCK_FAILED", "failed to unblock IP address")
		return
	}

	h.log.Info("ip unblocked via admin endpoint", zap.String("ip", req.IP))

	response.Success(w, http.StatusOK, map[string]any{
		"ip":      req.IP,
		"unblocked": true,
	}, nil)
}

// ListBlockedIPs returns all currently blocked IP addresses.
// Used by the demo frontend to show the live blocklist state.
func (h *AdminHandler) ListBlockedIPs(w http.ResponseWriter, r *http.Request) {
	if !h.isAuthorised(r) {
		response.Fail(w, http.StatusUnauthorized,
			"UNAUTHORISED", "valid admin token required")
		return
	}

	ips, err := h.blocklist.ListBlocked(r.Context())
	if err != nil {
		h.log.Error("failed to list blocked ips", zap.Error(err))
		response.Fail(w, http.StatusInternalServerError,
			"LIST_FAILED", "failed to retrieve blocked IP list")
		return
	}

	if ips == nil {
		ips = []string{}
	}

	response.Success(w, http.StatusOK, map[string]any{
		"blocked_ips": ips,
		"count":       len(ips),
	}, nil)
}

// isAuthorised checks that the request carries the static admin token.
// This is intentionally separate from the JWT user auth system —
// admin operations use a long-lived static secret, not a user token.
func (h *AdminHandler) isAuthorised(r *http.Request) bool {
	const prefix = "Bearer "
	authHeader := r.Header.Get("Authorization")
	if len(authHeader) <= len(prefix) {
		return false
	}
	token := authHeader[len(prefix):]
	return token == h.adminToken
}