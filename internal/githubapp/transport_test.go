package githubapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(code int) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
}

func TestRetryTransportRetriesGet5xx(t *testing.T) {
	var calls int32
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		if atomic.AddInt32(&calls, 1) < 3 {
			return resp(503), nil
		}
		return resp(200), nil
	})
	rt := &retryTransport{base: base, maxRetries: 3, baseDelay: time.Millisecond}
	req, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
	r, err := rt.RoundTrip(req)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("got (%v, %v), want 200", r, err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryTransportDoesNotRetryWrites(t *testing.T) {
	var calls int32
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return resp(503), nil
	})
	rt := &retryTransport{base: base, maxRetries: 3, baseDelay: time.Millisecond}
	req, _ := http.NewRequest(http.MethodPost, "http://x/", strings.NewReader("body"))
	r, _ := rt.RoundTrip(req)
	if r.StatusCode != 503 || calls != 1 {
		t.Fatalf("writes must not retry: status=%d calls=%d", r.StatusCode, calls)
	}
}

func TestRetryTransportFailsFastOnError(t *testing.T) {
	var calls int32
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("primary rate limit reached")
	})
	rt := &retryTransport{base: base, maxRetries: 3, baseDelay: time.Millisecond}
	req, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Fatalf("must not retry on transport error: calls=%d", calls)
	}
}

func TestRetryTransportRespectsContext(t *testing.T) {
	base := roundTripFunc(func(*http.Request) (*http.Response, error) { return resp(503), nil })
	rt := &retryTransport{base: base, maxRetries: 10, baseDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/", nil)

	errc := make(chan error, 1)
	go func() { _, err := rt.RoundTrip(req); errc <- err }()
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("want context error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not return on context cancel")
	}
}

func TestWrapTransportPassThroughAndPrimaryObserver(t *testing.T) {
	base := roundTripFunc(func(*http.Request) (*http.Response, error) { return resp(200), nil })
	rt := wrapTransport(base, func() {}) // observer non-nil but not triggered on success
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	r, err := rt.RoundTrip(req)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("pass-through failed: got (%v, %v)", r, err)
	}
}
