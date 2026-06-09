package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"api-gateway/internal/auth"
	"api-gateway/internal/models"
	"api-gateway/internal/metrics"
	"api-gateway/pkg/response"

	"go.uber.org/zap"
)

// userContextKey is the unexported context key for UserContext.
// Using a named type prevents collisions with keys from other packages.
type userContextKey struct{}

// Authenticate is the JWT authentication middleware.
//
// It sits at Layer 4 in the middleware chain, after observability has
// already recorded the request and after the IP blocklist has already
// rejected banned clients. By this point we know the request is worth
// the cost of cryptographic verification.
//
// On success: extracts claims, converts to UserContext, stores in context,
// and calls next. Every middleware after this can safely call GetUserContext.
//
// On failure: writes the appropriate 4xx response and short-circuits.
// The request never reaches the rate limiter or proxy.
func Authenticate(validator *auth.Validator, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := GetRequestID(r.Context())

			// Extract the Bearer token from the Authorization header.
			// We are strict about the format: "Bearer <token>" exactly.
			// Missing or malformed headers are rejected immediately.
			tokenString, err := extractBearerToken(r)
			if err != nil {
				log.Warn("authentication failed — missing or malformed header",
					zap.String("request_id", requestID),
					zap.String("remote_addr", r.RemoteAddr),
					zap.Error(err),
				)
				metrics.AuthFailures.WithLabelValues("MISSING_TOKEN").Inc()
				response.Fail(w, http.StatusUnauthorized,
					"MISSING_TOKEN",
					"authorization header must be in the format: Bearer <token>",
				)
				return
			}

			// Cryptographically verify the token and decode its claims.
			claims, err := validator.Validate(tokenString)
			if err != nil {
				log.Warn("authentication failed — token validation error",
					zap.String("request_id", requestID),
					zap.String("remote_addr", r.RemoteAddr),
					zap.Error(err),
				)
				metrics.AuthFailures.WithLabelValues(classifyAuthError(err)).Inc()
				writeAuthError(w, err)
				return
			}

			// Convert claims to UserContext and inject into request context.
			// From this point forward every middleware reads from UserContext,
			// not from the raw token — the JWT library is fully isolated.
			userCtx := claims.ToUserContext()
			ctx := context.WithValue(r.Context(), userContextKey{}, userCtx)

			log.Debug("authentication successful",
				zap.String("request_id", requestID),
				zap.String("user_id", userCtx.UserID),
				zap.String("tier", userCtx.Tier.Name),
				zap.Int("rpm_limit", userCtx.Tier.RequestsPerMinute),
			)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetUserContext retrieves the UserContext from the request context.
// Returns nil if auth middleware did not run or the request was not
// authenticated — callers must check for nil before dereferencing.
func GetUserContext(ctx context.Context) *models.UserContext {
	userCtx, _ := ctx.Value(userContextKey{}).(*models.UserContext)
	return userCtx
}

// extractBearerToken pulls the raw token string from the Authorization header.
// Returns ErrTokenMissing if the header is absent or not in Bearer format.
func extractBearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", auth.ErrTokenMissing
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", auth.ErrTokenMalformed
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", auth.ErrTokenMissing
	}

	return token, nil
}

// writeAuthError maps auth sentinel errors to the correct HTTP responses.
// 401 Unauthorized — the token is missing, malformed, or invalid.
// 401 with specific code — the token is expired (client should refresh).
// We deliberately do not return 403 here — 403 means authenticated but
// not authorised. Auth failures are always 401.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrTokenExpired):
		response.Fail(w, http.StatusUnauthorized,
			"TOKEN_EXPIRED",
			"your token has expired — please generate a new one",
		)
	case errors.Is(err, auth.ErrTokenMalformed):
		response.Fail(w, http.StatusUnauthorized,
			"TOKEN_MALFORMED",
			"the token structure is invalid",
		)
	default:
		response.Fail(w, http.StatusUnauthorized,
			"TOKEN_INVALID",
			"the token could not be verified",
		)
	}
}

func classifyAuthError(err error) string {
	switch {
	case errors.Is(err, auth.ErrTokenExpired):
		return "TOKEN_EXPIRED"
	case errors.Is(err, auth.ErrTokenMalformed):
		return "TOKEN_MALFORMED"
	case errors.Is(err, auth.ErrTokenMissing):
		return "TOKEN_MISSING"
	default:
		return "TOKEN_INVALID"
	}
}