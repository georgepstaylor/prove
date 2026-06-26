package engine

import (
	"testing"

	"github.com/georgepstaylor/prove/internal/config"
)

func files(ps ...string) []ChangedFile {
	out := make([]ChangedFile, len(ps))
	for i, p := range ps {
		out[i] = ChangedFile{Path: p}
	}
	return out
}

// rule builds a config with a single rule granting allow over paths.
func rule(paths []string, allow ...string) *config.Config {
	return &config.Config{Rules: []config.Rule{{Paths: paths, Allow: allow}}}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		author      string
		authorTeams []string
		files       []ChangedFile
		wantApprove bool
		wantFailing int
	}{
		{
			name:        "rule covering a subtree is approved",
			cfg:         rule([]string{"playground/alice/"}, "@alice"),
			author:      "alice",
			files:       files("playground/alice/notes.md", "playground/alice/sub/x.txt"),
			wantApprove: true,
		},
		{
			name:        "user not in allow is refused",
			cfg:         rule([]string{"playground/alice/"}, "@alice"),
			author:      "alice",
			files:       files("playground/bob/notes.md"),
			wantApprove: false,
			wantFailing: 1,
		},
		{
			name:        "uncovered path is refused",
			cfg:         rule([]string{"docs/"}, "@alice"),
			author:      "alice",
			files:       files("src/main.go"),
			wantApprove: false,
			wantFailing: 1,
		},
		{
			name:        "one uncovered file blocks the whole PR",
			cfg:         rule([]string{"playground/alice/"}, "@alice"),
			author:      "alice",
			files:       files("playground/alice/ok.md", "src/main.go"),
			wantApprove: false,
			wantFailing: 1,
		},
		{
			name:        "no changed files is not approvable",
			cfg:         rule([]string{"**"}, "@alice"),
			author:      "alice",
			files:       nil,
			wantApprove: false,
		},
		{
			name:        "empty default config approves nothing",
			cfg:         config.Default(),
			author:      "alice",
			files:       files("playground/alice/notes.md"),
			wantApprove: false,
			wantFailing: 1,
		},
		{
			name:        "exact file path rule",
			cfg:         rule([]string{"deploy/prod.yaml"}, "@alice"),
			author:      "alice",
			files:       files("deploy/prod.yaml"),
			wantApprove: true,
		},
		{
			name:        "extension glob matches",
			cfg:         rule([]string{"*.md"}, "@alice"),
			author:      "alice",
			files:       files("README.md"),
			wantApprove: true,
		},
		{
			name:        "extension glob does not match other types",
			cfg:         rule([]string{"*.md"}, "@alice"),
			author:      "alice",
			files:       files("main.go"),
			wantApprove: false,
			wantFailing: 1,
		},
		{
			name:        "config file requires review even when covered",
			cfg:         rule([]string{".github/"}, "@alice"),
			author:      "alice",
			files:       files(".github/prove.yml"),
			wantApprove: false,
			wantFailing: 1,
		},
		{
			name: "config file allowed when both protections disabled",
			cfg: &config.Config{
				Rules:   []config.Rule{{Paths: []string{".github/"}, Allow: []string{"@alice"}}},
				Protect: config.Protect{ConfigFile: boolPtr(false), DotGithub: boolPtr(false)},
			},
			author:      "alice",
			files:       files(".github/prove.yml"),
			wantApprove: true,
		},
		{
			name:        "team rule approved when a member",
			cfg:         rule([]string{"docs/"}, "@org/writers"),
			author:      "bob",
			authorTeams: []string{"org/writers"},
			files:       files("docs/guide.md"),
			wantApprove: true,
		},
		{
			name:        "team rule refused when not a member",
			cfg:         rule([]string{"docs/"}, "@org/writers"),
			author:      "bob",
			files:       files("docs/guide.md"),
			wantApprove: false,
			wantFailing: 1,
		},
		{
			name: "blocked user never approved",
			cfg: &config.Config{
				Rules:        []config.Rule{{Paths: []string{"playground/dave/"}, Allow: []string{"@dave"}}},
				BlockedUsers: []string{"dave"},
			},
			author:      "dave",
			files:       files("playground/dave/notes.md"),
			wantApprove: false,
		},
		{
			name:        "rename out of covered area is blocked via previous path",
			cfg:         rule([]string{"playground/alice/"}, "@alice"),
			author:      "alice",
			files:       []ChangedFile{{Path: "playground/alice/moved.md", Previous: "src/old.md"}},
			wantApprove: false,
			wantFailing: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(tt.cfg, tt.files, tt.author, tt.authorTeams)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Approve != tt.wantApprove {
				t.Errorf("Approve: got %v, want %v (reasons=%v failing=%v)", got.Approve, tt.wantApprove, got.Reasons, got.FailingFiles)
			}
			if tt.wantFailing > 0 && len(got.FailingFiles) != tt.wantFailing {
				t.Errorf("FailingFiles: got %d, want %d (%v)", len(got.FailingFiles), tt.wantFailing, got.FailingFiles)
			}
			if got.Approve && len(got.FailingFiles) != 0 {
				t.Errorf("approved but has failing files: %v", got.FailingFiles)
			}
		})
	}
}

