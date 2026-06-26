package config

import (
	"os"
	"testing"
)

// TestExampleConfigParses guards the example .github/prove.yml shipped in the
// repo against schema drift.
func TestExampleConfigParses(t *testing.T) {
	data, err := os.ReadFile("../../.github/prove.yml")
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("example .github/prove.yml does not parse: %v", err)
	}
}
