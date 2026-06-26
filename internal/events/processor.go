// Package events defines the processor that acts on parsed GitHub webhook
// events. The HTTP layer parses and verifies deliveries, then hands the typed
// event to a Processor for the actual approval logic.
package events

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/go-github/v78/github"
)

// Processor handles a parsed, signature-verified webhook event. Implementations
// must be safe for concurrent use: the server invokes Process in its own
// goroutine per delivery.
type Processor interface {
	// Process acts on a single delivery. delivery is GitHub's X-GitHub-Delivery
	// GUID, useful for log correlation. event is one of the github.*Event types
	// returned by github.ParseWebHook.
	Process(ctx context.Context, delivery string, event any) error
}

// Logging is a Processor that records which events it would act on without
// calling GitHub.
type Logging struct {
	logger *slog.Logger
}

// NewLogging returns a Logging processor.
func NewLogging(logger *slog.Logger) *Logging {
	return &Logging{logger: logger}
}

// Process logs a structured summary of the event.
func (l *Logging) Process(_ context.Context, delivery string, event any) error {
	switch e := event.(type) {
	case *github.PullRequestEvent:
		l.logger.Info("pull_request event",
			"delivery", delivery,
			"action", e.GetAction(),
			"repo", e.GetRepo().GetFullName(),
			"number", e.GetNumber(),
			"author", e.GetPullRequest().GetUser().GetLogin(),
		)
	case *github.PullRequestReviewEvent:
		l.logger.Info("pull_request_review event",
			"delivery", delivery,
			"action", e.GetAction(),
			"repo", e.GetRepo().GetFullName(),
			"number", e.GetPullRequest().GetNumber(),
		)
	case *github.InstallationEvent:
		l.logger.Info("installation event",
			"delivery", delivery,
			"action", e.GetAction(),
			"installation", e.GetInstallation().GetID(),
		)
	default:
		l.logger.Debug("ignored event", "delivery", delivery, "type", fmt.Sprintf("%T", event))
	}
	return nil
}
