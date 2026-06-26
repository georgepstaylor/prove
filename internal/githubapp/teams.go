package githubapp

import (
	"context"
	"fmt"
	"net/http"
)

// IsTeamMember reports whether user is an active member of org/slug. A 404 from
// the membership endpoint means "not a member" rather than an error.
func (c *RepoClient) IsTeamMember(ctx context.Context, org, slug, user string) (bool, error) {
	membership, resp, err := c.gh.Teams.GetTeamMembershipBySlug(ctx, org, slug, user)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("team membership %s/%s for %s: %w", org, slug, user, err)
	}
	return membership.GetState() == "active", nil
}

func mergeMethodEnum(method string) string {
	switch method {
	case "merge":
		return "MERGE"
	case "rebase":
		return "REBASE"
	default:
		return "SQUASH"
	}
}

// EnableAutoMerge turns on native auto-merge via GraphQL (the only API that
// supports it). Best-effort: an error is commonly just the repo having auto-merge
// disabled, so callers should treat it as non-fatal.
func (c *RepoClient) EnableAutoMerge(ctx context.Context, prNodeID, method string) error {
	const mutation = `mutation($prId:ID!,$method:PullRequestMergeMethod!){enablePullRequestAutoMerge(input:{pullRequestId:$prId,mergeMethod:$method}){clientMutationId}}`
	body := map[string]any{
		"query": mutation,
		"variables": map[string]any{
			"prId":   prNodeID,
			"method": mergeMethodEnum(method),
		},
	}
	return c.graphql(ctx, body)
}
