package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config is the single source of truth for all runtime configuration.
// Every value comes from environment variables — no hardcoded values anywhere
// in the codebase. This is the 12-factor app pattern and is required for
// Kubernetes where config is injected via ConfigMaps and Secrets.
type Config struct {
	// Application
	AppEnv     string
	AppName    string
	AppPort    string
	AppVersion string

	// JWT
	JWTSecret      string
	JWTExpiryHours int

	// Redis — two logical databases on the same instance
	// DB 0: rate limit counters (never evict — use explicit TTL)
	// DB 1: response cache (can evict under memory pressure)
	RedisAddr               string
	RedisPassword           string
	RedisRateLimitDB        int
	RedisCacheDB            int
	RedisDialTimeoutSeconds int
	RedisReadTimeoutSeconds int
	RedisWriteTimeoutSeconds int
	RedisPoolSize     int
	RedisMinIdleConns int

	// Rate Limiting
	RateLimitWindowSeconds int
	ThrottleDelayMs        int

	// Circuit Breaker
	CircuitBreakerFailureThreshold int
	CircuitBreakerRecoverySeconds  int

	// Proxy
	BackendURL      string
	MaxBodySizeBytes int64

	// Admin
	AdminToken string
}

// Load reads .env then environment variables.
// Shell environment always wins over .env — this means Kubernetes-injected
// values automatically override any .env file present in the image.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:     getEnv("APP_ENV", "development"),
		AppName:    getEnv("APP_NAME", "api-gateway"),
		AppPort:    getEnv("APP_PORT", "8080"),
		AppVersion: getEnv("APP_VERSION", "1.0.0"),
		JWTSecret:  getEnv("JWT_SECRET", ""),
		RedisAddr:  getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		BackendURL:    getEnv("BACKEND_URL", "http://localhost:8081"),
		AdminToken:    getEnv("ADMIN_TOKEN", ""),
	}

	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET must be set")
	}
	if cfg.AdminToken == "" {
		return nil, fmt.Errorf("ADMIN_TOKEN must be set")
	}
	if cfg.BackendURL == "" {
		return nil, fmt.Errorf("BACKEND_URL must be set")
	}

	var err error

	cfg.JWTExpiryHours, err = getEnvInt("JWT_EXPIRY_HOURS", 24)
	if err != nil {
		return nil, fmt.Errorf("JWT_EXPIRY_HOURS: %w", err)
	}

	cfg.RedisRateLimitDB, err = getEnvInt("REDIS_RATE_LIMIT_DB", 0)
	if err != nil {
		return nil, fmt.Errorf("REDIS_RATE_LIMIT_DB: %w", err)
	}

	cfg.RedisCacheDB, err = getEnvInt("REDIS_CACHE_DB", 1)
	if err != nil {
		return nil, fmt.Errorf("REDIS_CACHE_DB: %w", err)
	}

	cfg.RedisDialTimeoutSeconds, err = getEnvInt("REDIS_DIAL_TIMEOUT_SECONDS", 5)
	if err != nil {
		return nil, fmt.Errorf("REDIS_DIAL_TIMEOUT_SECONDS: %w", err)
	}

	cfg.RedisReadTimeoutSeconds, err = getEnvInt("REDIS_READ_TIMEOUT_SECONDS", 3)
	if err != nil {
		return nil, fmt.Errorf("REDIS_READ_TIMEOUT_SECONDS: %w", err)
	}

	cfg.RedisWriteTimeoutSeconds, err = getEnvInt("REDIS_WRITE_TIMEOUT_SECONDS", 3)
	if err != nil {
		return nil, fmt.Errorf("REDIS_WRITE_TIMEOUT_SECONDS: %w", err)
	}

	cfg.RateLimitWindowSeconds, err = getEnvInt("RATE_LIMIT_WINDOW_SECONDS", 60)
	if err != nil {
		return nil, fmt.Errorf("RATE_LIMIT_WINDOW_SECONDS: %w", err)
	}

	// Inside the Load() function — add these two blocks
	cfg.RedisPoolSize, err = getEnvInt("REDIS_POOL_SIZE", 0)
	if err != nil {
		return nil, fmt.Errorf("REDIS_POOL_SIZE: %w", err)
	}

	cfg.RedisMinIdleConns, err = getEnvInt("REDIS_MIN_IDLE_CONNS", 5)
	if err != nil {
		return nil, fmt.Errorf("REDIS_MIN_IDLE_CONNS: %w", err)
	}

	cfg.ThrottleDelayMs, err = getEnvInt("THROTTLE_DELAY_MS", 500)
	if err != nil {
		return nil, fmt.Errorf("THROTTLE_DELAY_MS: %w", err)
	}

	cfg.CircuitBreakerFailureThreshold, err = getEnvInt("CIRCUIT_BREAKER_FAILURE_THRESHOLD", 5)
	if err != nil {
		return nil, fmt.Errorf("CIRCUIT_BREAKER_FAILURE_THRESHOLD: %w", err)
	}

	cfg.CircuitBreakerRecoverySeconds, err = getEnvInt("CIRCUIT_BREAKER_RECOVERY_SECONDS", 30)
	if err != nil {
		return nil, fmt.Errorf("CIRCUIT_BREAKER_RECOVERY_SECONDS: %w", err)
	}

	maxBodyBytes, err := getEnvInt("MAX_BODY_SIZE_BYTES", 1048576)
	if err != nil {
		return nil, fmt.Errorf("MAX_BODY_SIZE_BYTES: %w", err)
	}
	cfg.MaxBodySizeBytes = int64(maxBodyBytes)

	return cfg, nil
}

// JWTExpiry returns the token lifetime as a time.Duration.
// Used by the token generation script and by the validator.
func (c *Config) JWTExpiry() time.Duration {
	return time.Duration(c.JWTExpiryHours) * time.Hour
}

// RateLimitWindow returns the sliding window size as a time.Duration.
func (c *Config) RateLimitWindow() time.Duration {
	return time.Duration(c.RateLimitWindowSeconds) * time.Second
}

// CircuitBreakerRecovery returns the recovery timer as a time.Duration.
func (c *Config) CircuitBreakerRecovery() time.Duration {
	return time.Duration(c.CircuitBreakerRecoverySeconds) * time.Second
}

// ThrottleDelay returns the artificial delay as a time.Duration.
func (c *Config) ThrottleDelay() time.Duration {
	return time.Duration(c.ThrottleDelayMs) * time.Millisecond
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	return strconv.Atoi(v)
}