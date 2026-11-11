package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
)

const (
	goalWorking   = "working"
	goalTesting   = "testing"
	goalReviewing = "reviewing"
)

// GoalProgress is checkpointed after every phase. The original objective is
// Job.Prompt, and the branch/worktree and latest test output live on Job.
type GoalProgress struct {
	Phase             string            `json:"phase"`
	Iterations        int               `json:"iterations"`
	MaxIterations     int               `json:"max_iterations"`
	Failures          int               `json:"consecutive_failures"`
	Input             string            `json:"additional_instructions,omitempty"`
	Report            GoalReport        `json:"report"`
	TestedCommit      string            `json:"tested_commit,omitempty"`
	TestCommand       string            `json:"test_command,omitempty"`
	ModelSelection    *ModelSelection   `json:"model_selection,omitempty"`
	ReviewerModel     string            `json:"reviewer_model,omitempty"`
	ReviewerSelection *ModelSelection   `json:"reviewer_selection,omitempty"`
	Adaptive          *AdaptiveProgress `json:"adaptive,omitempty"`
	Budget            *BudgetProgress   `json:"budget,omitempty"`
}

// GoalReport is the agent's assessment, checked against independently run
// tests and a fresh review before the runner can mark the goal done.
type GoalReport struct {
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	NextSteps string `json:"next_steps"`
}

type ResumeGoalRequest struct {
	ID            string
	Input         string
	Model         string
	Strategy      string
	ReviewerModel *string
	EscalateAfter *int
	MaxCostUSD    *float64
	// RetryUnavailable clears cached API failures for this strategy's pool
	// after the caller has restored credits or access. It does not raise limits.
	RetryUnavailable bool
	MaxIterations    *int
}

// ResumeGoals is called by serve after it has acquired the listening socket.
// The database claim also protects against another process resuming a goal.
func (r *Runner) ResumeGoals(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return errors.New("dotshift is stopping")
	}
	list, err := r.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, j := range list {
		if j.Goal == nil || j.Status.Terminal() || alive(j.RunnerPID) {
			continue
		}
		if err := r.Store.claimGoal(ctx, j); err != nil {
			if errors.Is(err, errGoalStopped) {
				continue
			}
			return err
		}
		r.startLocked(j, config.Repo{}, "")
	}
	return nil
}

