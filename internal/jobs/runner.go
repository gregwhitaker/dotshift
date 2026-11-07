package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
)

// APIKeyEnv is the variable the agent reads the router key from.
const APIKeyEnv = "DOTSHIFT_API_KEY"

// promptSuffix tells the agent that committing is handled for it. Codex's
// sandbox can't write the worktree's git metadata, which lives in the main
// repository, so the runner commits instead.
const promptSuffix = "\n\nWhen you're done, leave your changes in the working tree. dotshift commits them to this branch for you."

// Runner runs jobs in this process. It implements Service.
type Runner struct {
	Config  *config.Config
	Store   *Store
	Agent   agent.Agent
	BaseURL string // router endpoint the agent calls
	APIKey  string // router master key
	// StateDir holds job logs and, unless jobs.worktree_dir says otherwise,
	// job worktrees.
	StateDir string
	// Echo, when set, also receives every job's agent output.
	Echo io.Writer

	mu       sync.Mutex
	running  map[string]*active
	stopping bool
}

type active struct {
	cancel context.CancelFunc
	done   chan struct{}
	goal   bool
}

var _ Service = (*Runner)(nil)

// Repos implements Service.
func (r *Runner) Repos(ctx context.Context) ([]RepoInfo, error) {
	repos, err := r.allowedRepos(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RepoInfo, 0, len(repos))
	for name, repo := range repos {
		_, configured := r.Config.Repos[name]
		out = append(out, RepoInfo{Name: name, Path: expandHome(repo.Path), HasTests: repo.Test != "", TestCommand: repo.Test, Managed: !configured})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// resolveRepo finds the allowlisted repo a request names, by name or path.
func (r *Runner) resolveRepo(ctx context.Context, ref string) (string, config.Repo, error) {
	if ref == "" {
		return "", config.Repo{}, errors.New("no repo given")
	}
	repos, err := r.allowedRepos(ctx)
	if err != nil {
		return "", config.Repo{}, err
	}
	if repo, ok := repos[ref]; ok {
		return ref, repo, nil
	}
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	want, err := filepath.Abs(expandHome(ref))
	if err == nil {
		want = realPath(want)
		for _, name := range names {
			repo := repos[name]
			if realPath(expandHome(repo.Path)) == want {
				return name, repo, nil
			}
		}
	}
	if len(names) == 0 {
		return "", config.Repo{}, fmt.Errorf("repo %q is not allowlisted; use create_project or register_project under projects.root, or add it under repos: in dotshift.yaml", ref)
	}
	return "", config.Repo{}, fmt.Errorf("repo %q is not allowlisted (allowed: %s)", ref, strings.Join(names, ", "))
}

// Submit implements Service.
func (r *Runner) Submit(ctx context.Context, req Request) (*Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return nil, errors.New("dotshift is stopping")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("no instructions given")
	}
	var parent *Job
	if req.Continue != "" {
		p, err := r.Get(ctx, req.Continue)
		if err != nil {
			return nil, err
		}
		if !p.Status.Terminal() {
			return nil, fmt.Errorf("job %s is still %s; wait for it to finish, or cancel it, before following up", p.ID, p.Status)
		}
		if p.Worktree != "" {
			return nil, fmt.Errorf("job %s left its worktree at %s; remove it with `dotshift clean %s` before following up", p.ID, p.Worktree, p.ID)
		}
		parent = p
		req.Repo, req.Branch, req.Base = p.Repo, p.Branch, p.BaseCommit
		if req.Model == "" {
			req.Model = p.Model
		}
	}
	name, repo, err := r.resolveRepo(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	if req.MaxIterations < 0 || (!req.Goal && req.MaxIterations != 0) {
		return nil, errors.New("max_iterations must be non-negative and is only supported for goals")
	}
	if req.Goal && strings.TrimSpace(repo.Test) == "" {
		return nil, fmt.Errorf("repos.%s.test must be configured before starting a goal", name)
	}
	if !req.Goal && (req.Strategy != "" || req.ReviewerModel != "" || req.EscalateAfter != 0 || req.MaxCostUSD != nil) {
		return nil, errors.New("model strategies, reviewers, and spending caps are only supported for project goals")
	}
	model := req.Model
	if model == "" {
		model = r.Config.Jobs.Model
	}
	if _, ok := r.Config.Aliases[model]; !ok {
		return nil, fmt.Errorf("model %q is not an alias (have: %s)", model, strings.Join(r.Config.AliasNames(), ", "))
	}
	strategy := req.Strategy
	reviewer := req.ReviewerModel
	escalateAfter := req.EscalateAfter
	maxCost := r.Config.Jobs.MaxCostUSD
	if req.Goal {
		if strategy == "" {
			strategy = r.Config.Jobs.Strategy
		}
		if strategy == "" {
			strategy = config.StrategyAlias
		}
		if _, err := r.Config.StrategyModels(model, strategy); err != nil {
			return nil, err
		}
		if reviewer == "" {
			reviewer = r.Config.Jobs.ReviewerModel
		}
		if err := r.Config.ValidateReviewer(model, strategy, reviewer); err != nil {
			return nil, err
		}
		if escalateAfter < 0 || (escalateAfter != 0 && strategy != config.StrategyAdaptive) {
			return nil, errors.New("escalate_after must be positive and requires the adaptive strategy")
		}
		if escalateAfter == 0 {
			escalateAfter = r.Config.Jobs.EscalateAfter
		}
		if escalateAfter == 0 {
			escalateAfter = 2
		}
		if req.MaxCostUSD != nil {
			maxCost = *req.MaxCostUSD
		}
		if err := r.validateGoalBudget(maxCost); err != nil {
			return nil, err
		}
	}
	top, err := git(ctx, expandHome(repo.Path), "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("repos.%s is not a git repository: %w", name, err)
	}
	base := req.Base
	if base == "" {
		base = "HEAD"
	}
	baseCommit, err := git(ctx, top, "rev-parse", "--verify", "--quiet", base+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("base %q is not a commit in %s", base, name)
	}

	id, err := newID()
	if err != nil {
		return nil, err
	}
	branch := req.Branch
	if branch == "" {
		branch = "dotshift/" + id
	}
	if _, err := git(ctx, top, "check-ref-format", "--branch", branch); err != nil {
		return nil, fmt.Errorf("%q is not a valid branch name", branch)
	}
	_, branchErr := git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	branchExists := branchErr == nil
	if branchExists && parent == nil {
		return nil, fmt.Errorf("branch %s already exists in %s", branch, name)
	}

	wtDir := r.Config.Jobs.WorktreeDir
	if wtDir == "" {
		wtDir = filepath.Join(r.StateDir, "worktrees")
	}
	wtDir, err = filepath.Abs(expandHome(wtDir))
	if err != nil {
		return nil, err
	}
	if within(realPath(wtDir), realPath(top)) {
		return nil, fmt.Errorf("worktrees would be created inside %s's working copy (%s); set jobs.worktree_dir to a folder outside it", name, wtDir)
	}
	logDir := filepath.Join(r.StateDir, "jobs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(wtDir, 0o700); err != nil {
		return nil, err
	}

	j := &Job{
		ID: id, Repo: name, RepoPath: top, Prompt: req.Prompt, Model: model,
		ExecutionMode: r.Config.ExecutionMode(),
		Branch:        branch, BaseCommit: baseCommit, Worktree: filepath.Join(wtDir, id),
		Status: Queued, LogPath: filepath.Join(logDir, id+".log"),
		RunnerPID: os.Getpid(), CreatedAt: time.Now(),
	}
	if req.Goal {
		j.Goal = &GoalProgress{Phase: goalWorking, MaxIterations: req.MaxIterations}
		if strategy != config.StrategyAlias || reviewer != "" || maxCost > 0 || r.Config.UsesSubscriptions() {
			j.Goal.ModelSelection = &ModelSelection{Strategy: strategy}
		}
		if reviewer != "" {
			j.Goal.ReviewerModel, j.Goal.ReviewerSelection = reviewer, &ModelSelection{Strategy: config.StrategyAlias}
		}
		if strategy == config.StrategyAdaptive {
			j.Goal.Adaptive = &AdaptiveProgress{EscalateAfter: escalateAfter}
		}
		if maxCost > 0 {
			j.Goal.Budget = &BudgetProgress{LimitUSD: maxCost, RemainingUSD: maxCost, Models: []ModelSpend{}}
		}
		j.RunToken, err = newID()
		if err != nil {
			return nil, err
		}
	}
	add := []string{"worktree", "add", "--quiet", "-b", branch, j.Worktree, baseCommit}
	prompt := req.Prompt
	if parent != nil {
		j.Parent = parent.ID
		prompt = r.followUpPrompt(ctx, parent, req.Prompt)
		// A follow-up adds to the earlier job's branch. The branch is gone
		// only when that job made no commits, so it starts again from the base.
		if branchExists {
			add = []string{"worktree", "add", "--quiet", j.Worktree, branch}
		}
	}
	if _, err := git(ctx, top, add...); err != nil {
		return nil, err
	}
	if err := r.Store.Save(ctx, j); err != nil {
		r.removeWorktree(context.Background(), j, true)
		return nil, err
	}

	snapshot := cloneJob(j)
	r.startLocked(j, repo, prompt)
	return snapshot, nil
}

// startLocked starts a worker while mu is held, so shutdown cannot miss it.
func (r *Runner) startLocked(j *Job, repo config.Repo, prompt string) {
	jobCtx, cancel := context.WithCancel(context.Background())
	a := &active{cancel: cancel, done: make(chan struct{}), goal: j.Goal != nil}
	if r.running == nil {
		r.running = map[string]*active{}
	}
	r.running[j.ID] = a
	go func() {
		defer close(a.done)
		defer cancel()
		if j.Goal != nil {
			r.runGoal(jobCtx, j)
		} else {
			r.run(jobCtx, j, repo, prompt)
		}
		r.mu.Lock()
		delete(r.running, j.ID)
		r.mu.Unlock()
	}()
}

func cloneJob(j *Job) *Job {
	snapshot := *j
	if j.Goal != nil {
		progress := *j.Goal
		snapshot.Goal = &progress
		if j.Goal.ModelSelection != nil {
			selection := *j.Goal.ModelSelection
			snapshot.Goal.ModelSelection = &selection
		}
		if j.Goal.ReviewerSelection != nil {
			selection := *j.Goal.ReviewerSelection
			snapshot.Goal.ReviewerSelection = &selection
		}
		if j.Goal.Adaptive != nil {
			adaptive := *j.Goal.Adaptive
			snapshot.Goal.Adaptive = &adaptive
		}
		if j.Goal.Budget != nil {
			budget := *j.Goal.Budget
			budget.Models = append([]ModelSpend(nil), budget.Models...)
			snapshot.Goal.Budget = &budget
		}
	}
	return &snapshot
}

// run takes a recorded job through the agent, tests and commit.
func (r *Runner) run(ctx context.Context, j *Job, repo config.Repo, prompt string) {
	bg := context.Background() // record the outcome even after cancellation
	finish := func(st Status, msg string) {
		if cur, err := r.Store.Get(bg, j.ID); err == nil && cur.Status == Cancelled {
			st, msg = Cancelled, cur.Error
		}
		j.Status, j.Error, j.FinishedAt, j.AgentPID = st, msg, time.Now(), 0
		if err := r.Store.Save(bg, j); err != nil {
			fmt.Fprintf(os.Stderr, "dotshift: saving job %s: %v\n", j.ID, err)
		}
	}

	logFile, err := os.OpenFile(j.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		finish(Failed, err.Error())
		return
	}
	defer logFile.Close()
	var out io.Writer = logFile
	if r.Echo != nil {
		out = io.MultiWriter(logFile, r.Echo)
	}

	agentCtx, cancelTimeout := context.WithTimeout(ctx, r.Config.Jobs.Timeout.Duration)
	defer cancelTimeout()
	lastMessage := filepath.Join(filepath.Dir(j.LogPath), j.ID+".summary")
	model := j.Model
	if r.Config.UsesSubscriptions() {
		model = r.Config.Aliases[j.Model][0]
	}
	cmd, finishAgent, err := agent.Prepare(agentCtx, r.Agent, agent.Task{
		Dir: j.Worktree, Prompt: prompt, Model: model, BaseURL: r.BaseURL,
		APIKeyEnv: APIKeyEnv, LastMessageFile: lastMessage,
	}, out, cancelTimeout)
	if err != nil {
		finish(Failed, err.Error())
		return
	}
	cmd.Stdin = strings.NewReader(prompt + promptSuffix)
	if !r.Config.UsesSubscriptions() {
		cmd.Env = append(cmd.Environ(), APIKeyEnv+"="+r.APIKey)
	}
	detach(cmd)
	cmd.WaitDelay = 10 * time.Second

	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			finish(Cancelled, "cancelled")
			return
		}
		finish(Failed, fmt.Sprintf("starting %s: %v", r.Agent.Name(), err))
		return
	}
	j.StartedAt, j.AgentPID = time.Now(), cmd.Process.Pid
	if ok, err := r.Store.MarkRunning(bg, j); err != nil {
		fmt.Fprintf(os.Stderr, "dotshift: saving job %s: %v\n", j.ID, err)
	} else if !ok {
		_ = terminate(cmd.Process.Pid) // cancelled while starting
	}
	j.Status = Running
	waitErr := cmd.Wait()
	if err := finishAgent(); err != nil {
		waitErr = err
	}
	if cmd.ProcessState != nil {
		j.ExitCode = cmd.ProcessState.ExitCode()
	}
	if data, err := os.ReadFile(lastMessage); err == nil {
		j.Summary = strings.TrimSpace(string(data))
		os.Remove(lastMessage)
	}

	// Another process may have cancelled the job through the store.
	if cur, err := r.Store.Get(bg, j.ID); err == nil && cur.Status == Cancelled {
		finish(Cancelled, cur.Error)
		return
	}
	switch {
	case ctx.Err() != nil:
		finish(Cancelled, "cancelled")
		return
	case errors.Is(agentCtx.Err(), context.DeadlineExceeded):
		finish(Failed, fmt.Sprintf("%s did not finish within %s", r.Agent.Name(), r.Config.Jobs.Timeout.Duration))
		return
	case waitErr != nil:
		finish(Failed, fmt.Sprintf("%s exited: %v (see %s)", r.Agent.Name(), waitErr, j.LogPath))
		return
	}

	if repo.Test != "" {
		r.runTests(bg, j, repo.Test, out)
	}
	if _, err := commitAll(bg, j.Worktree, commitMessage(j)); err != nil {
		finish(Failed, "committing the agent's changes: "+err.Error())
		return
	}
	if n, err := git(bg, j.Worktree, "rev-list", "--count", j.BaseCommit+"..HEAD"); err == nil {
		fmt.Sscan(n, &j.Commits)
	}
	j.DiffStat, _ = git(bg, j.Worktree, "diff", "--stat", j.BaseCommit, "HEAD")
	// The branch keeps the work; the worktree was only somewhere to do it.
	r.removeWorktree(bg, j, j.Commits == 0)
	finish(Done, "")
}

