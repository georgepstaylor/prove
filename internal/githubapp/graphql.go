package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// graphqlEndpoint is GitHub's GraphQL API. go-github is REST-only, so the few
// GraphQL-only operations (auto-merge) go directly over the installation client.
const graphqlEndpoint = "https://api.github.com/graphql"

// graphql issues a GraphQL request, erroring if the transport fails, the HTTP
// status is non-2xx, or the response carries GraphQL errors.
func (c *RepoClient) graphql(ctx context.Context, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlEndpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("graphql http %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("graphql decode: %w", err)
	}
	if len(out.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", out.Errors[0].Message)
	}
	return nil
}
