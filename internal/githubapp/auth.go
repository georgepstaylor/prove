// Package githubapp authenticates as the prove GitHub App and exposes typed
// clients scoped to a single installation. Installation access tokens are
// cached in memory by ghinstallation — no external token store required.
package githubapp

import (
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v78/github"
)

// App holds the credentials needed to mint per-installation clients. The private
// key is GitHub's native PEM (PKCS#1); ghinstallation parses it without PKCS#8
// conversion.
type App struct {
	appID         int64
	privateKey    []byte
	transport     http.RoundTripper
	onPrimaryRate RateLimitObserver
}

// OnPrimaryRateLimit registers a callback invoked when a primary rate limit drops
// a request (used to feed a metric). Optional; nil-safe.
func (a *App) OnPrimaryRateLimit(fn RateLimitObserver) { a.onPrimaryRate = fn }

// New validates the private key and returns an App, failing fast on a malformed
// key so misconfiguration surfaces at startup rather than on the first webhook.
func New(appID int64, privateKeyPEM []byte) (*App, error) {
	// NewAppsTransport parses the key and errors if it is invalid.
	if _, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, privateKeyPEM); err != nil {
		return nil, fmt.Errorf("invalid GitHub App private key: %w", err)
	}
	return &App{appID: appID, privateKey: privateKeyPEM, transport: http.DefaultTransport}, nil
}

// InstallationClient returns a RepoClient authenticated as the given
// installation. The transport refreshes and caches installation tokens itself.
func (a *App) InstallationClient(installationID int64) (*RepoClient, error) {
	itr, err := ghinstallation.New(a.transport, a.appID, installationID, a.privateKey)
	if err != nil {
		return nil, fmt.Errorf("installation transport: %w", err)
	}
	rt := wrapTransport(itr, a.onPrimaryRate)
	hc := &http.Client{Transport: rt}
	return &RepoClient{gh: github.NewClient(hc), http: hc}, nil
}