// runTests runs the repo's test command in the worktree and records the result.
func (r *Runner) runTests(ctx context.Context, j *Job, command string, out io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, r.Config.Jobs.Timeout.Duration)
	defer cancel()
	fmt.Fprintf(out, "\n--- dotshift: running tests: %s\n", command)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = j.Worktree
	tail := &tailBuffer{max: 4000}
	cmd.Stdout = io.MultiWriter(out, tail)
	cmd.Stderr = cmd.Stdout
	if err := cmd.Run(); err != nil {
		j.TestResult = "failed"
	} else {
		j.TestResult = "passed"
	}
	j.TestOutput = tail.String()
	fmt.Fprintf(out, "--- dotshift: tests %s\n", j.TestResult)
}

// removeWorktree deletes the job's worktree, and its branch too when asked.
func (r *Runner) removeWorktree(ctx context.Context, j *Job, deleteBranch bool) {
	if _, err := git(ctx, j.RepoPath, "worktree", "remove", "--force", j.Worktree); err == nil {
		j.Worktree = ""
	}
	if deleteBranch {
		_, _ = git(ctx, j.RepoPath, "branch", "-D", j.Branch)
	}
}

// Get implements Service.
func (r *Runner) Get(ctx context.Context, id string) (*Job, error) {
	j, err := r.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	r.reconcile(ctx, j)
	return j, nil
}

