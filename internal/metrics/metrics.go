// Package metrics exposes prove's Prometheus instrumentation behind a small,
// nil-safe typed API so call sites never touch Prometheus directly.
//
// Cardinality guardrail: every label set here is strictly bounded
// ({event, action, result, cache, stage}). Never add repo/owner/PR/author as a
// label — per-entity detail belongs in the structured decision log, not metrics.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds prove's collectors on a private registry.
type Metrics struct {
	reg          *prometheus.Registry
	events       *prometheus.CounterVec // {event, action}
	invalidSig   prometheus.Counter
	decisions    *prometheus.CounterVec // {result}
	actions      *prometheus.CounterVec // {action}
	cache        *prometheus.CounterVec // {cache, result}
	errors       *prometheus.CounterVec // {stage}
	evalDuration prometheus.Histogram
}

// New builds the registry and registers all collectors.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prove_webhook_events_total", Help: "Webhook events received, by type and action.",
		}, []string{"event", "action"}),
		invalidSig: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prove_webhook_invalid_signature_total", Help: "Webhook deliveries rejected for a bad signature.",
		}),
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prove_decisions_total", Help: "Evaluation outcomes.",
		}, []string{"result"}),
		actions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prove_actions_total", Help: "Actions taken on PRs.",
		}, []string{"action"}),
		cache: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prove_cache_total", Help: "Cache lookups, by cache and hit/miss.",
		}, []string{"cache", "result"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prove_errors_total", Help: "Errors by processing stage.",
		}, []string{"stage"}),
		evalDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "prove_evaluation_duration_seconds", Help: "Per-event evaluation wall time.",
			Buckets: prometheus.DefBuckets,
		}),
	}
	m.reg.MustRegister(
		m.events, m.invalidSig, m.decisions, m.actions, m.cache, m.errors, m.evalDuration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves the metrics in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// All increment methods are nil-safe so callers (and tests) can pass a nil *Metrics.

func (m *Metrics) IncEvent(event, action string) {
	if m != nil {
		m.events.WithLabelValues(event, action).Inc()
	}
}

func (m *Metrics) IncInvalidSignature() {
	if m != nil {
		m.invalidSig.Inc()
	}
}

func (m *Metrics) IncDecision(result string) {
	if m != nil {
		m.decisions.WithLabelValues(result).Inc()
	}
}

func (m *Metrics) IncAction(action string) {
	if m != nil {
		m.actions.WithLabelValues(action).Inc()
	}
}

func (m *Metrics) IncCache(name, result string) {
	if m != nil {
		m.cache.WithLabelValues(name, result).Inc()
	}
}

func (m *Metrics) IncError(stage string) {
	if m != nil {
		m.errors.WithLabelValues(stage).Inc()
	}
}

func (m *Metrics) ObserveEvaluation(d time.Duration) {
	if m != nil {
		m.evalDuration.Observe(d.Seconds())
	}
}
