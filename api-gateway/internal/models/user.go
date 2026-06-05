package models

import "time"

// Tier defines the rate limiting rules for a class of users.
// These values are embedded directly into the JWT at token generation time —
// the gateway reads them from the token claims without any database lookup.
type Tier struct {
	Name                string `json:"name"`
	RequestsPerMinute   int    `json:"requests_per_minute"`
	// ThrottleAtPercent is the usage level (0–100) at which the gateway
	// introduces an artificial delay before forwarding the request.
	// Example: 75 means start throttling when the user hits 75% of their limit.
	ThrottleAtPercent   int    `json:"throttle_at_percent"`
}

// Predefined tiers. These are the source of truth for what each tier means.
// When generating tokens, pick one of these and embed it in the claims.
var (
	TierFree = Tier{
		Name:              "free",
		RequestsPerMinute: 10,
		ThrottleAtPercent: 70,
	}

	TierPremium = Tier{
		Name:              "premium",
		RequestsPerMinute: 100,
		ThrottleAtPercent: 75,
	}

	TierEnterprise = Tier{
		Name:              "enterprise",
		RequestsPerMinute: 1000,
		ThrottleAtPercent: 80,
	}
)

// TierByName returns a tier by its string name.
// Returns TierFree as the safe default if the name is unrecognised —
// failing open to a restrictive tier is safer than granting unlimited access.
func TierByName(name string) Tier {
	switch name {
	case "premium":
		return TierPremium
	case "enterprise":
		return TierEnterprise
	default:
		return TierFree
	}
}

// UserContext is the decoded, validated identity that flows through
// the request context after the auth middleware runs.
// Every subsequent middleware reads from this struct — not from the raw token.
type UserContext struct {
	UserID    string
	Email     string
	Tier      Tier
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// ThrottleThreshold returns the absolute request count at which throttling
// begins for this user, derived from their tier's percentage setting.
func (u *UserContext) ThrottleThreshold() int {
	return (u.Tier.RequestsPerMinute * u.Tier.ThrottleAtPercent) / 100
}