// ResumeGoal continues a blocked or cancelled goal in its existing worktree.
// New instructions supplement the original objective and may change the model.
func (r *Runner) ResumeGoal(ctx context.Context, req ResumeGoalRequest) (*Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return nil, errors.New("dotshift is stopping")
	}
	j, err := r.Store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if j.Goal == nil || j.Status == Done {
		return nil, errors.New("resume_goal requires an unfinished project goal")
	}
	if r.running[j.ID] != nil || alive(j.RunnerPID) {
		return nil, errors.New("goal worker is still active; wait for it to stop before resuming")
	}
	modelChanged := req.Model != "" && req.Model != j.Model
	if req.Model != "" {
		j.Model = req.Model
	}
	strategy := r.goalStrategy(j)
	if req.Strategy != "" {
		strategy = req.Strategy
	}
	if modelChanged || strategy != r.goalStrategy(j) {
		if _, err := r.Config.StrategyModels(j.Model, strategy); err != nil {
			return nil, err
		}
		j.Goal.ModelSelection = nil
		j.Goal.Adaptive = nil
		if strategy != config.StrategyAlias || r.Config.UsesSubscriptions() {
			j.Goal.ModelSelection = &ModelSelection{Strategy: strategy}
		}
	}
	if req.ReviewerModel != nil {
		j.Goal.ReviewerModel = *req.ReviewerModel
		j.Goal.ReviewerSelection = nil
	}
	if req.MaxCostUSD != nil {
		if err := r.validateGoalBudget(*req.MaxCostUSD); err != nil {
			return nil, err
		}
		if j.Goal.Budget == nil && *req.MaxCostUSD > 0 {
			return nil, errors.New("this goal started without cost tracking; start a new capped goal from its branch to track a new spending total")
		}
		if j.Goal.Budget != nil {
			j.Goal.Budget.LimitUSD = *req.MaxCostUSD
			j.Goal.Budget.RemainingUSD = max(0, *req.MaxCostUSD-j.Goal.Budget.SpentUSD)
		}
	}
	if j.Goal.Budget != nil && j.Goal.ModelSelection == nil {
		j.Goal.ModelSelection = &ModelSelection{Strategy: strategy}
	}
	if j.Goal.ReviewerModel != "" {
		if j.Goal.ModelSelection == nil {
			j.Goal.ModelSelection = &ModelSelection{Strategy: strategy}
		}
		if j.Goal.ReviewerSelection == nil {
			j.Goal.ReviewerSelection = &ModelSelection{Strategy: config.StrategyAlias}
		}
	}
	if strategy == config.StrategyAdaptive {
		if j.Goal.Adaptive == nil {
			j.Goal.Adaptive = &AdaptiveProgress{EscalateAfter: r.Config.Jobs.EscalateAfter}
			if j.Goal.Adaptive.EscalateAfter == 0 {
				j.Goal.Adaptive.EscalateAfter = 2
			}
		}
		if req.EscalateAfter != nil {
			j.Goal.Adaptive.EscalateAfter = *req.EscalateAfter
		}
		if j.Goal.Adaptive.EscalateAfter < 1 {
			return nil, errors.New("escalate_after must be positive")
		}
		j.Goal.Adaptive.StalledCycles, j.Goal.Adaptive.ReviewRejections = 0, 0
	} else if req.EscalateAfter != nil {
		return nil, errors.New("escalate_after requires the adaptive strategy")
	}
	if req.RetryUnavailable && j.Goal.ModelSelection == nil {
		return nil, errors.New("retry_unavailable requires a goal using managed model selection")
	}
	if req.MaxIterations != nil {
		if *req.MaxIterations < 0 {
			return nil, errors.New("max_iterations must be non-negative")
		}
		j.Goal.MaxIterations = *req.MaxIterations
	}
	if j.Goal.MaxIterations > 0 && j.Goal.Iterations >= j.Goal.MaxIterations {
		return nil, errors.New("raise max_iterations or set it to 0 to resume this goal")
	}
	if input := strings.TrimSpace(req.Input); input != "" {
		j.Goal.Input += "\n" + input
	}
	j.Goal.Failures = 0
	j.Goal.Phase = goalWorking
	if _, err := r.goalRepo(ctx, j); err != nil {
		return nil, err
	}
	if err := r.Store.claimGoal(ctx, j); err != nil {
		return nil, err
	}
	if req.RetryUnavailable {
		routes, _ := r.Config.StrategyModels(j.Model, r.goalStrategy(j))
		if j.Goal.ReviewerModel != "" {
			reviewRoutes, _ := r.Config.ReviewerRoutes(j.Model, j.Goal.ReviewerModel, "")
			routes = append(routes, reviewRoutes...)
		}
		if err := r.resetModelAvailability(ctx, routes); err != nil {
			j.Status, j.Error, j.RunnerPID = Blocked, "could not clear cached model availability: "+err.Error(), 0
			_ = r.Store.saveGoal(context.Background(), j)
			return nil, err
		}
	}
	snapshot := cloneJob(j)
	r.startLocked(j, config.Repo{}, "")
	return snapshot, nil
}

