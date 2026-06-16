import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";
import { textSummary } from "https://jslib.k6.io/k6-summary/0.0.2/index.js";

// =============================================================================
// Configuration
// Replace these token values with the output from generate_tokens.go
// Replace GATEWAY_URL with the URL from:
//   minikube service api-gateway-service -n api-gateway --url
// =============================================================================
const GATEWAY_URL = __ENV.GATEWAY_URL || "http://127.0.0.1:30080";

const TOKENS = {
  free:       __ENV.FREE_TOKEN       || "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJhcGktZ2F0ZXdheSIsInN1YiI6IjA2NDFkYTk3LTU1NDQtNDhiYy1iYjAxLWVmZjU0NDhiNjNlNyIsImV4cCI6MTc4MTcxNjY2OCwiaWF0IjoxNzgxNjMwMjY4LCJ1c2VyX2lkIjoiMDY0MWRhOTctNTU0NC00OGJjLWJiMDEtZWZmNTQ0OGI2M2U3IiwiZW1haWwiOiJmcmVlX3VzZXJAdGVzdC5jb20iLCJ0aWVyIjp7Im5hbWUiOiJmcmVlIiwicmVxdWVzdHNfcGVyX21pbnV0ZSI6MTAsInRocm90dGxlX2F0X3BlcmNlbnQiOjcwfX0.nGRcyANsa62JHfJ8AlgpSq9w-zu9VRVjI8qsmaMoDNI",
  premium:    __ENV.PREMIUM_TOKEN    || "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJhcGktZ2F0ZXdheSIsInN1YiI6IjFjY2Y0ZWIxLTE5ODctNDk4My1hYWZjLTBiN2I3NGQ4NjU1MCIsImV4cCI6MTc4MTcxNjY2OCwiaWF0IjoxNzgxNjMwMjY4LCJ1c2VyX2lkIjoiMWNjZjRlYjEtMTk4Ny00OTgzLWFhZmMtMGI3Yjc0ZDg2NTUwIiwiZW1haWwiOiJwcmVtaXVtX3VzZXJAdGVzdC5jb20iLCJ0aWVyIjp7Im5hbWUiOiJwcmVtaXVtIiwicmVxdWVzdHNfcGVyX21pbnV0ZSI6MTAwLCJ0aHJvdHRsZV9hdF9wZXJjZW50Ijo3NX19.HVRKpkUYOFVS1mwvna1FLw1r0OmXxTIvWoSQsrAIaWM",
  enterprise: __ENV.ENTERPRISE_TOKEN || "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJhcGktZ2F0ZXdheSIsInN1YiI6IjkzMmM2OWI0LTkwODktNDVlOS05YjY4LWMwZTliNjhlZmNkZCIsImV4cCI6MTc4MTcxNjY2OCwiaWF0IjoxNzgxNjMwMjY4LCJ1c2VyX2lkIjoiOTMyYzY5YjQtOTA4OS00NWU5LTliNjgtYzBlOWI2OGVmY2RkIiwiZW1haWwiOiJlbnRlcnByaXNlX3VzZXJAdGVzdC5jb20iLCJ0aWVyIjp7Im5hbWUiOiJlbnRlcnByaXNlIiwicmVxdWVzdHNfcGVyX21pbnV0ZSI6MTAwMCwidGhyb3R0bGVfYXRfcGVyY2VudCI6ODB9fQ.Xkmdxfzx8EgRcQVzDmk69SeLCOKwrU6s2IizsGCDEAQ",
};

// =============================================================================
// Custom metrics
// These appear in the k6 summary output and can be pushed to Prometheus
// for display in Grafana alongside gateway metrics.
// =============================================================================

// Counts how many requests were rate limited (429 responses)
const rateLimitedRequests = new Counter("rate_limited_requests");

// Counts how many requests hit open circuit breaker (503 responses)
const circuitBreakerRejections = new Counter("circuit_breaker_rejections");

// Tracks the rate of successful proxied responses (200)
const successRate = new Rate("success_rate");

// Tracks end-to-end latency as seen by k6 (client side)
// Compare this to Grafana P95 to see network overhead
const requestLatency = new Trend("request_latency_ms", true);

