package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/georgepstaylor/prove/internal/events"
	"github.com/georgepstaylor/prove/internal/metrics"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordingProcessor captures the events handed to it for assertions.
type recordingProcessor struct {
	done   chan struct{}
	events []any
}

func newRecordingProcessor() *recordingProcessor {
	return &recordingProcessor{done: make(chan struct{}, 8)}
}

func (r *recordingProcessor) Process(_ context.Context, _ string, event any) error {
	r.events = append(r.events, event)
	r.done <- struct{}{}
	return nil
}

func newTestServer(cfg Config, p events.Processor) *Server {
	if p == nil {
		p = events.NewLogging(discardLogger())
	}
	return New(cfg, discardLogger(), p, metrics.New())
}

func do(s *Server, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	if rec := do(newTestServer(Config{}, nil), http.MethodGet, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyz(t *testing.T) {
	if rec := do(newTestServer(Config{}, nil), http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("readyz: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	rec := do(newTestServer(Config{}, nil), http.MethodGet, "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: got %d, want %d", rec.Code, http.StatusOK)
	}
}
