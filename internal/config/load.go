package config

import "context"

// DefaultPath is where prove looks for its config in a repository.
const DefaultPath = ".github/prove.yml"

// FileFetcher reads a single file from a repository at a specific ref. The real
// implementation wraps the GitHub Contents API; tests use a fake. It returns
// found=false (with nil error) when the file does not exist.
type FileFetcher interface {
	GetFile(ctx context.Context, owner, repo, path, ref string) (content []byte, found bool, err error)
}

// Load reads and parses the prove config from the given ref. It must be called
// with the base repository's default branch as ref — never the PR head — so a
// PR author cannot rewrite the rules in the same change they want approved.
//
// When the config file is absent, Load returns Default().
func Load(ctx context.Context, f FileFetcher, owner, repo, ref string) (*Config, error) {
	data, found, err := f.GetFile(ctx, owner, repo, DefaultPath, ref)
	if err != nil {
		return nil, err
	}
	if !found {
		return Default(), nil
	}
	return Parse(data)
}
