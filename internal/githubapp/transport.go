package githubapp

import (
	"net/http"
	"time"

	"github.com/gofri/go-github-ratelimit/v2/github_ratelimit"
	"github.com/gofri/go-github-ratelimit/v2/github_ratelimit/github_primary_ratelimit"
)

// RateLimitObserver is invoked whenever a primary rate limit causes a request to
// be dropped. Wire it to a metric so exhaustion is alertable.
type RateLimitObserver func()

// wrapTransport layers GitHub rate-limit handling and bounded retry over base:
//   - secondary (abuse) limits → short backoff (gofri default; resets are brief)
//   - primary limit → fail fast (gofri default — no multi-minute sleep) + observer
//   - transient 5xx on idempotent GETs → a few bounded, context-aware retries
func wrapTransport(base http.RoundTripper, onPrimaryLimit RateLimitObserver) http.RoundTripper {
	notify := func(*github_primary_ratelimit.CallbackContext) {
		if onPrimaryLimit != nil {
			onPrimaryLimit()
		}
	}
	rl := github_ratelimit.New(base,
		github_primary_ratelimit.WithLimitDetectedCallback(notify),
		github_primary_ratelimit.WithRequestPreventedCallback(notify),
	)
	return &retryTransport{base: rl, maxRetries: 3, baseDelay: 250 * time.Millisecond}
}

// retryTransport retries transient 5xx responses for idempotent reads only.
// Writes (POST/PATCH/PUT/DELETE) are never retried: their bodies aren't replayed
// and a duplicate could double-apply.
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
	baseDelay  time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return t.base.RoundTrip(req)
	}
	delay := t.baseDelay
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			// Transport / rate-limit-prevented error: fail fast, no retry.
			return resp, err
		}
		if resp.StatusCode < 500 || attempt >= t.maxRetries {
			return resp, nil
		}
		resp.Body.Close()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(delay):
			delay *= 2
		}
	}
}
