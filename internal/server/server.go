// Package server wires the HTTP routes for the prove GitHub App.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/georgepstaylor/prove/internal/events"
	"github.com/georgepstaylor/prove/internal/metrics"
)

// Config holds runtime configuration sourced from the environment.
type Config struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// WebhookSecret is the shared secret GitHub uses to sign webhook deliveries.
	WebhookSecret string
}

// Server owns the HTTP server and the event processor, and tracks in-flight
// asynchronous event processing so shutdown can drain it.
type Server struct {
	cfg       Config
	logger    *slog.Logger
	processor events.Processor
	metrics   *metrics.Metrics
	e         *echo.Echo
	wg        sync.WaitGroup
}

// New builds a Server with all prove routes registered. metrics may be nil.
func New(cfg Config, logger *slog.Logger, processor events.Processor, m *metrics.Metrics) *Server {
	s := &Server{cfg: cfg, logger: logger, processor: processor, metrics: m}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())

	e.GET("/healthz", s.handleHealth)
	e.GET("/readyz", s.handleReady)
	e.POST("/webhook", s.handleWebhook)
	if m != nil {
		// In-cluster only — the k8s Ingress routes /webhook, not /metrics.
		e.GET("/metrics", echo.WrapHandler(m.Handler()))
	}

	s.e = e
	return s
}

// Start runs the HTTP server until shutdown; it returns http.ErrServerClosed on
// graceful stop.
func (s *Server) Start(addr string) error { return s.e.Start(addr) }

// Shutdown stops accepting connections and waits for in-flight event processing
// to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.e.Shutdown(ctx)
	s.wg.Wait()
	return err
}

// ServeHTTP lets the Server be used directly in tests via httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.e.ServeHTTP(w, r) }

// handleHealth is a liveness probe: the process is up.
func (s *Server) handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady is a readiness probe: the process can serve traffic.
func (s *Server) handleReady(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ready"})
}
