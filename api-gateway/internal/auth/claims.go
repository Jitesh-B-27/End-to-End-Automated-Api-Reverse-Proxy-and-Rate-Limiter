package auth

import (
	"api-gateway/internal/models"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the custom JWT payload embedded in every token.
//
// Design decision: tier data (RPM limit, throttle threshold) is embedded
// directly into the token at generation time. This means the gateway never
// needs a database call during request processing — the token itself carries
// everything needed to make rate limiting decisions.
//
// Trade-off: if you change a user's tier, their existing tokens still carry
// the old tier until they expire. For this project that is acceptable.
// In production you would add a token version field and invalidate via Redis.
type Claims struct {
	jwt.RegisteredClaims

	UserID string      `json:"user_id"`
	Email  string      `json:"email"`
	Tier   models.Tier `json:"tier"`
}

// ToUserContext converts validated JWT claims into the UserContext struct
// that flows through the request context for the rest of the middleware chain.
// This keeps the JWT library type (Claims) isolated to the auth package —
// nothing outside auth ever imports golang-jwt directly.
func (c *Claims) ToUserContext() *models.UserContext {
	return &models.UserContext{
		UserID:    c.UserID,
		Email:     c.Email,
		Tier:      c.Tier,
		IssuedAt:  c.IssuedAt.Time,
		ExpiresAt: c.ExpiresAt.Time,
	}
}