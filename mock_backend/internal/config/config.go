package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds every runtime knob for the mock backend.
// All values are sourced from environment variables so the same binary
// runs identically in local, Docker, and Kubernetes with no code changes.
type Config struct {
	AppEnv  string
	AppName string
	Port    string

	// Behavioural knobs — used by specific handlers to simulate real-world conditions
	SlowEndpointDelayMs        int
	UnstableEndpointFailureRate int // percentage 0–100
}

// Load reads the .env file (if present) and then reads environment variables.
// Environment variables set in the shell always take precedence over .env values.
// This is the standard 12-factor app configuration pattern.
func Load() (*Config, error) {
	// godotenv.Load is intentionally lenient — if .env is absent (e.g., in a
	// Kubernetes pod where vars are injected directly), we continue normally.
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:  getEnv("APP_ENV", "development"),
		AppName: getEnv("APP_NAME", "mock-backend"),
		Port:    getEnv("APP_PORT", "8081"),
	}

	var err error

	cfg.SlowEndpointDelayMs, err = getEnvInt("SLOW_ENDPOINT_DELAY_MS", 3000)
	if err != nil {
		return nil, fmt.Errorf("invalid SLOW_ENDPOINT_DELAY_MS: %w", err)
	}

	cfg.UnstableEndpointFailureRate, err = getEnvInt("UNSTABLE_ENDPOINT_FAILURE_RATE", 40)
	if err != nil {
		return nil, fmt.Errorf("invalid UNSTABLE_ENDPOINT_FAILURE_RATE: %w", err)
	}

	if cfg.UnstableEndpointFailureRate < 0 || cfg.UnstableEndpointFailureRate > 100 {
		return nil, fmt.Errorf("UNSTABLE_ENDPOINT_FAILURE_RATE must be between 0 and 100, got %d", cfg.UnstableEndpointFailureRate)
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	val := os.Getenv(key)
	if val == "" {
		return fallback, nil
	}
	return strconv.Atoi(val)
}