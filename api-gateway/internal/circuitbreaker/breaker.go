package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"api-gateway/internal/metrics"

	"go.uber.org/zap"
)

// State represents the circuit breaker's current operating mode.
type State int32

const (
	// StateClosed — normal operation. Requests flow through.
	// Failure counter increments on each backend error.
	StateClosed State = 0

	// StateOpen — backend is considered down. All requests fail fast
	// with 503 without touching the backend. Recovery timer is running.
	StateOpen State = 1

	// StateHalfOpen — recovery timer expired. One probe request is allowed
	// through. Success → Closed. Failure → Open again.
	StateHalfOpen State = 2
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Sentinel errors returned to the middleware layer.
var (
	ErrCircuitOpen    = errors.New("circuit breaker is open — backend unavailable")
	ErrProbeInFlight  = errors.New("circuit breaker is half-open — probe already in flight")
)

// Breaker is a thread-safe state machine that protects the gateway from
// cascading failures when the backend becomes unhealthy.
//
// All state transitions use atomic operations — no mutex on the hot path.
// The only mutex usage is for the failure counter reset on state transition,
// which happens rarely and is acceptable.
type Breaker struct {
	backendName      string
	failureThreshold int32
	recoveryDuration time.Duration
	log              *zap.Logger

	// state is read and written atomically on every request.
	state int32

	// consecutiveFailures counts failures since last successful request.
	// Reset to 0 on any successful backend response.
	consecutiveFailures int32

	// openedAt records when the breaker last transitioned to Open.
	// Used to compute when to transition to HalfOpen.
	openedAt atomic.Int64

	// probeInFlight prevents multiple concurrent probe requests in HalfOpen.
	// Only the first goroutine to reach HalfOpen gets to probe.
	probeInFlight atomic.Bool
}

// New constructs a Breaker starting in the Closed state.
func New(
	backendName string,
	failureThreshold int,
	recoveryDuration time.Duration,
	log *zap.Logger,
) *Breaker {
	b := &Breaker{
		backendName:      backendName,
		failureThreshold: int32(failureThreshold),
		recoveryDuration: recoveryDuration,
		log:              log,
	}

	// Initialise Prometheus gauge to 0 (closed) at startup.
	metrics.CircuitBreakerState.WithLabelValues(backendName).Set(0)
	return b
}

// Allow returns nil if the request should be forwarded to the backend.
// Returns ErrCircuitOpen if the breaker is Open and the recovery timer
// has not elapsed. Returns ErrProbeInFlight if HalfOpen and a probe
// is already running.
//
// This is called on every request — it must be fast.
func (b *Breaker) Allow() error {
	state := State(atomic.LoadInt32(&b.state))

	switch state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if recovery window has elapsed.
		openedAt := b.openedAt.Load()
		elapsed := time.Since(time.UnixMilli(openedAt))
		if elapsed < b.recoveryDuration {
			return ErrCircuitOpen
		}
		// Recovery window elapsed — transition to HalfOpen.
		b.transitionTo(StateOpen, StateHalfOpen)
		// Allow the first probe request through.
		if b.probeInFlight.CompareAndSwap(false, true) {
			return nil
		}
		return ErrProbeInFlight

	case StateHalfOpen:
		if b.probeInFlight.CompareAndSwap(false, true) {
			return nil
		}
		return ErrProbeInFlight

	default:
		return nil
	}
}

// RecordSuccess is called after a successful backend response.
// In Closed state it resets the failure counter.
// In HalfOpen state it transitions back to Closed — backend has recovered.
func (b *Breaker) RecordSuccess() {
	state := State(atomic.LoadInt32(&b.state))

	switch state {
	case StateClosed:
		atomic.StoreInt32(&b.consecutiveFailures, 0)

	case StateHalfOpen:
		b.log.Info("circuit breaker: probe succeeded — transitioning to closed",
			zap.String("backend", b.backendName),
		)
		atomic.StoreInt32(&b.consecutiveFailures, 0)
		b.probeInFlight.Store(false)
		b.transitionTo(StateHalfOpen, StateClosed)
	}
}

// RecordFailure is called after a backend error or timeout.
// In Closed state it increments the failure counter and trips the breaker
// when the threshold is reached.
// In HalfOpen state it immediately transitions back to Open — the backend
// is still not healthy.
func (b *Breaker) RecordFailure() {
	state := State(atomic.LoadInt32(&b.state))

	switch state {
	case StateClosed:
		failures := atomic.AddInt32(&b.consecutiveFailures, 1)
		if failures >= b.failureThreshold {
			b.log.Warn("circuit breaker: failure threshold reached — opening",
				zap.String("backend", b.backendName),
				zap.Int32("failures", failures),
				zap.Int32("threshold", b.failureThreshold),
			)
			b.openedAt.Store(time.Now().UnixMilli())
			b.transitionTo(StateClosed, StateOpen)
		}

	case StateHalfOpen:
		b.log.Warn("circuit breaker: probe failed — reopening",
			zap.String("backend", b.backendName),
		)
		b.probeInFlight.Store(false)
		b.openedAt.Store(time.Now().UnixMilli())
		b.transitionTo(StateHalfOpen, StateOpen)
	}
}

// State returns the current breaker state for health checks and admin endpoints.
func (b *Breaker) CurrentState() State {
	return State(atomic.LoadInt32(&b.state))
}

// RetryAfter returns how long the caller should wait before retrying.
// Returns 0 if the breaker is not Open.
func (b *Breaker) RetryAfter() time.Duration {
	if State(atomic.LoadInt32(&b.state)) != StateOpen {
		return 0
	}
	openedAt := time.UnixMilli(b.openedAt.Load())
	remaining := b.recoveryDuration - time.Since(openedAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// transitionTo atomically moves the breaker from one state to another.
// If the current state does not match fromState, the transition is a no-op —
// this prevents two concurrent goroutines from both tripping the breaker.
func (b *Breaker) transitionTo(from, to State) {
	if !atomic.CompareAndSwapInt32(&b.state, int32(from), int32(to)) {
		return
	}

	b.log.Info("circuit breaker state transition",
		zap.String("backend", b.backendName),
		zap.String("from", from.String()),
		zap.String("to", to.String()),
	)

	// Update Prometheus gauge so Grafana shows the state change immediately.
	metrics.CircuitBreakerState.WithLabelValues(b.backendName).Set(float64(to))
	metrics.CircuitBreakerTransitions.WithLabelValues(
		b.backendName, from.String(), to.String(),
	).Inc()
}

// statusFloat converts the current state to the float64 value used by
// the Prometheus gauge: 0=closed, 1=open, 2=half_open.
func (b *Breaker) statusFloat() float64 {
	return float64(atomic.LoadInt32(&b.state))
}

// contextKey for storing breaker results in request context.
type breakerKey struct{}

// StoreResult stores the breaker allow/deny result in the request context
// so the proxy layer can call RecordSuccess or RecordFailure correctly.
func StoreResult(ctx context.Context, allowed bool) context.Context {
	return context.WithValue(ctx, breakerKey{}, allowed)
}

// GetResult retrieves whether the breaker allowed this request.
func GetResult(ctx context.Context) bool {
	v, _ := ctx.Value(breakerKey{}).(bool)
	return v
}

// FormatRetryAfter returns the Retry-After header value in seconds.
func FormatRetryAfter(d time.Duration) string {
	seconds := int(d.Seconds()) + 1
	return fmt.Sprintf("%d", seconds)
}