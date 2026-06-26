package events

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v78/github"

	"github.com/georgepstaylor/prove/internal/cache"
	"github.com/georgepstaylor/prove/internal/config"
	"github.com/georgepstaylor/prove/internal/engine"
	"github.com/georgepstaylor/prove/internal/metrics"
)

// RepoService is the slice of GitHub behaviour the approver needs, scoped to one
// installation. It is an interface so the approver can be tested with fakes.
type RepoService interface {
	GetFile(ctx context.Context, owner, repo, path, ref string) ([]byte, bool, error)
	ListChangedFiles(ctx context.Context, owner, repo string, number, maxChangedFiles int) ([]engine.ChangedFile, error)
	FindApproval(ctx context.Context, owner, repo string, number int, botLogin string) (found bool, reviewID int64, commitID string, err error)
	PullHeadSHA(ctx context.Context, owner, repo string, number int) (string, error)
	Approve(ctx context.Context, owner, repo string, number int, commitID, body string) error
	DismissReview(ctx context.Context, owner, repo string, number int, reviewID int64, message string) error
	UpsertCheck(ctx context.Context, owner, repo, headSHA, conclusion, title, summary string) error
	UpsertComment(ctx context.Context, owner, repo string, number int, marker, body string) error
	IsTeamMember(ctx context.Context, org, slug, user string) (bool, error)
	EnableAutoMerge(ctx context.Context, prNodeID, method string) error
}

// ClientFactory mints per-installation RepoServices and exposes the bot's review identity.
type ClientFactory interface {
	InstallationClient(installationID int64) (RepoService, error)
	BotLogin(ctx context.Context) (string, error)
}

// prLockShards sizes the fixed mutex pool serializing same-PR processing within a
// replica (bounded — no per-key growth).
const prLockShards = 256

// Approver is the production Processor: it evaluates pull requests against the
// repo config and approves, dismisses, or explains accordingly.
type Approver struct {
	factory ClientFactory
	logger  *slog.Logger
	metrics *metrics.Metrics

	// configCache: owner/name@baseRef → parsed config (TTL ~60s).
	// teamCache:   org/slug#user → membership (TTL ~10m).
	configCache cache.Cache[string, *config.Config]
	teamCache   cache.Cache[string, bool]

	prLocks [prLockShards]sync.Mutex

	botOnce  sync.Once
	botLogin string
	botErr   error
}

// NewApprover builds an Approver. metrics may be nil.
func NewApprover(factory ClientFactory, logger *slog.Logger, m *metrics.Metrics) *Approver {
	return &Approver{
		factory:     factory,
		logger:      logger,
		metrics:     m,
		configCache: cache.NewTTL[string, *config.Config](60*time.Second, 1024),
		teamCache:   cache.NewTTL[string, bool](10*time.Minute, 4096),
	}
}

// lockPR serializes same-PR processing within this replica via a sharded mutex
// pool, closing the duplicate-comment race. Returns the unlock func.
func (a *Approver) lockPR(key string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	mu := &a.prLocks[h.Sum32()%prLockShards]
	mu.Lock()
	return mu.Unlock
}

// Process dispatches relevant pull-request events to evaluation.
func (a *Approver) Process(ctx context.Context, delivery string, event any) error {
	switch e := event.(type) {
	case *github.PullRequestEvent:
		if !relevantPRAction(e.GetAction()) {
			return nil
		}
		return a.evaluate(ctx, delivery, e.GetInstallation().GetID(), e.GetRepo(), e.GetPullRequest())
	case *github.PullRequestReviewEvent:
		// Re-evaluate on dismissal (e.g. of prove's own approval) to keep state consistent.
		if e.GetAction() != "dismissed" {
			return nil
		}
		return a.evaluate(ctx, delivery, e.GetInstallation().GetID(), e.GetRepo(), e.GetPullRequest())
	default:
		return nil
	}
}

func relevantPRAction(action string) bool {
	switch action {
	case "opened", "reopened", "synchronize", "ready_for_review":
		return true
	default:
		return false
	}
}