// List implements Service.
func (r *Runner) List(ctx context.Context) ([]*Job, error) {
	list, err := r.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, j := range list {
		r.reconcile(ctx, j)
	}
	return list, nil
}

// reconcile marks a job failed if the process running it has gone away.
func (r *Runner) reconcile(ctx context.Context, j *Job) {
	if j.Status.Terminal() || alive(j.RunnerPID) {
		return
	}
	if j.Goal != nil {
		// Reading status does not take ownership. serve claims the persisted
		// goal and resumes its worktree when it starts.
		j.Status = Queued
		return
	}
	j.Status, j.Error, j.FinishedAt = Failed, "dotshift stopped while the job was running", time.Now()
	_ = r.Store.Save(ctx, j)
}

// Cancel implements Service. It works for jobs run by this process and by
// other dotshift processes sharing the store.
func (r *Runner) Cancel(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if j.Status.Terminal() && !(j.Goal != nil && j.Status == Blocked) {
		return fmt.Errorf("job %s already %s", id, j.Status)
	}
	if j.Goal != nil {
		if err := r.Store.cancelGoal(ctx, j); err != nil {
			return err
		}
		if a := r.running[id]; a != nil {
			a.cancel()
		}
		return terminate(j.AgentPID)
	}
	if err := r.Store.SetStatus(ctx, id, Cancelled, "cancelled"); err != nil {
		return err
	}
	a := r.running[id]
	if a != nil {
		a.cancel()
		return nil
	}
	// Read the agent's PID after marking the job cancelled: a runner that
	// starts its agent from here on sees the mark and stops it itself.
	if j, err = r.Store.Get(ctx, id); err != nil {
		return err
	}
	return terminate(j.AgentPID)
}

