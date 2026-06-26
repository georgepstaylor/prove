package githubapp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v78/github"

	"github.com/georgepstaylor/prove/internal/engine"
)

// RepoClient is a thin wrapper over the GitHub REST client, scoped to one
// installation.
type RepoClient struct {
	gh   *github.Client
	http *http.Client // shares the installation transport; used for GraphQL
}

// GetFile reads a file at ref, returning found=false (nil error) when the path
// does not exist so callers can fall back to defaults.
func (c *RepoClient) GetFile(ctx context.Context, owner, repo, path, ref string) ([]byte, bool, error) {
	fc, _, resp, err := c.gh.Repositories.GetContents(ctx, owner, repo, path, &github.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get contents %s@%s: %w", path, ref, err)
	}
	if fc == nil {
		// Path resolved to a directory, not a file.
		return nil, false, nil
	}
	content, err := fc.GetContent()
	if err != nil {
		return nil, false, fmt.Errorf("decode %s: %w", path, err)
	}
	return []byte(content), true, nil
}

// ListChangedFiles returns every file touched by a pull request, stopping early
// once it exceeds maxChangedFiles (the engine refuses PRs that large anyway).
func (c *RepoClient) ListChangedFiles(ctx context.Context, owner, repo string, number, maxChangedFiles int) ([]engine.ChangedFile, error) {
	opt := &github.ListOptions{PerPage: 100}
	var out []engine.ChangedFile
	for {
		fs, resp, err := c.gh.PullRequests.ListFiles(ctx, owner, repo, number, opt)
		if err != nil {
			return nil, fmt.Errorf("list files for #%d: %w", number, err)
		}
		for _, f := range fs {
			cf := engine.ChangedFile{Path: f.GetFilename(), Patch: f.GetPatch()}
			if f.GetStatus() == "renamed" {
				cf.Previous = f.GetPreviousFilename()
			}
			out = append(out, cf)
		}
		if resp.NextPage == 0 || len(out) > maxChangedFiles {
			break
		}
		opt.Page = resp.NextPage
	}
	return out, nil
}