func (a *Approver) evaluate(ctx context.Context, delivery string, installID int64, repo *github.Repository, pr *github.PullRequest) error {
	if pr.GetDraft() {
		return nil
	}

	owner := repo.GetOwner().GetLogin()
	name := repo.GetName()
	number := pr.GetNumber()
	author := pr.GetUser().GetLogin()
	headSHA := pr.GetHead().GetSHA()
	baseBranch := repo.GetDefaultBranch()
	if baseBranch == "" {
		baseBranch = pr.GetBase().GetRef()
	}

	unlock := a.lockPR(owner + "/" + name + "#" + strconv.Itoa(number))
	defer unlock()

	start := time.Now()
	defer func() { a.metrics.ObserveEvaluation(time.Since(start)) }()

	log := a.logger.With("delivery", delivery, "repo", owner+"/"+name, "pr", number, "author", author)

	client, err := a.factory.InstallationClient(installID)
	if err != nil {
		a.metrics.IncError("client")
		return fmt.Errorf("installation client: %w", err)
	}
	botLogin, err := a.cachedBotLogin(ctx)
	if err != nil {
		a.metrics.IncError("bot_login")
		return fmt.Errorf("bot login: %w", err)
	}

	// Security: config is read from the base branch, never the PR head, so an
	// author cannot rewrite the rules in the change they want approved.
	cfg, err := a.loadConfig(ctx, client, owner, name, baseBranch)
	if err != nil {
		log.Warn("invalid prove config", "err", err)
		a.metrics.IncDecision("invalid_config")
		summary := fmt.Sprintf("prove could not load `.github/prove.yml` from `%s`:\n\n```\n%s\n```", baseBranch, err.Error())
		return client.UpsertCheck(ctx, owner, name, headSHA, "neutral", "Invalid prove config", summary)
	}

	files, err := client.ListChangedFiles(ctx, owner, name, number, cfg.EffectiveMaxChangedFiles())
	if err != nil {
		a.metrics.IncError("list_files")
		return err
	}

	decision, err := a.decide(ctx, client, cfg, author, files)
	if err != nil {
		a.metrics.IncError("decide")
		return err
	}

	if decision.Approve {
		a.metrics.IncDecision("approved")
	} else {
		a.metrics.IncDecision("rejected")
	}

	conclusion, title, summary := renderCheck(decision)
	if err := client.UpsertCheck(ctx, owner, name, headSHA, conclusion, title, summary); err != nil {
		log.Warn("check run failed", "err", err)
	}

	mode := cfg.EffectiveMode()
	action := "none"

	switch mode {
	case config.ModeDryRun:
		log.Info("dry run; commenting only", "would_approve", decision.Approve)
		if err := client.UpsertComment(ctx, owner, name, number, commentMarker, renderComment(decision, true)); err != nil {
			log.Warn("comment failed", "err", err)
		} else {
			a.metrics.IncAction("commented")
		}
		action = "commented"
	default:
		action, err = a.act(ctx, client, cfg, pr, log, decision, owner, name, number, headSHA, botLogin)
		if err != nil {
			a.metrics.IncError("act")
			return err
		}
		if cfg.Comment {
			if err := client.UpsertComment(ctx, owner, name, number, commentMarker, renderComment(decision, false)); err != nil {
				log.Warn("comment failed", "err", err)
			} else {
				a.metrics.IncAction("commented")
			}
		}
	}

	a.recordDecision(log, DecisionEvent{
		Delivery: delivery, Repo: owner + "/" + name, Number: number, Author: author,
		HeadSHA: headSHA, Mode: string(mode), Approve: decision.Approve,
		FailingCount: len(decision.FailingFiles), Action: action, DurationMs: time.Since(start).Milliseconds(),
	})
	return nil
}