// Wait implements Service.
func (r *Runner) Wait(ctx context.Context, id string) (*Job, error) {
	r.mu.Lock()
	a := r.running[id]
	r.mu.Unlock()
	if a != nil {
		select {
		case <-a.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return r.Get(ctx, id)
	}
	// Run by another process: poll the store.
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		j, err := r.Get(ctx, id)
		if err != nil || j.Status.Terminal() {
			return j, err
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Clean removes the worktree a failed or cancelled job left behind, and its
// branch if the branch has no commits of its own.
func (r *Runner) Clean(ctx context.Context, id string) (*Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !j.Status.Terminal() {
		return nil, fmt.Errorf("job %s is still %s; cancel it first", id, j.Status)
	}
	if j.Worktree == "" {
		return j, nil
	}
	status, message, finished := j.Status, j.Error, j.FinishedAt
	if j.Goal != nil {
		if r.running[id] != nil || alive(j.RunnerPID) {
			return nil, errors.New("goal worker is still stopping; wait before cleaning its worktree")
		}
		// Claim the goal while cleaning so a concurrent resume cannot start
		// an agent in the worktree that is about to be removed.
		if err := r.Store.claimGoal(ctx, j); err != nil {
			return nil, err
		}
	}
	n, _ := git(ctx, j.RepoPath, "rev-list", "--count", j.BaseCommit+".."+j.Branch)
	r.removeWorktree(ctx, j, n == "0")
	if j.Goal != nil {
		j.Status, j.Error, j.FinishedAt = status, message, finished
		j.RunnerPID, j.AgentPID = 0, 0
		if err := r.Store.saveGoal(context.Background(), j); err != nil {
			return nil, err
		}
		if j.Worktree != "" {
			return j, fmt.Errorf("could not remove goal worktree %s", j.Worktree)
		}
		return j, nil
	}
	return j, r.Store.Save(ctx, j)
}

// followUpPrompt tells a follow-up job's agent what came before: the agent
// starts fresh each time, so earlier instructions and notes are its memory.
func (r *Runner) followUpPrompt(ctx context.Context, parent *Job, input string) string {
	var chain []*Job
	for j := parent; j != nil && len(chain) < 5; {
		chain = append(chain, j)
		if j.Parent == "" {
			break
		}
		j, _ = r.Store.Get(ctx, j.Parent)
	}
	var b strings.Builder
	b.WriteString("You are following up on earlier work on this branch. The commits already on it are that work.\n")
	for i := len(chain) - 1; i >= 0; i-- {
		j := chain[i]
		fmt.Fprintf(&b, "\nEarlier instructions:\n%s\n", strings.TrimSpace(j.Prompt))
		if j.Summary != "" {
			fmt.Fprintf(&b, "\nWhat you reported then:\n%s\n", strings.TrimSpace(j.Summary))
		}
		if j.Status != Done {
			fmt.Fprintf(&b, "\nThat run ended %s: %s\n", j.Status, j.Error)
		}
	}
	fmt.Fprintf(&b, "\nNow:\n%s", strings.TrimSpace(input))
	return b.String()
}

// Shutdown cancels the jobs this process is running and waits until each has
// recorded how it ended, or ctx ends.
func (r *Runner) Shutdown(ctx context.Context, reason string) {
	r.mu.Lock()
	r.stopping = true
	running := make(map[string]*active, len(r.running))
	for id, a := range r.running {
		running[id] = a
	}
	r.mu.Unlock()
	for id, a := range running {
		if !a.goal {
			_ = r.Store.SetStatus(ctx, id, Cancelled, reason)
		}
		a.cancel()
	}
	for _, a := range running {
		select {
		case <-a.done:
		case <-ctx.Done():
			return
		}
	}
}

// Diff returns the changes a job's branch makes on top of its base commit,
// cut to at most max bytes.
func Diff(ctx context.Context, j *Job, max int) (patch string, truncated bool, err error) {
	if j.Commits == 0 {
		return "", false, nil
	}
	patch, err = git(ctx, j.RepoPath, "diff", j.BaseCommit, "refs/heads/"+j.Branch)
	if err != nil {
		return "", false, err
	}
	if len(patch) > max {
		return patch[:max], true, nil
	}
	return patch, false, nil
}

func commitMessage(j *Job) string {
	title, _, _ := strings.Cut(strings.TrimSpace(j.Prompt), "\n")
	if len(title) > 64 {
		title = title[:61] + "..."
	}
	return fmt.Sprintf("dotshift: %s\n\nJob %s, model %s.\n\n%s\n", title, j.ID, j.Model, strings.TrimSpace(j.Prompt))
}

func newID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// realPath resolves symlinks where it can, so /tmp and /private/tmp compare
// equal on macOS.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	p = filepath.Clean(p)
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
