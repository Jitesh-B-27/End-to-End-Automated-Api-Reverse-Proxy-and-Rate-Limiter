//go:build ignore

// Run with: go run ./scripts/generate_tokens.go
// This is a standalone utility script, not part of the gateway binary.
// It generates JWT tokens for local testing and k6 load tests.
// The generated tokens are printed to stdout — pipe to a file for k6 use.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

// Tier mirrors the models.Tier struct — duplicated here so this script
// has zero dependency on the gateway's internal packages.
type Tier struct {
	Name              string `json:"name"`
	RequestsPerMinute int    `json:"requests_per_minute"`
	ThrottleAtPercent int    `json:"throttle_at_percent"`
}

type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Tier   Tier   `json:"tier"`
}

// testUsers defines the set of users we want tokens for.
// For load testing: generate one token per tier type.
// k6 will rotate through these tokens across virtual users.
var testUsers = []struct {
	UserID string
	Email  string
	Tier   Tier
}{
	{
		UserID: uuid.NewString(),
		Email:  "free_user@test.com",
		Tier:   Tier{Name: "free", RequestsPerMinute: 10, ThrottleAtPercent: 70},
	},
	{
		UserID: uuid.NewString(),
		Email:  "premium_user@test.com",
		Tier:   Tier{Name: "premium", RequestsPerMinute: 100, ThrottleAtPercent: 75},
	},
	{
		UserID: uuid.NewString(),
		Email:  "enterprise_user@test.com",
		Tier:   Tier{Name: "enterprise", RequestsPerMinute: 1000, ThrottleAtPercent: 80},
	},
}

func main() {
	_ = godotenv.Load("api-gateway/.env")

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		log.Fatal("JWT_SECRET not set — ensure api-gateway/.env is present")
	}

	expiry := 24 * time.Hour
	now := time.Now()

	type tokenOutput struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Tier   string `json:"tier"`
		RPM    int    `json:"requests_per_minute"`
		Token  string `json:"token"`
	}

	var output []tokenOutput

	for _, u := range testUsers {
		claims := Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   u.UserID,
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
				Issuer:    "api-gateway",
			},
			UserID: u.UserID,
			Email:  u.Email,
			Tier:   u.Tier,
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, err := token.SignedString([]byte(secret))
		if err != nil {
			log.Fatalf("failed to sign token for %s: %v", u.Email, err)
		}

		output = append(output, tokenOutput{
			UserID: u.UserID,
			Email:  u.Email,
			Tier:   u.Tier.Name,
			RPM:    u.Tier.RequestsPerMinute,
			Token:  signed,
		})

		fmt.Fprintf(os.Stderr, "generated token for %s (%s tier)\n", u.Email, u.Tier.Name)
	}

	// Write JSON to stdout so it can be piped to a file
	// e.g.: go run ./scripts/generate_tokens.go > scripts/tokens.json
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		log.Fatalf("failed to encode output: %v", err)
	}
}