// gomod returns a one-file PR whose go.mod patch bumps a dependency to v.
func gomod(v string) []ChangedFile {
	return []ChangedFile{{Path: "go.mod", Patch: "-\tgithub.com/foo/bar v1.0.0\n+\tgithub.com/foo/bar " + v}}
}

func TestDecideSemverRule(t *testing.T) {
	cfg := &config.Config{Rules: []config.Rule{{
		Paths:  []string{"**/go.mod", "**/go.sum"},
		Allow:  []string{"@dependabot[bot]"},
		Semver: []string{"patch", "minor"},
	}}}

	cases := []struct {
		name        string
		author      string
		files       []ChangedFile
		wantApprove bool
	}{
		{"allowed bot, patch bump", "dependabot[bot]", gomod("v1.0.1"), true},
		{"allowed bot, minor bump", "dependabot[bot]", gomod("v1.1.0"), true},
		{"allowed bot, major bump out of range", "dependabot[bot]", gomod("v2.0.0"), false},
		{"allowed bot, no bump (no patch)", "dependabot[bot]", files("go.mod"), false},
		{"allowed bot, non-bump edit in matched file", "dependabot[bot]", []ChangedFile{{Path: "go.mod", Patch: "-// a\n+// b"}}, false},
		{"untrusted author with a real patch bump", "mallory", gomod("v1.0.1"), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(cfg, tt.files, tt.author, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Approve != tt.wantApprove {
				t.Errorf("Approve: got %v, want %v (%v)", got.Approve, tt.wantApprove, got.FailingFiles)
			}
		})
	}
}

func TestDecideSHARule(t *testing.T) {
	cfg := &config.Config{Rules: []config.Rule{{
		Paths: []string{"deps/pins.txt"},
		Allow: []string{"@renovate[bot]"},
		SHA:   true,
	}}}
	pin := func(a, b string) []ChangedFile {
		return []ChangedFile{{Path: "deps/pins.txt", Patch: "-tool@sha256:" + a + "\n+tool@sha256:" + b}}
	}

	cases := []struct {
		name        string
		author      string
		files       []ChangedFile
		wantApprove bool
	}{
		{"trusted bot, sha pin updated", "renovate[bot]", pin(hex64("a"), hex64("b")), true},
		{"trusted bot, not a sha change", "renovate[bot]", []ChangedFile{{Path: "deps/pins.txt", Patch: "-x\n+y"}}, false},
		{"untrusted author, sha change", "mallory", pin(hex64("a"), hex64("b")), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(cfg, tt.files, tt.author, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Approve != tt.wantApprove {
				t.Errorf("Approve: got %v, want %v (%v)", got.Approve, tt.wantApprove, got.FailingFiles)
			}
		})
	}
}

// TestDecideConstraintKinds checks that each flag permits only its own change
// kind, and that setting both permits a file mixing bumps and pins.
func TestDecideConstraintKinds(t *testing.T) {
	mixed := []ChangedFile{{Path: "deps.txt", Patch: "-tool@sha256:" + hex64("a") + "\n+tool@sha256:" + hex64("b") + "\n-\tx v1.0.0\n+\tx v1.0.1"}}
	pinOnly := []ChangedFile{{Path: "deps.txt", Patch: "-tool@sha256:" + hex64("a") + "\n+tool@sha256:" + hex64("b")}}
	bumpOnly := []ChangedFile{{Path: "deps.txt", Patch: "-\tx v1.0.0\n+\tx v1.0.1"}}

	ruleWith := func(r config.Rule) *config.Config {
		r.Paths = []string{"deps.txt"}
		r.Allow = []string{"@bot"}
		return &config.Config{Rules: []config.Rule{r}}
	}

	cases := []struct {
		name        string
		cfg         *config.Config
		files       []ChangedFile
		wantApprove bool
	}{
		{"semver-only refuses a pin", ruleWith(config.Rule{Semver: []string{"patch"}}), pinOnly, false},
		{"sha-only refuses a bump", ruleWith(config.Rule{SHA: true}), bumpOnly, false},
		{"both permits a mix", ruleWith(config.Rule{Semver: []string{"patch"}, SHA: true}), mixed, true},
		{"both refuses an out-of-range bump in the mix", ruleWith(config.Rule{Semver: []string{"patch"}, SHA: true}),
			[]ChangedFile{{Path: "deps.txt", Patch: "-tool@sha256:" + hex64("a") + "\n+tool@sha256:" + hex64("b") + "\n-\tx v1.0.0\n+\tx v2.0.0"}}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(tt.cfg, tt.files, "bot", nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Approve != tt.wantApprove {
				t.Errorf("Approve: got %v, want %v (%v)", got.Approve, tt.wantApprove, got.FailingFiles)
			}
		})
	}
}

