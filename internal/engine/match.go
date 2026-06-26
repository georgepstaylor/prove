package engine

import (
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/georgepstaylor/prove/internal/config"
)

// configFiles are prove's own config paths; approving a change to them would let
// an author rewrite the rules unreviewed.
var configFiles = map[string]bool{
	".github/prove.yml":  true,
	".github/prove.yaml": true,
}

// IsConfigFile reports whether p is prove's config file.
func IsConfigFile(p string) bool { return configFiles[p] }

func underDotGithub(p string) bool {
	return p == ".github" || strings.HasPrefix(p, ".github/")
}

// GuardedPath returns a non-empty reason if p must always be human-reviewed
// regardless of any rule (the config file, .github/, or a protect.paths match).
func GuardedPath(cfg *config.Config, p string) (string, error) {
	if cfg.ConfigFileProtected() && IsConfigFile(p) {
		return "the prove config file always requires human review", nil
	}
	if cfg.DotGithubProtected() && underDotGithub(p) {
		return ".github changes require human review (protect.dot_github is on)", nil
	}
	for _, pattern := range cfg.Protect.Paths {
		ok, err := matchPattern(pattern, p)
		if err != nil {
			return "", err
		}
		if ok {
			return "path is on the protect list and always requires review", nil
		}
	}
	return "", nil
}

// matchPattern reports whether filePath matches a rule path glob.
func matchPattern(pattern, filePath string) (bool, error) {
	ok, err := doublestar.Match(compilePattern(pattern), filePath)
	if err != nil {
		return false, fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	return ok, nil
}

// compilePattern normalizes a rule path to a doublestar glob: a leading "/" is
// stripped (globs are already root-anchored) and a trailing "/" expands to the
// whole subtree.
func compilePattern(p string) string {
	p = strings.TrimPrefix(p, "/")
	if strings.HasSuffix(p, "/") {
		return p + "**"
	}
	return p
}

// ruleAllows reports whether some rule lets author self-approve filePath: its
// paths match, author (or a team they're in) is allowed, and its optional
// semver/sha constraint is satisfied by c, the file's change kinds.
func ruleAllows(cfg *config.Config, author, filePath string, authorTeams []string, c change) (bool, error) {
	for _, r := range cfg.Rules {
		matched, err := ruleMatchesPath(r, filePath)
		if err != nil {
			return false, err
		}
		if matched && authorAllowed(r, author, authorTeams) && constraintSatisfied(r, c) {
			return true, nil
		}
	}
	return false, nil
}

func authorAllowed(r config.Rule, author string, authorTeams []string) bool {
	for _, principal := range r.Allow {
		if principalMatches(principal, author, authorTeams) {
			return true
		}
	}
	return false
}

// constraintSatisfied reports whether a file's change kinds satisfy a rule's
// optional semver/sha constraint. With no constraint it always passes; otherwise
// c must be non-empty and a subset of the permitted kinds. changeUnknown is never
// permitted, so an unrecognised line, an out-of-range bump, or a pin under a
// semver-only rule all fail closed.
func constraintSatisfied(r config.Rule, c change) bool {
	allowed := allowedChanges(r)
	if allowed == 0 {
		return true // no change-kind constraint
	}
	return c != 0 && c&^allowed == 0
}

// allowedChanges is the set of change kinds a rule permits via its semver levels
// and sha flag. changeUnknown is never included.
func allowedChanges(r config.Rule) change {
	var a change
	for _, s := range r.Semver {
		switch s {
		case "patch":
			a |= changePatch
		case "minor":
			a |= changeMinor
		case "major":
			a |= changeMajor
		}
	}
	if r.SHA {
		a |= changeSHA
	}
	return a
}

func ruleMatchesPath(r config.Rule, filePath string) (bool, error) {
	for _, pattern := range r.Paths {
		ok, err := matchPattern(pattern, filePath)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// principalMatches reports whether handle ("@user" or "@org/team") refers to the
// author directly or to a team they belong to.
func principalMatches(handle, author string, authorTeams []string) bool {
	n := NormalizeTeam(handle)
	if strings.Contains(n, "/") {
		return contains(authorTeams, n)
	}
	return n == author
}

// NormalizeTeam strips the leading "@" from a handle ("@org/writers" → "org/writers").
func NormalizeTeam(handle string) string {
	return strings.TrimPrefix(handle, "@")
}

// SplitTeam parses "@org/writers" into org and slug, with ok=false for user
// handles (no slash).
func SplitTeam(handle string) (org, slug string, ok bool) {
	parts := strings.SplitN(NormalizeTeam(handle), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// contains reports whether s is in list (case-sensitive, as GitHub logins compare).
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
