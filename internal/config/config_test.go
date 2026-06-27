package config

import (
	"context"
	"testing"
)

func TestParseValid(t *testing.T) {
	yml := []byte(`
rules:
  - paths: ["docs/", "*.md"]
    allow: ["@org/writers", "@alice"]
  - paths: ["**/go.mod", "**/go.sum"]
    allow: ["@dependabot[bot]"]
    semver: [patch, minor]
blocked_users: [mallory]
protect:
  config_file: false
max_changed_files: 42
auto_merge:
  enabled: true
  method: squash
`)
	c, err := Parse(yml)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Rules) != 2 {
		t.Errorf("rules: got %d, want 2", len(c.Rules))
	}
	if r := c.Rules[0]; len(r.Paths) != 2 || len(r.Allow) != 2 || r.Allow[0] != "@org/writers" {
		t.Errorf("first rule not parsed: %+v", r)
	}
	if r := c.Rules[1]; len(r.Semver) != 2 || r.Semver[0] != "patch" || r.Allow[0] != "@dependabot[bot]" {
		t.Errorf("semver rule not parsed: %+v", r)
	}
	if c.ConfigFileProtected() {
		t.Error("protect.config_file: false should disable protection")
	}
	if c.AutoMerge.Method != "squash" {
		t.Errorf("auto_merge.method: got %q", c.AutoMerge.Method)
	}
	if c.MaxChangedFiles != 42 {
		t.Errorf("max_changed_files: got %d, want 42", c.MaxChangedFiles)
	}
}

func TestConfigFileProtectedDefaultsTrue(t *testing.T) {
	c, err := Parse([]byte("rules:\n  - paths: [\"docs/\"]\n    allow: [\"@alice\"]\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !c.ConfigFileProtected() {
		t.Error("config file protection should default to true")
	}
}

func TestValidateRejectsAllowWithoutAt(t *testing.T) {
	_, err := Parse([]byte("rules:\n  - paths: [\"docs/\"]\n    allow: [alice]\n"))
	if err == nil {
		t.Fatal("expected error for allow handle missing @ prefix")
	}
}

func TestValidateRejectsEmptyAllow(t *testing.T) {
	_, err := Parse([]byte("rules:\n  - paths: [\"docs/\"]\n    allow: []\n"))
	if err == nil {
		t.Fatal("expected error for rule with empty allow")
	}
}

func TestParseProtectPaths(t *testing.T) {
	c, err := Parse([]byte("protect:\n  paths: [\"deploy/prod/\", \"secret/**\"]\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Protect.Paths) != 2 {
		t.Errorf("protect.paths: got %d, want 2", len(c.Protect.Paths))
	}
}

func TestValidateRejectsEmptyProtectPath(t *testing.T) {
	_, err := Parse([]byte("protect:\n  paths: [\"\"]\n"))
	if err == nil {
		t.Fatal("expected error for empty protect path")
	}
}

func TestValidateRejectsBadMergeMethod(t *testing.T) {
	_, err := Parse([]byte("auto_merge:\n  method: fastforward\n"))
	if err == nil {
		t.Fatal("expected error for invalid merge method")
	}
}

func TestEnabledDefaultsTrue(t *testing.T) {
	c, err := Parse([]byte("rules:\n  - paths: [\"docs/\"]\n    allow: [\"@a\"]\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !c.IsEnabled() {
		t.Error("enabled should default to true when unset")
	}

	c, err = Parse([]byte("enabled: false\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.IsEnabled() {
		t.Error("enabled: false should disable prove")
	}
}

func TestValidateRejectsBadSemver(t *testing.T) {
	_, err := Parse([]byte("rules:\n  - paths: [\"go.mod\"]\n    allow: [\"@dependabot[bot]\"]\n    semver: [hotfix]\n"))
	if err == nil {
		t.Fatal("expected error for invalid semver token")
	}
}

func TestValidateAcceptsSemverAndSHA(t *testing.T) {
	// semver and sha may be combined: the rule then permits bumps and/or pins.
	if _, err := Parse([]byte("rules:\n  - paths: [\"go.mod\"]\n    allow: [\"@dependabot[bot]\"]\n    semver: [patch]\n    sha: true\n")); err != nil {
		t.Fatalf("a rule with both semver and sha should be valid: %v", err)
	}
}

func TestParseDefaultsMaxChangedFiles(t *testing.T) {
	c, err := Parse([]byte("rules:\n  - paths: [\"docs/\"]\n    allow: [\"@alice\"]\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.MaxChangedFiles != DefaultMaxChangedFiles {
		t.Errorf("max_changed_files default: got %d, want %d", c.MaxChangedFiles, DefaultMaxChangedFiles)
	}
}

func TestValidateRejectsNegativeMaxChangedFiles(t *testing.T) {
	_, err := Parse([]byte("max_changed_files: -1\n"))
	if err == nil {
		t.Fatal("expected error for negative max_changed_files")
	}
}

// fakeFetcher implements FileFetcher for Load tests.
type fakeFetcher struct {
	data  []byte
	found bool
	err   error
}

func (f fakeFetcher) GetFile(_ context.Context, _, _, _, _ string) ([]byte, bool, error) {
	return f.data, f.found, f.err
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	c, err := Load(context.Background(), fakeFetcher{found: false}, "o", "r", "main")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Rules) != 0 {
		t.Errorf("default config should have no rules, got %v", c.Rules)
	}
}

func TestLoadParsesPresentFile(t *testing.T) {
	c, err := Load(context.Background(), fakeFetcher{data: []byte("rules:\n  - paths: [\"docs/\"]\n    allow: [\"@alice\"]\n"), found: true}, "o", "r", "main")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Rules) != 1 {
		t.Errorf("expected one rule, got %v", c.Rules)
	}
}
