// Package engine contains prove's core decision logic: given the repo config,
// the PR author, and the changed files, decide whether the PR may be
// auto-approved. The Decide function is pure and side-effect free so it can be
// exhaustively unit tested.
package engine

import (
	"fmt"

	"github.com/georgepstaylor/prove/internal/config"
)

// FailingFile records a path that blocked approval and why.
type FailingFile struct {
	File   string
	Reason string
}

// Decision is the outcome of evaluating a pull request. The same struct drives
// both the review action and the transparency Check Run.
type Decision struct {
	// Approve is true only if every changed path is permitted.
	Approve bool
	Reasons []string
	// FailingFiles lists the paths (and reasons) that prevented approval.
	FailingFiles []FailingFile
}

// Decide evaluates whether author's changes may be auto-approved under cfg.
//
// The rule is: approve iff some rule allows the author to self-approve EVERY
// touched path. A single uncovered path blocks the whole PR. Guards override the
// rules entirely (see GuardedPath): the prove config file, the .github/ folder,
// and any protect.paths match — all (by default) require human review.
//
// authorTeams is the set of teams (normalized "org/slug") the author belongs to,
// used to satisfy rules that allow a team. Pass nil when teams are not in play.
//
// Rules with a semver/sha constraint are matched against the change kind of the
// specific file being evaluated (detected from that file's own patch), so the
// constraint means "this file is purely an in-range bump/pin" — not "some file
// in the PR is".
func Decide(cfg *config.Config, files []ChangedFile, author string, authorTeams []string) (Decision, error) {
	if contains(cfg.BlockedUsers, author) {
		return Decision{Reasons: []string{fmt.Sprintf("author %s is blocked", author)}}, nil
	}
	if len(files) == 0 {
		return Decision{Reasons: []string{"no changed files to evaluate"}}, nil
	}
	maxChangedFiles := cfg.EffectiveMaxChangedFiles()
	if len(files) > maxChangedFiles {
		return Decision{Reasons: []string{fmt.Sprintf("too many changed files (%d > %d) to verify", len(files), maxChangedFiles)}}, nil
	}

	// Only analyze patches when some rule actually carries a semver/sha
	// constraint; otherwise the per-file change kind is never consulted.
	detect := hasConstraint(cfg)

	var failing []FailingFile
	for _, f := range files {
		var c change
		if detect {
			c = analyzeChange(f.Patch)
		}
		for _, p := range f.TouchedPaths() {
			reason, err := evaluatePath(cfg, author, p, authorTeams, c)
			if err != nil {
				return Decision{}, err
			}
			if reason != "" {
				failing = append(failing, FailingFile{File: p, Reason: reason})
			}
		}
	}

	if len(failing) > 0 {
		return Decision{
			Reasons:      []string{fmt.Sprintf("%d path(s) have no rule allowing %s to self-approve", len(failing), author)},
			FailingFiles: failing,
		}, nil
	}

	return Decision{
		Approve: true,
		Reasons: []string{fmt.Sprintf("every changed file is covered by a rule allowing %s to self-approve", author)},
	}, nil
}

// evaluatePath returns an empty string if the path is permitted, or a reason it
// is not. c is the change kinds of the file this path belongs to, used by rules
// with a semver/sha constraint.
func evaluatePath(cfg *config.Config, author, p string, authorTeams []string, c change) (string, error) {
	if reason, err := GuardedPath(cfg, p); err != nil {
		return "", err
	} else if reason != "" {
		return reason, nil
	}
	allowed, err := ruleAllows(cfg, author, p, authorTeams, c)
	if err != nil {
		return "", err
	}
	if !allowed {
		return fmt.Sprintf("no rule allows %s to self-approve this path", author), nil
	}
	return "", nil
}

// hasConstraint reports whether any rule carries a semver or sha constraint, so
// per-file change detection only runs when it can affect the outcome.
func hasConstraint(cfg *config.Config) bool {
	for _, r := range cfg.Rules {
		if len(r.Semver) > 0 || r.SHA {
			return true
		}
	}
	return false
}
