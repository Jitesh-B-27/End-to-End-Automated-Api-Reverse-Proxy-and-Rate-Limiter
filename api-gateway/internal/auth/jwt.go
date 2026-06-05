package auth

import (
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Sentinel errors allow middleware to respond with the correct HTTP status
// without inspecting error strings — always use errors.Is() to check these.
var (
	ErrTokenMissing  = errors.New("authorization token is missing")
	ErrTokenInvalid  = errors.New("authorization token is invalid")
	ErrTokenExpired  = errors.New("authorization token has expired")
	ErrTokenMalformed = errors.New("authorization token is malformed")
)

// Validator cryptographically verifies JWT tokens and extracts their claims.
// It holds the signing secret and exposes a single public method — Validate.
// Keeping the secret inside this struct means it never leaks into middleware
// or handler code.
type Validator struct {
	secret []byte
}

// NewValidator constructs a Validator with the given HMAC-SHA256 signing secret.
func NewValidator(secret string) *Validator {
	return &Validator{secret: []byte(secret)}
}

// Validate parses the raw token string, verifies the HMAC-SHA256 signature,
// checks expiry, and returns the decoded Claims on success.
//
// Error mapping:
//   - Malformed token string       → ErrTokenMalformed
//   - Valid signature but expired  → ErrTokenExpired
//   - Any other signature failure  → ErrTokenInvalid
func (v *Validator) Validate(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		v.keyFunc,
		// Require exp claim — tokens without an expiry are rejected.
		jwt.WithExpirationRequired(),
	)

	if err != nil {
		return nil, v.mapError(err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}

	// Validate required custom fields — a token missing UserID or Tier name
	// is structurally invalid even if the signature checks out.
	if claims.UserID == "" {
		return nil, fmt.Errorf("%w: missing user_id claim", ErrTokenMalformed)
	}
	if claims.Tier.Name == "" {
		return nil, fmt.Errorf("%w: missing tier claim", ErrTokenMalformed)
	}
	if claims.Tier.RequestsPerMinute <= 0 {
		return nil, fmt.Errorf("%w: tier requests_per_minute must be positive", ErrTokenMalformed)
	}

	return claims, nil
}

// keyFunc returns the signing key for the parser.
// We only support HMAC signing methods — reject anything else explicitly
// to prevent the "algorithm confusion" attack where an attacker switches
// the algorithm to "none" or an asymmetric method.
func (v *Validator) keyFunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, fmt.Errorf("%w: unexpected signing method %v",
			ErrTokenInvalid, token.Header["alg"])
	}
	return v.secret, nil
}

// mapError translates golang-jwt library errors into our sentinel errors.
// This decouples the rest of the codebase from the jwt library's error types.
func (v *Validator) mapError(err error) error {
	switch {
	case errors.Is(err, jwt.ErrTokenMalformed):
		return ErrTokenMalformed
	case errors.Is(err, jwt.ErrTokenExpired):
		return ErrTokenExpired
	case errors.Is(err, jwt.ErrTokenNotValidYet),
		errors.Is(err, jwt.ErrTokenSignatureInvalid),
		errors.Is(err, jwt.ErrTokenUnverifiable):
		return ErrTokenInvalid
	default:
		return fmt.Errorf("%w: %s", ErrTokenInvalid, err.Error())
	}
}