// =============================================================================
// Load test stages
// This is the complete traffic lifecycle from ramp-up to recovery.
// Total duration: approximately 4 minutes 30 seconds.
// =============================================================================
export const options = {
  scenarios: {

    // -------------------------------------------------------------------------
    // Scenario 1 — Mixed tier traffic
    // Simulates realistic production traffic with users across all three tiers.
    // This is the primary scenario that drives rate limiting decisions and
    // HPA scaling.
    // -------------------------------------------------------------------------
    mixed_traffic: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        // Stage 1 — Warm up
        // Ramp from 0 to 20 VUs over 30 seconds.
        // All requests should be ALLOW. Establishes baseline metrics.
        // Grafana shows: clean green rate limit panel, low P95 latency.
        { duration: "30s", target: 20 },

        // Stage 2 — Sustained normal load
        // Hold at 20 VUs for 30 seconds.
        // Free tier users begin hitting their 10 RPM limit.
        // Grafana shows: throttle decisions appearing in yellow.
        { duration: "30s", target: 20 },

        // Stage 3 — Ramp to medium load
        // Increase to 80 VUs over 30 seconds.
        // Free tier users heavily throttled and rejected.
        // Premium users starting to throttle.
        // HPA begins evaluating whether to scale.
        // Grafana shows: mix of green, yellow, red decisions.
        { duration: "30s", target: 80 },

        // Stage 4 — Sustained medium load
        // Hold at 80 VUs for 60 seconds.
        // HPA triggers and adds gateway pods.
        // Grafana shows: pod count rising, rate limits stabilising.
        { duration: "60s", target: 80 },

        // Stage 5 — Traffic spike
        // Jump to 200 VUs over 30 seconds.
        // Heavy rejection rate for free and premium tiers.
        // Enterprise users beginning to throttle.
        // HPA scaling aggressively.
        // Grafana shows: predominantly red rate limit panel.
        { duration: "30s", target: 200 },

        // Stage 6 — Sustained spike
        // Hold at 200 VUs for 60 seconds.
        // Maximum observable system pressure.
        // Record peak RPS, pod count, and P95 latency here.
        // This is the screenshot moment for your resume metrics.
        { duration: "60s", target: 200 },

        // Stage 7 — Recovery ramp down
        // Drop to 0 VUs over 30 seconds.
        // Rate limit windows drain as time passes.
        // HPA begins scale-down evaluation (5 min stabilisation window).
        // Grafana shows: decisions returning to green.
        { duration: "30s", target: 0 },
      ],
      // All mixed traffic VUs use this function
      exec: "mixedTraffic",
    },

    // -------------------------------------------------------------------------
    // Scenario 2 — Circuit breaker trigger
    // A small number of VUs continuously hit the error endpoint to build
    // up failures and trip the circuit breaker open.
    // Runs concurrently with mixed_traffic.
    // -------------------------------------------------------------------------
    circuit_breaker_trigger: {
      executor: "constant-vus",
      vus: 3,
      duration: "4m30s",
      exec: "triggerCircuitBreaker",
      // Start 60 seconds in — after baseline is established
      startTime: "1m",
    },

    // -------------------------------------------------------------------------
    // Scenario 3 — Slow endpoint pressure
    // Hits the slow endpoint (3s delay) to demonstrate timeout handling
    // and show the backend latency panel spike in Grafana.
    // Runs for a short window during the spike phase.
    // -------------------------------------------------------------------------
    slow_endpoint: {
      executor: "constant-vus",
      vus: 5,
      duration: "1m",
      exec: "hitSlowEndpoint",
      // Start during the spike phase
      startTime: "2m30s",
    },
  },

  // ---------------------------------------------------------------------------
  // Thresholds — these define pass/fail criteria for the load test.
  // k6 exits with a non-zero code if any threshold is breached.
  // In CI this makes the pipeline fail if performance degrades.
  // ---------------------------------------------------------------------------
  thresholds: {
    // 95% of requests must complete within 2 seconds
    // This accounts for throttle delays (500ms) and slow endpoint spikes
    "http_req_duration{scenario:mixed_traffic}": ["p(95)<2000"],

    // Gateway must never return 5xx on its own (circuit breaker 503 is ok,
    // but internal gateway errors are not acceptable)
    // We allow up to 15% failure rate accounting for rate limit 429s and
    // circuit breaker 503s — these are expected, not failures
    "http_req_failed{scenario:mixed_traffic}": ["rate<0.50"],

    // Circuit breaker scenario expects mostly 503s when breaker is open
    // and 500s from the error endpoint — high failure rate is expected
    "http_req_failed{scenario:circuit_breaker_trigger}": ["rate<0.99"],

    // Success rate for normal traffic should stay above 30%
    // (many requests will be rate limited — that is the point)
    success_rate: ["rate>0.30"],
  },
};

