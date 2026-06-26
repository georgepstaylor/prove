package events

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-github/v78/github"

	"github.com/georgepstaylor/prove/internal/engine"
)

// fakeRepo is a programmable RepoService that records the actions taken.
type fakeRepo struct {
	configData  []byte
	configFound bool
	files       []engine.ChangedFile

	approvalFound  bool
	approvalReview int64
	approvalCommit string

	headSHA string // live head reported by PullHeadSHA ("" defaults to "sha1")

	approved        bool
	approvedCommit  string
	approvedBody    string
	dismissed       bool
	dismissedReview int64
	checkConclusion string
	checkTitle      string
	maxChangedFiles int

	teamMember      bool
	autoMergeCalled bool
	autoMergeMethod string
	commented       bool
	commentBody     string

	// call counters for the cache "load test"
	getFileCalls      int
	isTeamMemberCalls int
}

func (f *fakeRepo) GetFile(_ context.Context, _, _, _, _ string) ([]byte, bool, error) {
	f.getFileCalls++
	return f.configData, f.configFound, nil
}

func (f *fakeRepo) ListChangedFiles(_ context.Context, _, _ string, _ int, maxChangedFiles int) ([]engine.ChangedFile, error) {
	f.maxChangedFiles = maxChangedFiles
	return f.files, nil
}

func (f *fakeRepo) FindApproval(_ context.Context, _, _ string, _ int, _ string) (bool, int64, string, error) {
	return f.approvalFound, f.approvalReview, f.approvalCommit, nil
}

func (f *fakeRepo) PullHeadSHA(_ context.Context, _, _ string, _ int) (string, error) {
	if f.headSHA == "" {
		return "sha1", nil // the head used by most tests
	}
	return f.headSHA, nil
}

func (f *fakeRepo) Approve(_ context.Context, _, _ string, _ int, commitID, body string) error {
	f.approved = true
	f.approvedCommit = commitID
	f.approvedBody = body
	return nil
}

func (f *fakeRepo) DismissReview(_ context.Context, _, _ string, _ int, reviewID int64, _ string) error {
	f.dismissed = true
	f.dismissedReview = reviewID
	return nil
}

func (f *fakeRepo) UpsertCheck(_ context.Context, _, _, _, conclusion, title, _ string) error {
	f.checkConclusion = conclusion
	f.checkTitle = title
	return nil
}

func (f *fakeRepo) UpsertComment(_ context.Context, _, _ string, _ int, _, body string) error {
	f.commented = true
	f.commentBody = body
	return nil
}

func (f *fakeRepo) IsTeamMember(_ context.Context, _, _, _ string) (bool, error) {
	f.isTeamMemberCalls++
	return f.teamMember, nil
}

func (f *fakeRepo) EnableAutoMerge(_ context.Context, _, method string) error {
	f.autoMergeCalled = true
	f.autoMergeMethod = method
	return nil
}

type fakeFactory struct{ repo *fakeRepo }

func (f fakeFactory) InstallationClient(int64) (RepoService, error) { return f.repo, nil }
func (f fakeFactory) BotLogin(context.Context) (string, error)      { return "prove[bot]", nil }

