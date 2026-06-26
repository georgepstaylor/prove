package engine

import "testing"

func TestSplitTeam(t *testing.T) {
	org, slug, ok := SplitTeam("@org/writers")
	if !ok || org != "org" || slug != "writers" {
		t.Fatalf("SplitTeam = (%q,%q,%v)", org, slug, ok)
	}
	if _, _, ok := SplitTeam("@alice"); ok {
		t.Fatal("expected SplitTeam to reject a user handle (no slash)")
	}
}

func TestPrincipalMatches(t *testing.T) {
	if !principalMatches("@alice", "alice", nil) {
		t.Error("user handle should match its login")
	}
	if principalMatches("@alice", "bob", nil) {
		t.Error("user handle should not match a different login")
	}
	if !principalMatches("@org/writers", "bob", []string{"org/writers"}) {
		t.Error("team handle should match a member")
	}
	if principalMatches("@org/writers", "bob", nil) {
		t.Error("team handle should not match a non-member")
	}
}

func TestCompilePattern(t *testing.T) {
	cases := map[string]string{
		"docs/":     "docs/**",
		"/docs/":    "docs/**",
		"*.go":      "*.go",
		"/file.txt": "file.txt",
	}
	for in, want := range cases {
		if got := compilePattern(in); got != want {
			t.Errorf("compilePattern(%q) = %q, want %q", in, got, want)
		}
	}
}
