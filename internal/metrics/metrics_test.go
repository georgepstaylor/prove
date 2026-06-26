package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCountersIncrement(t *testing.T) {
	m := New()
	m.IncDecision("approved")
	m.IncDecision("approved")
	m.IncCache("config", "hit")

	if got := testutil.ToFloat64(m.decisions.WithLabelValues("approved")); got != 2 {
		t.Errorf("decisions approved = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.cache.WithLabelValues("config", "hit")); got != 1 {
		t.Errorf("cache config/hit = %v, want 1", got)
	}
}

func TestNilSafe(t *testing.T) {
	var m *Metrics // nil
	// none of these should panic
	m.IncEvent("pull_request", "opened")
	m.IncDecision("approved")
	m.IncAction("approved")
	m.IncCache("team", "miss")
	m.IncError("rate_limited")
	m.IncInvalidSignature()
	m.ObserveEvaluation(0)
}

func TestHandlerServesMetrics(t *testing.T) {
	m := New()
	m.IncDecision("rejected")
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "prove_decisions_total") {
		t.Error("body missing prove_decisions_total")
	}
}