func newApprover(repo *fakeRepo) *Approver {
	return NewApprover(fakeFactory{repo: repo}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

func cf(ps ...string) []engine.ChangedFile {
	res := make([]engine.ChangedFile, len(ps))
	for i, p := range ps {
		res[i] = engine.ChangedFile{Path: p}
	}
	return res
}

func prEvent(action, author, headSHA string) *github.PullRequestEvent {
	return &github.PullRequestEvent{
		Action:       github.Ptr(action),
		Installation: &github.Installation{ID: github.Ptr(int64(1))},
		Repo: &github.Repository{
			Name:          github.Ptr("r"),
			Owner:         &github.User{Login: github.Ptr("o")},
			DefaultBranch: github.Ptr("main"),
		},
		PullRequest: &github.PullRequest{
			Number: github.Ptr(7),
			User:   &github.User{Login: github.Ptr(author)},
			Head:   &github.PullRequestBranch{SHA: github.Ptr(headSHA)},
			Base:   &github.PullRequestBranch{Ref: github.Ptr("main")},
		},
	}
}

// alicePlaygroundRule is a config where alice owns her playground subtree.
var alicePlaygroundRule = []byte("rules:\n  - paths: [\"playground/alice/\"]\n    allow: [\"@alice\"]\n")

// carolDocsRule is a config that does not cover src/, used for refusal tests.
var carolDocsRule = []byte("rules:\n  - paths: [\"docs/\"]\n    allow: [\"@carol\"]\n")

func TestApproverApprovesAllowedPaths(t *testing.T) {
	repo := &fakeRepo{configFound: true, configData: alicePlaygroundRule, files: cf("playground/alice/notes.md")}
	if err := newApprover(repo).Process(context.Background(), "d1", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.approved {
		t.Fatal("expected approval")
	}
	if repo.approvedCommit != "sha1" {
		t.Errorf("approval not pinned to evaluated commit: got %q, want sha1", repo.approvedCommit)
	}
	if repo.checkConclusion != "success" {
		t.Errorf("check conclusion: got %q, want success", repo.checkConclusion)
	}
}

func TestApproverSkipsApprovalWhenHeadMoved(t *testing.T) {
	// The PR qualifies, but the live head advanced past the evaluated commit
	// between evaluation and approval — prove must NOT approve the moved head.
	repo := &fakeRepo{
		configFound: true,
		configData:  alicePlaygroundRule,
		files:       cf("playground/alice/notes.md"),
		headSHA:     "sha-moved", // live head != evaluated "sha1"
	}
	if err := newApprover(repo).Process(context.Background(), "hm1", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved {
		t.Fatal("must not approve when the head moved since evaluation")
	}
}

func TestApproverRefusesDisallowedPaths(t *testing.T) {
	repo := &fakeRepo{configFound: true, configData: carolDocsRule, files: cf("src/main.go")}
	if err := newApprover(repo).Process(context.Background(), "d2", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved {
		t.Fatal("did not expect approval")
	}
	if repo.checkConclusion != "neutral" {
		t.Errorf("check conclusion: got %q, want neutral", repo.checkConclusion)
	}
}

func TestApproverDismissesStaleApproval(t *testing.T) {
	repo := &fakeRepo{
		configFound:    true,
		configData:     carolDocsRule, // src/ is now unowned by alice
		files:          cf("src/main.go"),
		approvalFound:  true,
		approvalReview: 42,
		approvalCommit: "oldsha",
	}
	if err := newApprover(repo).Process(context.Background(), "d3", prEvent("synchronize", "alice", "sha2")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.dismissed || repo.dismissedReview != 42 {
		t.Fatalf("expected dismissal of review 42, got dismissed=%v id=%d", repo.dismissed, repo.dismissedReview)
	}
}

func TestApproverIdempotentAtSameHead(t *testing.T) {
	repo := &fakeRepo{
		configFound:    true,
		configData:     alicePlaygroundRule,
		files:          cf("playground/alice/notes.md"),
		approvalFound:  true,
		approvalReview: 1,
		approvalCommit: "sha1",
	}
	if err := newApprover(repo).Process(context.Background(), "d4", prEvent("synchronize", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved {
		t.Fatal("should not re-approve the same head SHA")
	}
}

func TestApproverIgnoresDraft(t *testing.T) {
	repo := &fakeRepo{configFound: true, configData: alicePlaygroundRule, files: cf("playground/alice/notes.md")}
	ev := prEvent("opened", "alice", "sha1")
	ev.PullRequest.Draft = github.Ptr(true)
	if err := newApprover(repo).Process(context.Background(), "d5", ev); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved || repo.checkConclusion != "" {
		t.Fatal("draft PRs should be ignored entirely")
	}
}

func TestApproverIgnoresIrrelevantAction(t *testing.T) {
	repo := &fakeRepo{configFound: true, configData: alicePlaygroundRule, files: cf("playground/alice/notes.md")}
	if err := newApprover(repo).Process(context.Background(), "d6", prEvent("labeled", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved || repo.checkConclusion != "" {
		t.Fatal("irrelevant actions should be ignored")
	}
}

// bumpFiles is a go.mod change whose diff parses to a patch-level bump.
var bumpFiles = []engine.ChangedFile{{Path: "go.mod", Patch: "-\tgithub.com/foo/bar v1.0.0\n+\tgithub.com/foo/bar v1.0.1"}}

func TestApproverApprovesSemverRule(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("rules:\n  - paths: [\"go.mod\", \"go.sum\"]\n    allow: [\"@dependabot[bot]\"]\n    semver: [patch, minor]\n"),
		files:       bumpFiles,
	}
	if err := newApprover(repo).Process(context.Background(), "sv1", prEvent("opened", "dependabot[bot]", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.approved {
		t.Fatal("expected patch bump (from the diff) by the trusted bot to be approved")
	}
}

func TestApproverRefusesSemverRuleForUntrustedAuthor(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("rules:\n  - paths: [\"go.mod\", \"go.sum\"]\n    allow: [\"@dependabot[bot]\"]\n    semver: [patch, minor]\n"),
		files:       bumpFiles,
	}
	// A real patch bump, but by an untrusted author — must NOT be approved.
	if err := newApprover(repo).Process(context.Background(), "sv2", prEvent("opened", "mallory", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved {
		t.Fatal("untrusted author must not be approved even for a genuine bump")
	}
}

func TestApproverResolvesTeamMembership(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("rules:\n  - paths: [\"docs/\"]\n    allow: [\"@org/writers\"]\n"),
		files:       cf("docs/guide.md"),
		teamMember:  true,
	}
	if err := newApprover(repo).Process(context.Background(), "t1", prEvent("opened", "bob", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.approved {
		t.Fatal("expected team member to be approved")
	}
}

func TestApproverEnablesAutoMerge(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("rules:\n  - paths: [\"playground/alice/\"]\n    allow: [\"@alice\"]\nauto_merge:\n  enabled: true\n  method: squash\n"),
		files:       cf("playground/alice/notes.md"),
	}
	if err := newApprover(repo).Process(context.Background(), "m1", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.approved {
		t.Fatal("expected approval")
	}
	if !repo.autoMergeCalled || repo.autoMergeMethod != "squash" {
		t.Fatalf("expected auto-merge squash, got called=%v method=%q", repo.autoMergeCalled, repo.autoMergeMethod)
	}
}

func TestApproverDryRunCommentsInsteadOfApproving(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("mode: dry_run\nrules:\n  - paths: [\"playground/alice/\"]\n    allow: [\"@alice\"]\n"),
		files:       cf("playground/alice/notes.md"),
	}
	if err := newApprover(repo).Process(context.Background(), "dr1", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved {
		t.Fatal("dry run must not approve")
	}
	if !repo.commented {
		t.Fatal("dry run should post a comment")
	}
	if !strings.Contains(repo.commentBody, "would be auto-approved") {
		t.Errorf("comment should say it would approve, got: %q", repo.commentBody)
	}
}

func TestApproverDryRunCommentsOnRefusal(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("mode: dry_run\nrules:\n  - paths: [\"docs/\"]\n    allow: [\"@carol\"]\n"),
		files:       cf("src/main.go"),
	}
	if err := newApprover(repo).Process(context.Background(), "dr2", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved || repo.dismissed {
		t.Fatal("dry run must take no action")
	}
	if !repo.commented || !strings.Contains(repo.commentBody, "would not be auto-approved") {
		t.Errorf("comment should say it would not approve, got: %q", repo.commentBody)
	}
}

func TestApproverCommentFlagApprovesAndComments(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("comment: true\nrules:\n  - paths: [\"playground/alice/\"]\n    allow: [\"@alice\"]\n"),
		files:       cf("playground/alice/notes.md"),
	}
	if err := newApprover(repo).Process(context.Background(), "cm1", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.approved {
		t.Fatal("comment flag should still approve (enforce)")
	}
	if !repo.commented || !strings.Contains(repo.commentBody, "Auto-approved") {
		t.Errorf("comment flag should explain the approval, got: %q", repo.commentBody)
	}
}

func TestApproverEnforceDefaultDoesNotComment(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  alicePlaygroundRule, // no mode (enforce), no comment flag
		files:       cf("playground/alice/notes.md"),
	}
	if err := newApprover(repo).Process(context.Background(), "en1", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.approved {
		t.Fatal("enforce mode should approve")
	}
	if repo.commented {
		t.Fatal("without comment flag prove should not comment")
	}
}

func TestApproverInvalidConfig(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("auto_merge:\n  method: bogus\n"),
		files:       cf("playground/alice/notes.md"),
	}
	if err := newApprover(repo).Process(context.Background(), "d7", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.approved {
		t.Fatal("must not approve when config is invalid")
	}
	if repo.checkTitle != "Invalid prove config" {
		t.Errorf("check title: got %q", repo.checkTitle)
	}
}

func TestApproverPassesConfiguredMaxChangedFiles(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  []byte("max_changed_files: 7\ndirectory_matching_rules: [\"playground/{{username}}/**/*\"]\n"),
		files:       cf("playground/alice/notes.md"),
	}
	if err := newApprover(repo).Process(context.Background(), "d8", prEvent("opened", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if repo.maxChangedFiles != 7 {
		t.Errorf("maxChangedFiles: got %d, want 7", repo.maxChangedFiles)
	}
}

// teamRuleConfig allows a team to self-approve everything (drives IsTeamMember).
var teamRuleConfig = []byte("rules:\n  - paths: [\"**\"]\n    allow: [\"@org/writers\"]\n")

func TestApproverCachesConfigAndTeams(t *testing.T) {
	repo := &fakeRepo{
		configFound: true,
		configData:  teamRuleConfig,
		files:       cf("src/main.go"),
		teamMember:  true,
	}
	a := newApprover(repo)
	for i := 0; i < 50; i++ {
		ev := prEvent("synchronize", "alice", "sha"+strconv.Itoa(i)) // distinct heads
		if err := a.Process(context.Background(), "d"+strconv.Itoa(i), ev); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}
	if repo.getFileCalls != 1 {
		t.Errorf("config not cached: getFileCalls = %d, want 1", repo.getFileCalls)
	}
	if repo.isTeamMemberCalls != 1 {
		t.Errorf("team membership not cached: isTeamMemberCalls = %d, want 1", repo.isTeamMemberCalls)
	}
}

func TestApproverConcurrentSamePRSerialized(t *testing.T) {
	// Two near-simultaneous events for the SAME PR must be serialized by the
	// per-PR lock; under -race a missing lock would flag the shared fakeRepo writes.
	repo := &fakeRepo{configFound: true, configData: alicePlaygroundRule, files: cf("playground/alice/notes.md")}
	repo.configData = []byte("comment: true\nrules:\n  - paths: [\"playground/alice/\"]\n    allow: [\"@alice\"]\n")
	a := newApprover(repo)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = a.Process(context.Background(), "c"+strconv.Itoa(i), prEvent("opened", "alice", "sha1"))
		}(i)
	}
	wg.Wait()

	if !repo.approved {
		t.Fatal("expected approval after concurrent processing")
	}
}

func reviewEvent(action, author, headSHA string) *github.PullRequestReviewEvent {
	return &github.PullRequestReviewEvent{
		Action:       github.Ptr(action),
		Installation: &github.Installation{ID: github.Ptr(int64(1))},
		Repo: &github.Repository{
			Name:          github.Ptr("r"),
			Owner:         &github.User{Login: github.Ptr("o")},
			DefaultBranch: github.Ptr("main"),
		},
		PullRequest: &github.PullRequest{
			Number: github.Ptr(7),
			User:   &github.User{Login: github.Ptr(author)},
			Head:   &github.PullRequestBranch{SHA: github.Ptr(headSHA)},
			Base:   &github.PullRequestBranch{Ref: github.Ptr("main")},
		},
	}
}

func TestApproverReReviewsOnDismissal(t *testing.T) {
	// A dismissed review on an eligible head must still trigger act() (re-approval).
	repo := &fakeRepo{configFound: true, configData: alicePlaygroundRule, files: cf("playground/alice/notes.md")}
	if err := newApprover(repo).Process(context.Background(), "rd1", reviewEvent("dismissed", "alice", "sha1")); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !repo.approved {
		t.Fatal("dismissed review should trigger re-approval")
	}
}