// TestDecideSemverNoCrossFileLeak is the regression for the approval leak (#1):
// a broad-path semver rule must NOT approve an unrelated code file just because
// another file in the PR contains an in-range bump.
func TestDecideSemverNoCrossFileLeak(t *testing.T) {
	cfg := &config.Config{
		Rules:   []config.Rule{{Paths: []string{"**"}, Allow: []string{"@bot"}, Semver: []string{"patch"}}},
		Protect: config.Protect{DotGithub: boolPtr(false), ConfigFile: boolPtr(false)},
	}
	prFiles := []ChangedFile{
		{Path: "go.mod", Patch: "-\tgithub.com/foo/bar v1.0.0\n+\tgithub.com/foo/bar v1.0.1"}, // a real patch bump
		{Path: "src/app.go", Patch: "-doSafeThing()\n+exfiltrateSecrets()"},                   // arbitrary code
	}
	got, err := Decide(cfg, prFiles, "bot", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Approve {
		t.Fatal("code file rode a global patch bump into approval (leak not fixed)")
	}
}

// TestDecideSemverNoCrossFilePoisoning is the regression for over-refusal (#3):
// a major bump in one file must NOT poison a separate patch-only rule covering a
// different file.
func TestDecideSemverNoCrossFilePoisoning(t *testing.T) {
	cfg := &config.Config{Rules: []config.Rule{
		{Paths: []string{"libA/go.mod"}, Allow: []string{"@bot"}, Semver: []string{"major"}},
		{Paths: []string{"libB/go.mod"}, Allow: []string{"@bot"}, Semver: []string{"patch"}},
	}}
	prFiles := []ChangedFile{
		{Path: "libA/go.mod", Patch: "-\ta v1.0.0\n+\ta v2.0.0"}, // major, allowed by rule 1
		{Path: "libB/go.mod", Patch: "-\tb v1.0.0\n+\tb v1.0.1"}, // patch, allowed by rule 2
	}
	got, err := Decide(cfg, prFiles, "bot", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Approve {
		t.Fatalf("expected approval; libA's major poisoned libB's patch rule (%v)", got.FailingFiles)
	}
}

func TestDecideBlockedPaths(t *testing.T) {
	// A rule allows alice everything, but protect.paths overrides it.
	cfg := &config.Config{
		Rules:   []config.Rule{{Paths: []string{"**"}, Allow: []string{"@alice"}}},
		Protect: config.Protect{Paths: []string{"secret/**"}, DotGithub: boolPtr(false)}, // isolate protect.paths
	}

	cases := []struct {
		name        string
		files       []ChangedFile
		wantApprove bool
	}{
		{"blocked glob refused", files("secret/key.txt"), false},
		{"nested blocked file refused", files("secret/sub/key.txt"), false},
		{"unblocked file allowed", files("docs/readme.md"), true},
		{"any blocked file blocks the PR", files("README.md", "secret/key.txt"), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(cfg, tt.files, "alice", nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Approve != tt.wantApprove {
				t.Errorf("Approve: got %v, want %v (%v)", got.Approve, tt.wantApprove, got.FailingFiles)
			}
		})
	}
}

func TestDecideDotGithubGuard(t *testing.T) {
	allowAll := []config.Rule{{Paths: []string{"**"}, Allow: []string{"@alice"}}}

	cases := []struct {
		name        string
		cfg         *config.Config
		file        string
		wantApprove bool
	}{
		{
			name:        "dot-github blocked by default",
			cfg:         &config.Config{Rules: allowAll},
			file:        ".github/labeler.yml",
			wantApprove: false,
		},
		{
			name:        "dot-github allowed when protection disabled",
			cfg:         &config.Config{Rules: allowAll, Protect: config.Protect{DotGithub: boolPtr(false)}},
			file:        ".github/labeler.yml",
			wantApprove: true,
		},
		{
			name:        "config file still blocked when dot-github protection disabled",
			cfg:         &config.Config{Rules: allowAll, Protect: config.Protect{DotGithub: boolPtr(false)}},
			file:        ".github/prove.yml",
			wantApprove: false,
		},
		{
			name:        "non dot-github file unaffected",
			cfg:         &config.Config{Rules: allowAll},
			file:        "src/main.go",
			wantApprove: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(tt.cfg, files(tt.file), "alice", nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Approve != tt.wantApprove {
				t.Errorf("Approve: got %v, want %v (%v)", got.Approve, tt.wantApprove, got.FailingFiles)
			}
		})
	}
}

func TestDecideTooManyFiles(t *testing.T) {
	cfg := &config.Config{Rules: []config.Rule{{Paths: []string{"**"}, Allow: []string{"@alice"}}}, MaxChangedFiles: 1}
	big := make([]ChangedFile, cfg.MaxChangedFiles+1)
	for i := range big {
		big[i] = ChangedFile{Path: "playground/alice/f.txt"}
	}
	got, err := Decide(cfg, big, "alice", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Approve {
		t.Fatal("expected refusal for oversized PR")
	}
}

func boolPtr(b bool) *bool { return &b }
