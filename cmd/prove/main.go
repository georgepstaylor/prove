// Command prove runs the GitHub App webhook server that auto-approves pull
// requests when every changed file falls under a path the author is allowed to
// touch.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/georgepstaylor/prove/internal/events"
	"github.com/georgepstaylor/prove/internal/githubapp"
	"github.com/georgepstaylor/prove/internal/logging"
	"github.com/georgepstaylor/prove/internal/metrics"
	"github.com/georgepstaylor/prove/internal/server"
)

func main() {
	logger := logging.New()
	slog.SetDefault(logger)

	cfg := server.Config{
		Addr:          envOr("PROVE_ADDR", ":8080"),
		WebhookSecret: os.Getenv("PROVE_WEBHOOK_SECRET"),
	}

	// Fail fast on misconfiguration: an empty secret makes every webhook
	// signature validate, which would let anyone trigger approvals.
	if cfg.WebhookSecret == "" {
		fatal(logger, "PROVE_WEBHOOK_SECRET is required")
	}

	app, err := buildApp(logger)
	if err != nil {
		fatal(logger, "GitHub App configuration error", "err", err)
	}

	m := metrics.New()
	app.OnPrimaryRateLimit(func() { m.IncError("rate_limited") })

	processor := events.NewApprover(ghFactory{app: app}, logger, m)
	srv := server.New(cfg, logger, processor, m)

	go func() {
		logger.Info("prove listening", "addr", cfg.Addr)
		if err := srv.Start(cfg.Addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(logger, "server failed", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}

// buildApp constructs the GitHub App from the environment. The private key may be
// supplied inline (PROVE_PRIVATE_KEY) or via a mounted file (PROVE_PRIVATE_KEY_FILE).
func buildApp(logger *slog.Logger) (*githubapp.App, error) {
	appIDStr := os.Getenv("PROVE_APP_ID")
	if appIDStr == "" {
		return nil, errors.New("PROVE_APP_ID is required")
	}
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return nil, errors.New("PROVE_APP_ID must be an integer")
	}

	key := []byte(os.Getenv("PROVE_PRIVATE_KEY"))
	if len(key) == 0 {
		path := os.Getenv("PROVE_PRIVATE_KEY_FILE")
		if path == "" {
			return nil, errors.New("PROVE_PRIVATE_KEY or PROVE_PRIVATE_KEY_FILE is required")
		}
		key, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	return githubapp.New(appID, key)
}

// ghFactory adapts *githubapp.App to events.ClientFactory: the concrete
// *RepoClient satisfies events.RepoService.
type ghFactory struct{ app *githubapp.App }

func (f ghFactory) InstallationClient(installationID int64) (events.RepoService, error) {
	return f.app.InstallationClient(installationID)
}

func (f ghFactory) BotLogin(ctx context.Context) (string, error) {
	return f.app.BotLogin(ctx)
}

func fatal(logger *slog.Logger, msg string, args ...any) {
	logger.Error(msg, args...)
	os.Exit(1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
