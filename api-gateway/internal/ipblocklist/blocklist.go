package ipblocklist

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"api-gateway/internal/repository"

	"go.uber.org/zap"
)

// Blocklist manages the IP ban list backed by Redis.
// It is intentionally simple — the complexity lives in the middleware
// that calls it, not here. This struct only knows about Redis operations.
type Blocklist struct {
	repo repository.CacheRepository
	log  *zap.Logger
}

// New constructs the Blocklist with its Redis repository dependency.
func New(repo repository.CacheRepository, log *zap.Logger) *Blocklist {
	return &Blocklist{repo: repo, log: log}
}

// IsBlocked returns true if the given IP address is currently banned.
// The IP is extracted and normalised before the Redis lookup to ensure
// "192.168.1.1:54321" and "192.168.1.1" resolve to the same ban entry.
func (b *Blocklist) IsBlocked(ctx context.Context, ip string) (bool, error) {
	normalised := normaliseIP(ip)
	blocked, err := b.repo.IsIPBlocked(ctx, normalised)
	if err != nil {
		// Log but do not block on Redis errors — same fail-open policy
		// as the rate limiter. A Redis outage must not deny all traffic.
		b.log.Error("blocklist redis lookup failed — failing open",
			zap.String("ip", normalised),
			zap.Error(err),
		)
		return false, nil
	}
	return blocked, nil
}

// Block adds an IP to the ban list.
// ttl=0 means permanent ban. Positive ttl means temporary ban.
func (b *Blocklist) Block(ctx context.Context, ip string, ttl time.Duration) error {
	normalised := normaliseIP(ip)
	if err := b.repo.BlockIP(ctx, normalised, ttl); err != nil {
		return fmt.Errorf("blocking ip %s: %w", normalised, err)
	}
	b.log.Info("ip blocked",
		zap.String("ip", normalised),
		zap.Duration("ttl", ttl),
	)
	return nil
}

// Unblock removes an IP from the ban list.
func (b *Blocklist) Unblock(ctx context.Context, ip string) error {
	normalised := normaliseIP(ip)
	if err := b.repo.UnblockIP(ctx, normalised); err != nil {
		return fmt.Errorf("unblocking ip %s: %w", normalised, err)
	}
	b.log.Info("ip unblocked", zap.String("ip", normalised))
	return nil
}

// ListBlocked returns all currently banned IPs.
// Used by the admin handler to render the blocklist in the demo.
func (b *Blocklist) ListBlocked(ctx context.Context) ([]string, error) {
	return b.repo.ListBlockedIPs(ctx)
}

// ExtractIP pulls the client IP from an HTTP request.
// It respects X-Real-IP and X-Forwarded-For headers set by the Kubernetes
// ingress controller so we ban the actual client, not the ingress pod IP.
func ExtractIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return normaliseIP(ip)
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// X-Forwarded-For can be a comma-separated list — leftmost is the client
		for _, part := range splitAndTrim(ip, ",") {
			if part != "" {
				return normaliseIP(part)
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return normaliseIP(host)
}

// normaliseIP strips the port from an address if present and returns
// the bare IP string. Ensures consistent Redis key format.
func normaliseIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port present — addr is already a bare IP
		return addr
	}
	return host
}

func splitAndTrim(s, sep string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i:i+len(sep)] == sep {
			part := trimSpace(s[start:i])
			parts = append(parts, part)
			start = i + len(sep)
		}
	}
	return parts
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}