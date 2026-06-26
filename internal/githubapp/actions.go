package githubapp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v78/github"
)

// FindApproval looks for an active approving review left by the prove bot,
// returning its id and approved commit SHA so the caller can tell whether the
// current head is already approved.
func (c *RepoClient) FindApproval(ctx context.Context, owner, repo string, number int, botLogin string) (found bool, reviewID int64, commitID string, err error) {
	opt := &github.ListOptions{PerPage: 100}
	for {
		reviews, resp, lerr := c.gh.PullRequests.ListReviews(ctx, owner, repo, number, opt)
		if lerr != nil {
			return false, 0, "", fmt.Errorf("list reviews for #%d: %w", number, lerr)
		}
		for _, r := range reviews {
			if r.GetUser().GetLogin() == botLogin && r.GetState() == "APPROVED" {
				// Keep scanning: later approvals supersede earlier ones.
				found, reviewID, commitID = true, r.GetID(), r.GetCommitID()
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return found, reviewID, commitID, nil
}

// Approve submits an approving review pinned to commitID, so it is attributed to
// the exact commit prove evaluated rather than the live head at record time.
func (c *RepoClient) Approve(ctx context.Context, owner, repo string, number int, commitID, body string) error {
	_, _, err := c.gh.PullRequests.CreateReview(ctx, owner, repo, number, &github.PullRequestReviewRequest{
		CommitID: github.Ptr(commitID),
		Event:    github.Ptr("APPROVE"),
		Body:     github.Ptr(body),
	})
	if err != nil {
		return fmt.Errorf("approve #%d at %s: %w", number, commitID, err)
	}
	return nil
}

// PullHeadSHA returns the PR's current head SHA, used to confirm the head has not
// advanced since evaluation before approving.
func (c *RepoClient) PullHeadSHA(ctx context.Context, owner, repo string, number int) (string, error) {
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return "", fmt.Errorf("get pull #%d: %w", number, err)
	}
	return pr.GetHead().GetSHA(), nil
}

// DismissReview dismisses a previously submitted review.
func (c *RepoClient) DismissReview(ctx context.Context, owner, repo string, number int, reviewID int64, message string) error {
	_, _, err := c.gh.PullRequests.DismissReview(ctx, owner, repo, number, reviewID, &github.PullRequestReviewDismissalRequest{
		Message: github.Ptr(message),
	})
	if err != nil {
		return fmt.Errorf("dismiss review %d on #%d: %w", reviewID, number, err)
	}
	return nil
}

// UpsertCheck publishes a completed check run for prove's decision. A fresh run
// per evaluation is fine — GitHub surfaces the latest for the head SHA.
func (c *RepoClient) UpsertCheck(ctx context.Context, owner, repo, headSHA, conclusion, title, summary string) error {
	_, _, err := c.gh.Checks.CreateCheckRun(ctx, owner, repo, github.CreateCheckRunOptions{
		Name:       "prove",
		HeadSHA:    headSHA,
		Status:     github.Ptr("completed"),
		Conclusion: github.Ptr(conclusion),
		Output: &github.CheckRunOutput{
			Title:   github.Ptr(title),
			Summary: github.Ptr(summary),
		},
	})
	if err != nil {
		return fmt.Errorf("create check run: %w", err)
	}
	return nil
}

// UpsertComment creates a PR comment, or edits the existing one containing marker
// (a hidden token), so repeated evaluations update one comment instead of spamming.
func (c *RepoClient) UpsertComment(ctx context.Context, owner, repo string, number int, marker, body string) error {
	opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := c.gh.Issues.ListComments(ctx, owner, repo, number, opt)
		if err != nil {
			return fmt.Errorf("list comments for #%d: %w", number, err)
		}
		for _, cm := range comments {
			if strings.Contains(cm.GetBody(), marker) {
				_, _, err := c.gh.Issues.EditComment(ctx, owner, repo, cm.GetID(), &github.IssueComment{Body: github.Ptr(body)})
				if err != nil {
					return fmt.Errorf("edit comment %d: %w", cm.GetID(), err)
				}
				return nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	_, _, err := c.gh.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: github.Ptr(body)})
	if err != nil {
		return fmt.Errorf("create comment on #%d: %w", number, err)
	}
	return nil
}

// BotLogin returns the prove bot's review identity ("<slug>[bot]"). The result is
// stable, so callers should cache it.
func (a *App) BotLogin(ctx context.Context) (string, error) {
	atr, err := ghinstallation.NewAppsTransport(a.transport, a.appID, a.privateKey)
	if err != nil {
		return "", fmt.Errorf("apps transport: %w", err)
	}
	gh := github.NewClient(&http.Client{Transport: atr})
	app, _, err := gh.Apps.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("get app: %w", err)
	}
	return app.GetSlug() + "[bot]", nil
}