// act applies prove's decision: approve (and optionally auto-merge), or dismiss a
// now-stale approval. Returns the action taken ("approved" | "dismissed" | "none").
func (a *Approver) act(ctx context.Context, client RepoService, cfg *config.Config, pr *github.PullRequest, log *slog.Logger, decision engine.Decision, owner, name string, number int, headSHA, botLogin string) (string, error) {
	found, reviewID, commitID, err := client.FindApproval(ctx, owner, name, number, botLogin)
	if err != nil {
		return "none", err
	}

	if decision.Approve {
		if found && commitID == headSHA {
			log.Info("already approved at head; skipping")
			return "none", nil
		}
		// Guard the evaluate→approve race: only approve if the live head still
		// matches the commit we evaluated. If a new commit landed since, approving
		// would bless code prove never inspected; that push raises its own
		// synchronize event, which re-evaluates the new head.
		liveHead, err := client.PullHeadSHA(ctx, owner, name, number)
		if err != nil {
			return "none", err
		}
		if liveHead != headSHA {
			log.Info("head moved since evaluation; deferring to re-evaluation", "evaluated", headSHA, "live", liveHead)
			return "none", nil
		}
		log.Info("approving")
		// Pin the approval to the evaluated commit, so it is treated as stale once
		// the head advances.
		if err := client.Approve(ctx, owner, name, number, headSHA, approvalBody(decision)); err != nil {
			return "none", err
		}
		a.metrics.IncAction("approved")
		if cfg.AutoMerge.Enabled {
			// Best-effort: a disabled "Allow auto-merge" repo setting makes this
			// fail, which must not fail the whole delivery.
			if err := client.EnableAutoMerge(ctx, pr.GetNodeID(), cfg.AutoMerge.Method); err != nil {
				log.Warn("enable auto-merge failed", "err", err)
			} else {
				a.metrics.IncAction("auto_merge")
			}
		}
		return "approved", nil
	}

	if found {
		log.Info("dismissing stale approval")
		if err := client.DismissReview(ctx, owner, name, number, reviewID,
			"prove: changes no longer qualify for auto-approval"); err != nil {
			return "none", err
		}
		a.metrics.IncAction("dismissed")
		return "dismissed", nil
	}
	return "none", nil
}

// loadConfig returns the repo config from cache or a base-branch fetch. Only
// successful loads are cached (~60s TTL), so a fixed config recovers within a TTL.
func (a *Approver) loadConfig(ctx context.Context, client RepoService, owner, name, baseRef string) (*config.Config, error) {
	key := owner + "/" + name + "@" + baseRef
	if c, ok := a.configCache.Get(key); ok {
		a.metrics.IncCache("config", "hit")
		return c, nil
	}
	a.metrics.IncCache("config", "miss")
	c, err := config.Load(ctx, client, owner, name, baseRef)
	if err != nil {
		return nil, err
	}
	a.configCache.Set(key, c)
	return c, nil
}

// DecisionEvent is the structured record emitted per evaluation.
type DecisionEvent struct {
	Delivery, Repo, Author, HeadSHA, Mode, Action string
	Number, FailingCount                          int
	Approve                                       bool
	DurationMs                                    int64
}

// recordDecision emits the decision as a structured log line; swap the body for a
// durable sink to deliver an audit log. (repo/pr/author are already on log.)
func (a *Approver) recordDecision(log *slog.Logger, ev DecisionEvent) {
	log.Info("decision",
		"approve", ev.Approve, "action", ev.Action, "mode", ev.Mode,
		"failing", ev.FailingCount, "duration_ms", ev.DurationMs)
}

// decide produces the approval decision for a PR, resolving the author's team
// memberships first since rules may grant access by team.
func (a *Approver) decide(ctx context.Context, client RepoService, cfg *config.Config, author string, files []engine.ChangedFile) (engine.Decision, error) {
	authorTeams, err := a.resolveAuthorTeams(ctx, client, cfg, author)
	if err != nil {
		return engine.Decision{}, err
	}
	return engine.Decide(cfg, files, author, authorTeams)
}

