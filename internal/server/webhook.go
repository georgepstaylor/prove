package server

import (
	"context"
	"net/http"
	"time"

	"github.com/google/go-github/v78/github"
	"github.com/labstack/echo/v4"
)

// processTimeout bounds how long a single delivery may take to process before
// its context is cancelled.
const processTimeout = 30 * time.Second

// handleWebhook verifies the delivery signature, parses the event, acknowledges
// immediately (GitHub expects a response within ~10s), and processes the event
// asynchronously.
func (s *Server) handleWebhook(c echo.Context) error {
	req := c.Request()

	// ValidatePayload checks the X-Hub-Signature-256 HMAC in constant time and
	// returns the raw body on success. This must happen before any work.
	payload, err := github.ValidatePayload(req, []byte(s.cfg.WebhookSecret))
	if err != nil {
		s.logger.Warn("webhook signature validation failed", "err", err)
		s.metrics.IncInvalidSignature()
		return c.NoContent(http.StatusUnauthorized)
	}

	eventType := github.WebHookType(req)
	delivery := github.DeliveryID(req)

	event, err := github.ParseWebHook(eventType, payload)
	if err != nil {
		s.logger.Warn("webhook parse failed", "type", eventType, "delivery", delivery, "err", err)
		return c.NoContent(http.StatusBadRequest)
	}
	s.metrics.IncEvent(eventType, webhookAction(event))

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
		defer cancel()
		if err := s.processor.Process(ctx, delivery, event); err != nil {
			s.logger.Error("event processing failed",
				"type", eventType, "delivery", delivery, "err", err)
		}
	}()

	return c.NoContent(http.StatusAccepted)
}

// webhookAction extracts the action from the events prove cares about (bounded
// label values only — never repo/PR identifiers).
func webhookAction(event any) string {
	switch e := event.(type) {
	case *github.PullRequestEvent:
		return e.GetAction()
	case *github.PullRequestReviewEvent:
		return e.GetAction()
	default:
		return "other"
	}
}