// goalRepo rechecks the allowlist and checkout on restart before executing
// anything, since configuration or the worktree may have changed meanwhile.
func (r *Runner) goalRepo(ctx context.Context, j *Job) (config.Repo, error) {
	mode := j.ExecutionMode
	if mode == "" {
		mode = config.ModeAPI
	}
	if mode != r.Config.ExecutionMode() {
		return config.Repo{}, fmt.Errorf("goal uses %s mode but jobs.mode is %s; restore its mode or start a new goal from its branch", mode, r.Config.ExecutionMode())
	}
	_, repo, err := r.resolveRepo(ctx, j.Repo)
	if err != nil {
		return repo, err
	}
	repoTop, err := git(ctx, expandHome(repo.Path), "rev-parse", "--show-toplevel")
	if err != nil || realPath(repoTop) != realPath(j.RepoPath) {
		return repo, errors.New("the allowlisted repository path changed; restore it before resuming")
	}
	if strings.TrimSpace(repo.Test) == "" {
		return repo, fmt.Errorf("repos.%s.test must be configured for this goal", j.Repo)
	}
	if _, ok := r.Config.Aliases[j.Model]; !ok {
		return repo, fmt.Errorf("model alias %q is no longer configured", j.Model)
	}
	if _, err := r.Config.StrategyModels(j.Model, r.goalStrategy(j)); err != nil {
		return repo, err
	}
	if err := r.Config.ValidateReviewer(j.Model, r.goalStrategy(j), j.Goal.ReviewerModel); err != nil {
		return repo, err
	}
	if j.Goal.Budget != nil {
		if err := r.validateGoalBudget(j.Goal.Budget.LimitUSD); err != nil {
			return repo, err
		}
	}
	if j.Worktree == "" {
		return repo, errors.New("the goal's worktree was removed; start a new goal from its branch")
	}
	top, err := git(ctx, j.Worktree, "rev-parse", "--show-toplevel")
	if err != nil || realPath(top) != realPath(j.Worktree) {
		return repo, fmt.Errorf("goal worktree is missing or invalid: %s", j.Worktree)
	}
	common, err := git(ctx, j.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	expected, repoErr := git(ctx, j.RepoPath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || repoErr != nil || realPath(common) != realPath(expected) {
		return repo, errors.New("goal worktree no longer belongs to its repository")
	}
	branch, err := git(ctx, j.Worktree, "branch", "--show-current")
	if err != nil || branch != j.Branch {
		return repo, errors.New("goal worktree is no longer on its recorded branch")
	}
	return repo, nil
}

func (r *Runner) runGoal(ctx context.Context, j *Job) {
	// A crashed supervisor may have left a coding agent or test process alive.
	// Terminate that process group before touching its checkout again.
	err := killGroup(j.AgentPID)
	j.AgentPID = 0
	if err == nil {
		err = r.executeGoal(ctx, j)
	}
	bg := context.Background()
	if errors.Is(err, errGoalStopped) {
		// The cancel tool owns the terminal status. Release the old worker
		// without writing any of its stale progress over a resumed worker.
		_, _ = r.Store.db.ExecContext(bg, `UPDATE jobs SET agent_pid = 0, runner_pid = 0
			WHERE id = ? AND run_token = ? AND status = ?`, j.ID, j.RunToken, Cancelled)
		return
	}
	j.RunnerPID, j.AgentPID = 0, 0
	j.FinishedAt = time.Now()
	switch {
	case ctx.Err() != nil:
		j.Status, j.Error = Queued, "service stopped; goal will resume when dotshift serve starts"
		j.FinishedAt = time.Time{}
	case err != nil:
		j.Status, j.Error = Blocked, err.Error()
	default:
		j.Status, j.Error = Done, ""
		// Keep ownership through cleanup so another process cannot clean
		// this worktree while the successful worker is removing it.
		j.RunnerPID = os.Getpid()
	}
	if err := r.Store.saveGoal(bg, j); err != nil {
		if errors.Is(err, errGoalStopped) {
			_, _ = r.Store.db.ExecContext(bg, `UPDATE jobs SET agent_pid = 0, runner_pid = 0
				WHERE id = ? AND run_token = ? AND status = ?`, j.ID, j.RunToken, Cancelled)
		} else {
			fmt.Fprintf(os.Stderr, "dotshift: saving goal %s: %v\n", j.ID, err)
		}
		return
	}
	if j.Status == Done {
		r.removeWorktree(bg, j, j.Commits == 0)
		j.RunnerPID = 0
		if err := r.Store.saveGoal(bg, j); err != nil {
			fmt.Fprintf(os.Stderr, "dotshift: saving goal cleanup %s: %v\n", j.ID, err)
		}
	}
}

func (r *Runner) executeGoal(ctx context.Context, j *Job) error {
	repo, err := r.goalRepo(ctx, j)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(j.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	var out io.Writer = logFile
	if r.Echo != nil {
		out = io.MultiWriter(out, r.Echo)
	}
	schemaPath := filepath.Join(filepath.Dir(j.LogPath), j.ID+".schema.json")
	if err := os.WriteFile(schemaPath, []byte(agent.GoalOutputSchema), 0o600); err != nil {
		return err
	}
	defer os.Remove(schemaPath)
	j.Status = Running
	if j.StartedAt.IsZero() {
		j.StartedAt = time.Now()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.Store.saveGoal(ctx, j); err != nil {
			return err
		}
		iteration := j.Goal.Iterations + 1
		if j.Goal.Phase == goalReviewing {
			iteration = j.Goal.Iterations
		}
		fmt.Fprintf(out, "\n--- dotshift: goal %s, iteration %d, %s\n", j.ID, iteration, j.Goal.Phase)
		switch j.Goal.Phase {
		case goalWorking, goalReviewing:
			review := j.Goal.Phase == goalReviewing
			if review {
				current, err := r.goalValidationCurrent(ctx, j, repo.Test)
				if err != nil {
					return err
				}
				if !current {
					j.Goal.Phase = goalTesting
					continue
				}
			}
			if !review && j.Goal.MaxIterations > 0 && j.Goal.Iterations >= j.Goal.MaxIterations {
				return fmt.Errorf("reached max_iterations (%d) before the goal passed review; resume_goal can raise the limit", j.Goal.MaxIterations)
			}
			report, err := r.goalAgent(ctx, j, repo, schemaPath, review, out)
			if budgetErr := r.Store.refreshBudget(ctx, j); budgetErr != nil && err == nil {
				err = budgetErr
			}
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, errGoalStopped) || errors.Is(err, errModelsUnavailable) {
					return err
				}
				var accounting *modelpolicy.AccountingError
				var setup *agent.SetupError
				if errors.As(err, &accounting) || errors.As(err, &setup) {
					return err
				}
				var failure *modelpolicy.Failure
				if errors.As(err, &failure) && j.Goal.ModelSelection != nil {
					selection := r.phaseSelection(j)
					provider, _, _ := config.SplitModel(selection.ActiveModel)
					route := config.ModelRoute{Model: selection.ActiveModel, Route: selection.ActiveRoute, Provider: provider}
					if err := r.recordModelFailure(ctx, route, failure); err != nil {
						return fmt.Errorf("saving model availability: %w", err)
					}
					j.Error = selection.ActiveModel + ": " + failure.Error()
					selection.LastFailure = j.Error
					fmt.Fprintf(out, "--- dotshift: %s; preserving files and selecting the next available model\n", j.Error)
					continue
				}
				j.Goal.Failures++
				j.Error = err.Error()
				if a := j.Goal.Adaptive; !review && a != nil && j.Goal.Failures >= a.EscalateAfter {
					if err := r.escalateGoal(j, "repeated agent execution or report failures", out); err != nil {
						return err
					}
					continue
				}
				if j.Goal.Failures >= 3 && (review || j.Goal.Adaptive == nil) {
					return fmt.Errorf("agent failed %d consecutive attempts: %w", j.Goal.Failures, err)
				}
				if err := r.Store.saveGoal(ctx, j); err != nil {
					return err
				}
				timer := time.NewTimer(time.Second << min(j.Goal.Failures-1, 2))
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
				continue
			}
			j.Goal.Report, j.Goal.Failures = report, 0
			j.Summary, j.Error = report.Summary, ""
			if review {
				if report.Status == "complete" {
					current, err := r.goalValidationCurrent(ctx, j, repo.Test)
					if err != nil {
						return err
					}
					if !current {
						j.Goal.Phase = goalTesting
						continue
					}
					return nil
				}
				if report.Status == "blocked" {
					return fmt.Errorf("goal needs input: %s", report.NextSteps)
				}
				j.Goal.Phase = goalWorking
				if err := r.adaptiveReviewRejected(j, out); err != nil {
					return err
				}
			} else {
				j.Goal.Phase = goalTesting
			}
		case goalTesting:
			if err := r.goalTests(ctx, j, repo.Test, out); err != nil {
				return err
			}
			if _, err := commitAll(ctx, j.Worktree, commitMessage(j)); err != nil {
				return fmt.Errorf("checkpointing goal changes: %w", err)
			}
			n, err := git(ctx, j.Worktree, "rev-list", "--count", j.BaseCommit+"..HEAD")
			if err != nil {
				return err
			}
			fmt.Sscan(n, &j.Commits)
			j.DiffStat, err = git(ctx, j.Worktree, "diff", "--stat", j.BaseCommit, "HEAD")
			if err != nil {
				return err
			}
			j.Goal.TestedCommit, err = git(ctx, j.Worktree, "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			j.Goal.TestCommand = repo.Test
			j.Goal.Iterations++
			j.Goal.Phase = goalWorking
			if j.Goal.Report.Status == "blocked" {
				return fmt.Errorf("goal needs input: %s", j.Goal.Report.NextSteps)
			}
			if j.Goal.Report.Status == "complete" && j.TestResult == "passed" {
				j.Goal.Phase = goalReviewing
			}
			if err := r.adaptiveTestResult(ctx, j, out); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown goal phase %q", j.Goal.Phase)
		}
	}
}

func (r *Runner) goalValidationCurrent(ctx context.Context, j *Job, command string) (bool, error) {
	head, err := git(ctx, j.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	status, err := git(ctx, j.Worktree, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return j.TestResult == "passed" && j.Goal.TestCommand == command && j.Goal.TestedCommit == head && status == "", nil
}

func (r *Runner) goalAgent(ctx context.Context, j *Job, repo config.Repo, schema string, review bool, out io.Writer) (GoalReport, error) {
	goalContext := ctx
	var route config.ModelRoute
	if j.Goal.ModelSelection != nil {
		var err error
		route, err = r.selectGoalModel(ctx, j, review, out)
		if err != nil {
			return GoalReport{}, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, r.Config.Jobs.Timeout.Duration)
	defer cancel()
	baseURL, apiKey, model := r.BaseURL, r.APIKey, j.Model
	var gateway *modelpolicy.Gateway
	if r.Config.UsesSubscriptions() {
		model = route.Model
	} else if j.Goal.ModelSelection != nil {
		var err error
		options := modelpolicy.GatewayOptions{AccountingContext: goalContext}
		if j.Goal.Budget != nil {
			options.Accountant = &goalAccountant{store: r.Store, jobID: j.ID, token: j.RunToken, model: route.Model, role: j.Goal.Phase}
		}
		gateway, err = modelpolicy.StartGateway(r.BaseURL, r.APIKey, route.Route, cancel, options)
		if err != nil {
			return GoalReport{}, err
		}
		defer gateway.Close()
		baseURL, apiKey, model = gateway.URL, gateway.Key, route.Route
	}
	// A unique output file prevents a crash or retry from reusing stale success.
	last, err := os.CreateTemp(filepath.Dir(j.LogPath), j.ID+"-*.summary")
	if err != nil {
		return GoalReport{}, err
	}
	last.Close()
	defer os.Remove(last.Name())
	prompt := goalPrompt(j, repo.Test, review)
	if review {
		evidence, err := reviewEvidence(ctx, j)
		if err != nil {
			return GoalReport{}, err
		}
		prompt += evidence
	}
	cmd, finishAgent, err := agent.Prepare(ctx, r.Agent, agent.Task{
		Dir: j.Worktree, Prompt: prompt, Model: model, BaseURL: baseURL,
		APIKeyEnv: APIKeyEnv, LastMessageFile: last.Name(), OutputSchemaFile: schema, ReadOnly: review,
	}, out, cancel)
	if err != nil {
		return GoalReport{}, err
	}
	if !review {
		prompt += promptSuffix
	}
	cmd.Stdin = strings.NewReader(prompt)
	if !r.Config.UsesSubscriptions() {
		cmd.Env = append(cmd.Environ(), APIKeyEnv+"="+apiKey)
	}
	cmd.Env = append(cmd.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	err = r.goalCommand(ctx, j, cmd)
	cliErr := finishAgent()
	if gateway != nil {
		gateway.Close()
	}
	if errors.Is(err, errGoalStopped) {
		return GoalReport{}, err
	}
	if cliErr != nil {
		return GoalReport{}, cliErr
	}
	if gateway != nil {
		if err := gateway.AccountingError(); err != nil {
			return GoalReport{}, err
		}
		if failure := gateway.Failure(); failure != nil {
			return GoalReport{}, failure
		}
	}
	if err != nil {
		return GoalReport{}, fmt.Errorf("%s: %w", r.Agent.Name(), err)
	}
	data, err := os.ReadFile(last.Name())
	if err != nil {
		return GoalReport{}, err
	}
	var report GoalReport
	if err := json.Unmarshal(data, &report); err != nil {
		return report, fmt.Errorf("agent did not return the required goal JSON: %w", err)
	}
	if report.Status != "continue" && report.Status != "complete" && report.Status != "blocked" {
		return report, fmt.Errorf("invalid goal status %q", report.Status)
	}
	if strings.TrimSpace(report.Summary) == "" || (report.Status != "complete" && strings.TrimSpace(report.NextSteps) == "") {
		return report, errors.New("goal response must include a summary and actionable next_steps when unfinished")
	}
	return report, nil
}

func goalPrompt(j *Job, testCommand string, review bool) string {
	role := "Work autonomously toward the project goal. Plan the remaining work, implement the next useful increment, and fix any reported test failures."
	assessment, remaining := j.Goal.Report.Summary, j.Goal.Report.NextSteps
	if review {
		role = "Independently review whether the entire project goal is complete in a fresh read-only session. Inspect the code, tests, requirements, and runner-generated diff evidence. Look for missing behavior, regressions, accidental generated files, and tests weakened to conceal failures."
		assessment, remaining = "Make your own assessment from the requirements, files, diff, and validation evidence.", ""
	}
	modelHandoff := ""
	selection := j.Goal.ModelSelection
	if review && j.Goal.ReviewerSelection != nil {
		selection = j.Goal.ReviewerSelection
	}
	if selection != nil {
		modelHandoff = fmt.Sprintf("\nModel strategy: %s\nCurrent model: %s\nLast model API failure: %s\nInspect the current files and git diff before continuing; an earlier model attempt may have left partial edits.\n", selection.Strategy, selection.ActiveModel, selection.LastFailure)
	}
	if a := j.Goal.Adaptive; a != nil && a.Reason != "" {
		modelHandoff += "\nEscalation context: " + a.Reason + "\n"
	}
	if review && j.Goal.ReviewerModel != "" {
		modelHandoff += "\nImplementation model: " + j.Goal.ModelSelection.ActiveModel + "\n"
	}
	return fmt.Sprintf(`%s
%s

Original project goal
%s

Additional instructions
%s

Previous assessment
%s

Remaining work or review feedback
%s

Last execution error
%s

Validation command
%s

Latest validation result: %s
%s

The branch contains all earlier work. Preserve it and continue from the current files.
For a new project, scaffold its application and meaningful tests for the validation command. A placeholder or an empty test suite is not completion evidence.
Return JSON with status, summary, and next_steps. Use status "continue" for unfinished work or fixable issues, "complete" only when every part of the goal is implemented and verified, and "blocked" only when a specific missing credential, decision, or external dependency requires human input. Explain completion evidence in summary. Keep a concise cumulative progress summary and remaining plan so the next attempt can continue without the earlier conversation. dotshift independently runs validation and reviews completion.`,
		role, modelHandoff, j.Prompt, j.Goal.Input, assessment, remaining, j.Error,
		testCommand, j.TestResult, j.TestOutput)
}

func (r *Runner) goalTests(ctx context.Context, j *Job, command string, out io.Writer) error {
	stepCtx, cancel := context.WithTimeout(ctx, r.Config.Jobs.Timeout.Duration)
	defer cancel()
	fmt.Fprintf(out, "\n--- dotshift: running tests: %s\n", command)
	tail := &tailBuffer{max: 16000}
	cmd := exec.CommandContext(stepCtx, "sh", "-c", command)
	cmd.Dir = j.Worktree
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	cmd.Stdout = io.MultiWriter(out, tail)
	cmd.Stderr = cmd.Stdout
	err := r.goalCommand(stepCtx, j, cmd)
	if ctx.Err() != nil || errors.Is(err, errGoalStopped) {
		return err
	}
	j.TestResult = "passed"
	if err != nil {
		j.TestResult = "failed"
		fmt.Fprintf(tail, "\nTest command failed: %v\n", err)
	}
	j.TestOutput = tail.String()
	fmt.Fprintf(out, "--- dotshift: tests %s\n", j.TestResult)
	return nil
}

// goalCommand records the live process for cancellation and crash recovery.
// Both coding and tests use this path so either phase can be interrupted.
func (r *Runner) goalCommand(ctx context.Context, j *Job, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Store.saveGoal(ctx, j); err != nil {
		return err
	}
	detach(cmd)
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	j.AgentPID = cmd.Process.Pid
	if err := r.Store.saveGoal(context.Background(), j); err != nil {
		_ = killGroup(j.AgentPID)
		_ = cmd.Wait()
		j.AgentPID = 0
		return err
	}
	err := cmd.Wait()
	_ = killGroup(j.AgentPID)
	j.AgentPID = 0
	if cmd.ProcessState != nil {
		j.ExitCode = cmd.ProcessState.ExitCode()
	}
	if saveErr := r.Store.saveGoal(context.Background(), j); saveErr != nil {
		return saveErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