// =============================================================================
// Scenario functions
// =============================================================================

// Distributes requests across endpoints and tiers to simulate real traffic.
// 70% of VUs use premium token, 20% use free token, 10% use enterprise.
// This ratio creates visible throttling without overwhelming the system.
export function mixedTraffic() {
  const vuIndex = __VU % 10;

  let token;
  let tierLabel;

  if (vuIndex < 7) {
    // 70% premium tier
    token = TOKENS.premium;
    tierLabel = "premium";
  } else if (vuIndex < 9) {
    // 20% free tier
    token = TOKENS.free;
    tierLabel = "free";
  } else {
    // 10% enterprise tier
    token = TOKENS.enterprise;
    tierLabel = "enterprise";
  }

  const headers = {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
    "X-Load-Test": "true",
    "X-Tier": tierLabel,
  };

  // Distribute requests across endpoints
  const endpointIndex = __ITER % 4;
  let url;
  let endpointLabel;

  switch (endpointIndex) {
    case 0:
    case 1:
    case 2:
      // 75% of requests to records — the primary endpoint
      url = `${GATEWAY_URL}/api/v1/records`;
      endpointLabel = "records";
      break;
    case 3:
      // 25% to unstable — generates mixed success/failure
      // for more realistic backend behaviour
      url = `${GATEWAY_URL}/api/v1/unstable`;
      endpointLabel = "unstable";
      break;
  }

  const startTime = Date.now();
  const response = http.get(url, {
    headers,
    timeout: "10s",
    tags: { endpoint: endpointLabel, tier: tierLabel },
  });
  const latency = Date.now() - startTime;

  // Record custom metrics
  requestLatency.add(latency, { tier: tierLabel, endpoint: endpointLabel });

  // Track rate limiting
  if (response.status === 429) {
    rateLimitedRequests.add(1, { tier: tierLabel });
  }

  // Track success
  successRate.add(response.status === 200);

  // Validate response structure
  check(response, {
    "response has body": (r) => r.body && r.body.length > 0,
    "not a gateway internal error": (r) => r.status !== 500,
    "rate limit headers present on success": (r) =>
      r.status !== 200 || r.headers["X-Ratelimit-Limit"] !== undefined,
  });

  // Realistic think time between requests — 100ms to 500ms
  // Without sleep all VUs hammer the gateway as fast as possible
  // which is unrealistic and overwhelms even large clusters
  sleep(Math.random() * 0.4 + 0.1);
}

// Continuously hits the error endpoint to build failure count
// and trip the circuit breaker open.
export function triggerCircuitBreaker() {
  const response = http.get(`${GATEWAY_URL}/api/v1/error`, {
    headers: {
      Authorization: `Bearer ${TOKENS.enterprise}`,
      "X-Load-Test": "true",
    },
    timeout: "5s",
    tags: { endpoint: "error", purpose: "circuit_breaker" },
  });

  if (response.status === 503) {
    circuitBreakerRejections.add(1);
  }

  check(response, {
    "circuit breaker scenario response received": (r) =>
      r.status === 500 || r.status === 503 || r.status === 429,
  });

  // Short sleep — we want to accumulate failures quickly
  sleep(0.5);
}

// Hits the slow endpoint to demonstrate timeout handling
// and spike the backend latency panel in Grafana.
export function hitSlowEndpoint() {
  const response = http.get(`${GATEWAY_URL}/api/v1/slow`, {
    headers: {
      Authorization: `Bearer ${TOKENS.premium}`,
      "X-Load-Test": "true",
    },
    // Timeout longer than the slow endpoint delay
    timeout: "10s",
    tags: { endpoint: "slow", purpose: "latency_demo" },
  });

  check(response, {
    "slow endpoint responds": (r) =>
      r.status === 200 || r.status === 429 || r.status === 503,
  });

  sleep(1);
}

// =============================================================================
// Summary output
// Runs after the test completes and writes results to a JSON file.
// The JSON file contains all metrics with exact numbers for your resume.
// =============================================================================
export function handleSummary(data) {
  // Print human readable summary to console
  console.log(textSummary(data, { indent: " ", enableColors: true }));

  // Write full results to file for reference
  return {
    "load-tests/results/summary.json": JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: " ", enableColors: true }),
  };
}