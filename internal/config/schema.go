// Package config defines the .github/prove.yml schema and how it is loaded and
// validated. The config is the source of truth for which paths each owner may
// self-approve.
package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultMaxChangedFiles caps the number of files prove evaluates unless a repo
// config overrides it.
const DefaultMaxChangedFiles = 3000

// Config is the parsed .github/prove.yml.
type Config struct {
	// Rules grant self-approval: a PR confined to a rule's Paths may be
	// self-approved by anyone in its Allow list. A PR is auto-approved only when
	// every changed path is covered by a rule allowing its author.
	Rules []Rule `yaml:"rules"`
	// Mode controls whether prove acts on its decision. See Mode.
	Mode Mode `yaml:"mode"`
	// Comment, when true, posts a comment on every PR explaining why prove did or
	// didn't approve. Independent of Mode. dry_run always comments regardless.
	Comment bool `yaml:"comment"`
	// Protect holds the guards that always require human review regardless of any
	// rule.
	Protect Protect `yaml:"protect"`
	// BlockedUsers may never have a PR auto-approved, even when a rule would
	// otherwise allow it.
	BlockedUsers []string `yaml:"blocked_users"`
	// MaxChangedFiles caps how many files prove will evaluate before refusing the
	// PR as too large to verify confidently.
	MaxChangedFiles int `yaml:"max_changed_files"`
	// AutoMerge optionally enables GitHub native auto-merge after approval.
	AutoMerge AutoMergeConfig `yaml:"auto_merge"`
}

// Protect groups the guards that force human review regardless of rules.
type Protect struct {
	// DotGithub, when true (the default), forces review of any PR touching the
	// .github/ folder (workflows, actions, templates, etc.).
	DotGithub *bool `yaml:"dot_github"`
	// ConfigFile, when true (the default), forces review of any PR touching
	// .github/prove.yml — so the rules cannot be rewritten by self-approval.
	ConfigFile *bool `yaml:"config_file"`
	// Paths are extra globs that always require review: a PR touching any of them
	// is never auto-approved, even if a rule would allow it. Doublestar syntax; a
	// trailing "/" means the subtree.
	Paths []string `yaml:"paths"`
}

// Mode controls whether prove acts on its decision.
type Mode string

const (
	// ModeEnforce (the default) approves/dismisses and posts a Check Run.
	ModeEnforce Mode = "enforce"
	// ModeDryRun takes no action (no approve/dismiss/auto-merge); it only posts a
	// comment describing what it *would* do. Use it to build trust before
	// granting prove approval power.
	ModeDryRun Mode = "dry_run"
)

// EffectiveMode returns Mode, defaulting to enforce when unset.
func (c *Config) EffectiveMode() Mode {
	if c.Mode == "" {
		return ModeEnforce
	}
	return c.Mode
}

// Rule grants self-approval of PRs confined to Paths to the principals in Allow,
// optionally restricted to a change kind (Semver bump or SHA pin). Allow is the
// trust anchor; Semver/SHA are an additional, per-file narrowing on top of it.
type Rule struct {
	// Paths are globs (doublestar syntax; a trailing "/" means the whole
	// subtree, e.g. "docs/").
	Paths []string `yaml:"paths"`
	// Allow lists who may self-approve: GitHub handles "@user" or "@org/team"
	// (a bot account such as "@dependabot[bot]" is just a @user here).
	Allow []string `yaml:"allow"`
	// Semver, when set, permits in-range version bumps (patch/minor/major),
	// detected from the diff. ANDed with Allow: the author must be allowed AND
	// every changed line in a matched file must be a permitted change kind — a
	// version bump within these levels (and, if SHA is also set, a pinned-SHA
	// swap). Any other change sends the PR to a human. Empty means "no semver
	// constraint".
	Semver []string `yaml:"semver"`
	// SHA, when true, permits updates of a pinned commit SHA or image digest (e.g.
	// a SHA-pinned GitHub Action or @sha256: digest). Like Semver it is ANDed with
	// Allow and applies per file. Set both Semver and SHA to permit a file that
	// mixes in-range bumps and pins.
	SHA bool `yaml:"sha"`
}

// AutoMergeConfig controls native auto-merge.
type AutoMergeConfig struct {
	Enabled bool   `yaml:"enabled"`
	Method  string `yaml:"method"` // squash | merge | rebase
}

// Default is the configuration applied when no .github/prove.yml exists: no
// rules, so prove approves nothing until rules are configured.
func Default() *Config {
	return &Config{MaxChangedFiles: DefaultMaxChangedFiles}
}

// Parse unmarshals and validates prove.yml content.
func Parse(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse prove.yml: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

var validMergeMethods = map[string]bool{"": true, "squash": true, "merge": true, "rebase": true}

var validSemver = map[string]bool{"patch": true, "minor": true, "major": true}

func (c *Config) applyDefaults() {
	if c.MaxChangedFiles == 0 {
		c.MaxChangedFiles = DefaultMaxChangedFiles
	}
}

// EffectiveMaxChangedFiles returns the configured cap, falling back to the
// default for tests and callers that construct Config directly.
func (c *Config) EffectiveMaxChangedFiles() int {
	if c.MaxChangedFiles > 0 {
		return c.MaxChangedFiles
	}
	return DefaultMaxChangedFiles
}

// ConfigFileProtected reports whether PRs touching the prove config require human
// review. It defaults to true when unset.
func (c *Config) ConfigFileProtected() bool {
	return c.Protect.ConfigFile == nil || *c.Protect.ConfigFile
}

// DotGithubProtected reports whether PRs touching the .github/ folder require
// human review. It defaults to true when unset.
func (c *Config) DotGithubProtected() bool {
	return c.Protect.DotGithub == nil || *c.Protect.DotGithub
}

// Validate checks the config for self-consistency. It returns an error that is
// surfaced to the user (via the Check Run) rather than silently ignored.
func (c *Config) Validate() error {
	if c.MaxChangedFiles < 0 {
		return fmt.Errorf("max_changed_files must be positive")
	}
	switch c.Mode {
	case "", ModeEnforce, ModeDryRun:
	default:
		return fmt.Errorf("mode %q must be one of enforce, dry_run", c.Mode)
	}
	for i, r := range c.Rules {
		if len(r.Paths) == 0 {
			return fmt.Errorf("rules[%d]: at least one path is required", i)
		}
		for _, p := range r.Paths {
			if p == "" {
				return fmt.Errorf("rules[%d]: empty path", i)
			}
		}
		if len(r.Allow) == 0 {
			return fmt.Errorf("rules[%d]: at least one allow principal is required", i)
		}
		for _, a := range r.Allow {
			if !strings.HasPrefix(a, "@") || len(a) < 2 {
				return fmt.Errorf("rules[%d]: %q must be a @user or @org/team handle", i, a)
			}
		}
		for _, s := range r.Semver {
			if !validSemver[s] {
				return fmt.Errorf("rules[%d]: semver %q must be one of patch, minor, major", i, s)
			}
		}
	}
	for i, p := range c.Protect.Paths {
		if p == "" {
			return fmt.Errorf("protect.paths[%d]: empty path", i)
		}
	}
	if !validMergeMethods[c.AutoMerge.Method] {
		return fmt.Errorf("auto_merge.method %q must be one of squash, merge, rebase", c.AutoMerge.Method)
	}
	return nil
}