// resolveAuthorTeams returns the normalized "org/slug" identifiers of the teams
// (named in any rule's allow list) that the author belongs to.
func (a *Approver) resolveAuthorTeams(ctx context.Context, client RepoService, cfg *config.Config, author string) ([]string, error) {
	seen := map[string]bool{}
	var member []string
	for _, r := range cfg.Rules {
		for _, handle := range r.Allow {
			org, slug, ok := engine.SplitTeam(handle)
			if !ok {
				continue // a @user, not a team
			}
			norm := engine.NormalizeTeam(handle)
			if seen[norm] {
				continue
			}
			seen[norm] = true

			cacheKey := norm + "#" + author
			if isMember, ok := a.teamCache.Get(cacheKey); ok {
				a.metrics.IncCache("team", "hit")
				if isMember {
					member = append(member, norm)
				}
				continue
			}
			a.metrics.IncCache("team", "miss")
			isMember, err := client.IsTeamMember(ctx, org, slug, author)
			if err != nil {
				return nil, err
			}
			a.teamCache.Set(cacheKey, isMember)
			if isMember {
				member = append(member, norm)
			}
		}
	}
	return member, nil
}

func (a *Approver) cachedBotLogin(ctx context.Context) (string, error) {
	a.botOnce.Do(func() {
		a.botLogin, a.botErr = a.factory.BotLogin(ctx)
	})
	return a.botLogin, a.botErr
}

func renderCheck(d engine.Decision) (conclusion, title, summary string) {
	if d.Approve {
		return "success", "Auto-approved by prove", "✅ " + strings.Join(d.Reasons, "; ")
	}
	var b strings.Builder
	b.WriteString("prove did not auto-approve this pull request.\n\n")
	if len(d.Reasons) > 0 {
		b.WriteString(strings.Join(d.Reasons, "; "))
		b.WriteString("\n\n")
	}
	if len(d.FailingFiles) > 0 {
		b.WriteString("| File | Reason |\n|---|---|\n")
		for _, f := range d.FailingFiles {
			fmt.Fprintf(&b, "| `%s` | %s |\n", f.File, f.Reason)
		}
	}
	return "neutral", "Not auto-approved", b.String()
}

func approvalBody(d engine.Decision) string {
	return "✅ prove auto-approved: " + strings.Join(d.Reasons, "; ")
}

// commentMarker is a hidden token used to find and update prove's own status
// comment instead of posting a new one on every push.
const commentMarker = "<!-- prove:status -->"

// renderComment builds prove's PR comment explaining its decision; dryRun makes
// the wording conditional and notes no action was taken.
func renderComment(d engine.Decision, dryRun bool) string {
	var b strings.Builder
	b.WriteString(commentMarker)
	if dryRun {
		b.WriteString("\n### 🔍 prove (dry run)\n\n")
	} else {
		b.WriteString("\n### prove\n\n")
	}

	if d.Approve {
		if dryRun {
			b.WriteString("This PR **would be auto-approved**.\n\n")
		} else {
			b.WriteString("✅ **Auto-approved** — no human review required.\n\n")
		}
		b.WriteString(strings.Join(d.Reasons, "; "))
		b.WriteString("\n")
	} else {
		if dryRun {
			b.WriteString("This PR **would not be auto-approved** — it would still need a human reviewer.\n\n")
		} else {
			b.WriteString("This PR was **not auto-approved** — it needs a human reviewer.\n\n")
		}
		if len(d.Reasons) > 0 {
			b.WriteString(strings.Join(d.Reasons, "; "))
			b.WriteString("\n\n")
		}
		if len(d.FailingFiles) > 0 {
			b.WriteString("| File | Reason |\n|---|---|\n")
			for _, f := range d.FailingFiles {
				fmt.Fprintf(&b, "| `%s` | %s |\n", f.File, f.Reason)
			}
		}
	}

	if dryRun {
		b.WriteString("\n_prove is in dry-run mode and took no action. Set `mode: enforce` in `.github/prove.yml` to enable auto-approval._")
	}
	return b.String()
}
