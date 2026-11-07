// Package mcpintake is the MCP intake: an MCP server whose tools start and
// follow jobs, served over streamable HTTP behind OAuth so a remote client
// (an OpenAI dot through a tunnel) can reach it.
//
// The tools only use jobs.Service, so their names and shapes can change
// without touching the job runner.
package mcpintake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/jobs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is reported to MCP clients.
var Version = "0.1.0"

const (
	recentOutputBytes = 4000
	maxDiffBytes      = 60000
)

type listReposOut struct {
	Repos    []repoOut           `json:"repos"`
	Projects jobs.ProjectOptions `json:"projects"`
	// DefaultModel is the alias start_task uses when none is given.
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
}

type repoOut struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	HasTests    bool   `json:"has_tests"`
	TestCommand string `json:"test_command,omitempty"`
	Managed     bool   `json:"managed,omitempty"`
}

type createProjectIn struct {
	Name        string `json:"name" jsonschema:"new folder and repo name: 1-64 lowercase letters, digits, - or _, starting with a letter or digit; created directly inside the configured projects.root"`
	Description string `json:"description,omitempty" jsonschema:"short project description for the initial README; provide the full objective and acceptance criteria separately to start_goal"`
	TestProfile string `json:"test_profile" jsonschema:"name from list_repos.projects.test_profiles; the coding agent must implement real tests for its command"`
}

type registerProjectIn struct {
	Name        string `json:"name" jsonschema:"existing Git repository folder directly inside projects.root; must have an initial commit; paths and symlinks are not accepted"`
	TestProfile string `json:"test_profile" jsonschema:"name from list_repos.projects.test_profiles"`
}

type projectOut struct {
	Project *jobs.Project `json:"project"`
	Next    string        `json:"next"`
}

type startTaskIn struct {
	Repo         string `json:"repo,omitempty" jsonschema:"name of the repo from list_repos; may be left out when only one repo is allowed"`
	Instructions string `json:"instructions" jsonschema:"what the coding agent should do, in plain language, with enough detail to work without asking"`
	Model        string `json:"model,omitempty" jsonschema:"optional model alias from list_repos, e.g. fast or smart"`
	Branch       string `json:"branch,omitempty" jsonschema:"optional name for the new git branch; default dotshift/<task id>"`
	Base         string `json:"base,omitempty" jsonschema:"optional branch or commit to start from; default the repo's current HEAD"`
}

type taskIn struct {
	TaskID string `json:"task_id" jsonschema:"the task_id start_task or send_input returned"`
}

type taskResultIn struct {
	TaskID      string `json:"task_id" jsonschema:"the task_id start_task or send_input returned"`
	IncludeDiff bool   `json:"include_diff,omitempty" jsonschema:"also return the full diff of the branch (cut to about 60 KB)"`
}

type sendInputIn struct {
	TaskID string `json:"task_id" jsonschema:"the finished task to follow up on"`
	Input  string `json:"input" jsonschema:"the answer to the agent's question, or further instructions"`
}

type startGoalIn struct {
	Repo          string   `json:"repo,omitempty" jsonschema:"allowlisted repo name; optional when only one repo is configured"`
	Goal          string   `json:"goal" jsonschema:"the complete project objective and acceptance criteria to pursue autonomously"`
	Model         string   `json:"model,omitempty" jsonschema:"model alias from list_repos"`
	Strategy      string   `json:"strategy,omitempty" jsonschema:"model strategy from list_strategies; defaults to jobs.strategy"`
	ReviewerModel string   `json:"reviewer_model,omitempty" jsonschema:"reviewer alias or auto to prefer a different provider in the coding alias; always excludes the implementation model; defaults to jobs.reviewer_model"`
	EscalateAfter int      `json:"escalate_after,omitempty" jsonschema:"adaptive strategy only: repeated failed tests, unchanged cycles, agent failures, or rejected reviews before advancing to the next configured model; default jobs.escalate_after"`
	MaxCostUSD    *float64 `json:"max_cost_usd,omitempty" jsonschema:"API mode only: project spending cap in USD including retries and review; stop once reported costs reach it, so the final request can exceed it; 0 disables; defaults to jobs.max_cost_usd"`
	Branch        string   `json:"branch,omitempty" jsonschema:"optional new branch name"`
	Base          string   `json:"base,omitempty" jsonschema:"optional starting branch or commit"`
	MaxIterations int      `json:"max_iterations,omitempty" jsonschema:"optional limit on coding and testing cycles; 0 means keep working until complete or blocked"`
}

type resumeGoalIn struct {
	TaskID           string   `json:"task_id" jsonschema:"the unfinished goal's task_id"`
	Input            string   `json:"input,omitempty" jsonschema:"additional instructions or the answer to a blocker"`
	Model            string   `json:"model,omitempty" jsonschema:"optional replacement model alias"`
	Strategy         string   `json:"strategy,omitempty" jsonschema:"optional replacement strategy from list_strategies; alias uses configured alias order"`
	ReviewerModel    *string  `json:"reviewer_model,omitempty" jsonschema:"optional replacement reviewer alias or auto; empty string restores same-model fresh review"`
	EscalateAfter    *int     `json:"escalate_after,omitempty" jsonschema:"optional positive adaptive escalation threshold; resuming retains the current model tier"`
	MaxCostUSD       *float64 `json:"max_cost_usd,omitempty" jsonschema:"optional replacement total USD cap for a goal that started capped; accumulated spending is preserved; 0 removes the cap; only raise or remove a cap when the user authorizes it"`
	RetryUnavailable bool     `json:"retry_unavailable,omitempty" jsonschema:"clear cached unavailability after subscription allowance resets or API credits/access are restored; does not change actual provider limits"`
	MaxIterations    *int     `json:"max_iterations,omitempty" jsonschema:"optional replacement total iteration limit; 0 removes the limit"`
}

// taskOut is a job as MCP clients see it. Fields that don't apply are left out.
type taskOut struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	// Finished is true once the status will not change again.
	Finished bool `json:"finished"`
	// WaitingForInput is true when the agent stopped to ask something
	// mid-task. Codex runs non-interactively, so today an agent's questions
	// arrive in its notes when it finishes; answer them with send_input.
	WaitingForInput bool   `json:"waiting_for_input"`
	Repo            string `json:"repo"`
	Branch          string `json:"branch"`
	Model           string `json:"model"`
	ExecutionMode   string `json:"execution_mode"`
	Instructions    string `json:"instructions"`
	FollowsUp       string `json:"follows_up,omitempty"`
	CreatedAt       string `json:"created_at"`
	StartedAt       string `json:"started_at,omitempty"`
	FinishedAt      string `json:"finished_at,omitempty"`
	Elapsed         string `json:"elapsed,omitempty"`
	Error           string `json:"error,omitempty"`
	// RecentOutput is the end of the agent's output while it runs.
	RecentOutput string `json:"recent_output,omitempty"`
	// Notes is the agent's final message: what it did, or what it needs.
	Notes      string `json:"notes,omitempty"`
	Commits    int    `json:"commits"`
	DiffStat   string `json:"diff_stat,omitempty"`
	Tests      string `json:"tests,omitempty"`
	TestOutput string `json:"test_output,omitempty"`
	Diff       string `json:"diff,omitempty"`
	// DiffTruncated is true when Diff was cut short.
	DiffTruncated bool `json:"diff_truncated,omitempty"`
	// Next says what the client can do now.
	Next string             `json:"next,omitempty"`
	Goal *jobs.GoalProgress `json:"goal,omitempty"`
}

type cancelOut struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// NewServer returns the MCP server with dotshift's tools. logf, if not nil,
// gets one line per tool call.
func NewServer(svc jobs.Service, defaultModel string, models []string, logf func(format string, args ...any)) *mcp.Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "dotshift", Title: "dotshift", Version: Version}, &mcp.ServerOptions{
		Instructions: "dotshift runs coding tasks on the user's own machine, in git repositories they allowed, " +
			"with the coding agent and models they chose. Each task works on its own new git branch and never " +
			"touches the user's working copy. Call list_repos first. When projects.enabled is true, " +
			"use create_project to start a new project, or register_project for an existing committed repository directly inside projects.root. " +
			"Choose a test_profile from the catalog. Creation seeds a README and initial commit; it does not build the application. " +
			"Then call start_goal with the returned project name, the user's objective, and concrete acceptance criteria, including real tests. " +
			"Use start_goal for a project objective that " +
			"should keep working, testing, and fixing issues autonomously; its repo must have tests configured. " +
			"Goals persist and resume when the service restarts. Use start_task for a single coding task. " +
			"Call list_strategies to select a goal's model policy and see observed model availability. " +
			"The strategy catalog reports execution_mode: api or subscription. In subscription mode the official CLIs use the user's saved sign-ins; there is no API fallback. " +
			"Use prefer_openai to switch from OpenAI to Anthropic on observed limits. Subscription mode waits for a known reset, or blocks for retry_unavailable if no reset time is reported. " +
			"Use adaptive for an ordered ladder that escalates on repeated failures or stalled progress, and reviewer_model=auto for a distinct completion reviewer. " +
			"Only API mode supports max_cost_usd to bound reported spending; the last request may exceed it. Subscription mode uses max_iterations and the configured timeout, not USD caps. " +
			"Never raise or remove a spending cap to bypass a blocker without the user's authorization. " +
			"Only set retry_unavailable after access has actually been restored. " +
			"Poll task_status until finished is true (tasks take minutes), then task_result. If the agent's notes ask a question, " +
			"answer it with send_input for a task, or resume_goal for a blocked goal. Both continue on the same branch.",
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_strategies", Title: "List model strategies",
		Description: "List execution mode, goal model strategies, eligible aliases, spending-cap support, and observed availability and retry times. Does not make model calls or measure remaining credits/subscription allowance.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, *jobs.StrategyCatalog, error) {
		catalog, err := svc.Strategies(ctx)
		return nil, catalog, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_repos",
		Title:       "List repos",
		Description: "List allowed repositories, model aliases, and the project root and test profiles available for create_project and register_project.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listReposOut, error) {
		out := listReposOut{Repos: []repoOut{}, DefaultModel: defaultModel, Models: models, Projects: svc.ProjectOptions()}
		repos, err := svc.Repos(ctx)
		if err != nil {
			return nil, out, err
		}
		for _, r := range repos {
			out.Repos = append(out.Repos, repoOut{Name: r.Name, Path: r.Path, HasTests: r.HasTests, TestCommand: r.TestCommand, Managed: r.Managed})
		}
		logf("list_repos: %d repo(s)", len(out.Repos))
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "create_project", Title: "Create a project",
		Description: "Create a new local Git repository inside the configured project root, with a README and initial commit, and register it immediately. Never overwrites an existing path. Choose a test profile from list_repos; then call start_goal to build the project and its tests. Makes no model calls and publishes nothing.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createProjectIn) (*mcp.CallToolResult, projectOut, error) {
		project, err := svc.CreateProject(ctx, jobs.ProjectRequest{Name: in.Name, Description: in.Description, TestProfile: in.TestProfile})
		if err != nil {
			return nil, projectOut{}, err
		}
		logf("create_project: %s", project.Name)
		return nil, projectOut{Project: project, Next: "Call start_goal with repo=" + project.Name + " and the complete objective and acceptance criteria. The agent must scaffold the application and real tests for the configured test command. Results are committed to the returned task branch."}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "register_project", Title: "Register an existing project",
		Description: "Register an existing Git repository directly inside the configured project root. Requires an initial commit and a test profile from list_repos. Preserves existing files and uncommitted changes; tasks start from committed code. Safe to retry with the same name and profile.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false), IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registerProjectIn) (*mcp.CallToolResult, projectOut, error) {
		project, err := svc.RegisterProject(ctx, jobs.ProjectRequest{Name: in.Name, TestProfile: in.TestProfile})
		if err != nil {
			return nil, projectOut{}, err
		}
		logf("register_project: %s", project.Name)
		return nil, projectOut{Project: project, Next: "Use start_goal or start_task with repo=" + project.Name + ". Work starts from committed code; uncommitted files in the original checkout are not included."}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "start_goal", Title: "Start a project goal",
		Description: "Pursue a project goal autonomously on a new branch. Repeats coding, configured tests, and fixes " +
			"until tests pass and an independent review confirms completion. Persists progress across service restarts. " +
			"Select an optional model strategy from list_strategies. Requires a repo with has_tests=true. " +
			"Returns a task_id for task_status, task_result, and cancel_task.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in startGoalIn) (*mcp.CallToolResult, taskOut, error) {
		repo := in.Repo
		if repo == "" {
			repos, err := svc.Repos(ctx)
			if err != nil {
				return nil, taskOut{}, err
			}
			if len(repos) == 1 {
				repo = repos[0].Name
			}
		}
		j, err := svc.Submit(ctx, jobs.Request{
			Repo: repo, Prompt: in.Goal, Model: in.Model, Branch: in.Branch, Base: in.Base,
			Goal: true, MaxIterations: in.MaxIterations,
			Strategy: in.Strategy, ReviewerModel: in.ReviewerModel, EscalateAfter: in.EscalateAfter, MaxCostUSD: in.MaxCostUSD,
		})
		if err != nil {
			return nil, taskOut{}, err
		}
		logf("start_goal: %s in %s with %s", j.ID, j.Repo, j.Model)
		out := describe(j)
		out.Next = "The goal continues autonomously. Use task_status to check progress and task_result for the outcome."
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "resume_goal", Title: "Resume a project goal",
		Description: "Resume a blocked or cancelled goal in its existing worktree, with optional instructions, " +
			"model or strategy change, or a higher total iteration limit. Use retry_unavailable only after allowance resets or access is restored. " +
			"Returns the same task_id. Resolving the blocker resumes the autonomous loop.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in resumeGoalIn) (*mcp.CallToolResult, taskOut, error) {
		j, err := svc.ResumeGoal(ctx, jobs.ResumeGoalRequest{
			ID: in.TaskID, Input: in.Input, Model: in.Model, MaxIterations: in.MaxIterations,
			Strategy: in.Strategy, RetryUnavailable: in.RetryUnavailable,
			ReviewerModel: in.ReviewerModel, EscalateAfter: in.EscalateAfter, MaxCostUSD: in.MaxCostUSD,
		})
		if err != nil {
			return nil, taskOut{}, err
		}
		logf("resume_goal: %s with %s", j.ID, j.Model)
		out := describe(j)
		out.Next = "The goal is continuing autonomously. Use task_status to check progress."
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:  "start_task",
		Title: "Start a coding task",
		Description: "Start a coding task in one of the user's repos. A coding agent works on a new git branch " +
			"on the user's machine and commits the result there; nothing is pushed. Returns at once with a " +
			"task_id; follow it with task_status.",
		Annotations: &mcp.ToolAnnotations{Title: "Start a coding task", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in startTaskIn) (*mcp.CallToolResult, taskOut, error) {
		repo := in.Repo
		if repo == "" {
			repos, err := svc.Repos(ctx)
			if err != nil {
				return nil, taskOut{}, err
			}
			if len(repos) == 1 {
				repo = repos[0].Name
			} else {
				return nil, taskOut{}, errors.New("say which repo; list_repos shows the choices")
			}
		}
		j, err := svc.Submit(ctx, jobs.Request{Repo: repo, Prompt: in.Instructions, Model: in.Model, Branch: in.Branch, Base: in.Base})
		if err != nil {
			logf("start_task refused: %v", err)
			return nil, taskOut{}, err
		}
		logf("start_task: job %s in %s on %s with %s", j.ID, j.Repo, j.Branch, j.Model)
		out := describe(j)
		out.Next = "Poll task_status with this task_id until finished is true."
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "task_status",
		Title:       "Check a task",
		Description: "Show where a task is: its status, the end of the agent's output while it runs, and its notes once it finishes.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in taskIn) (*mcp.CallToolResult, taskOut, error) {
		j, err := svc.Get(ctx, in.TaskID)
		if err != nil {
			return nil, taskOut{}, err
		}
		out := describe(j)
		if !j.Status.Terminal() {
			out.RecentOutput = tail(j.LogPath, recentOutputBytes)
			out.Next = "Still working. Check again in a minute."
		} else {
			out.Next = "Finished. Call task_result for the changes and test results."
		}
		if j.Goal != nil && j.Status == jobs.Blocked {
			out.Next = "The goal needs input. Inspect error and goal.report, then call resume_goal after resolving the blocker."
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "task_result",
		Title:       "Get a task's result",
		Description: "Return a finished task's outcome: the agent's notes, the branch and commits, a diff summary (or the full diff), and test results.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in taskResultIn) (*mcp.CallToolResult, taskOut, error) {
		j, err := svc.Get(ctx, in.TaskID)
		if err != nil {
			return nil, taskOut{}, err
		}
		out := describe(j)
		if !j.Status.Terminal() {
			out.Next = "Not finished yet. Poll task_status until finished is true."
			return nil, out, nil
		}
		out.DiffStat = j.DiffStat
		out.Tests = j.TestResult
		if j.TestResult == "failed" {
			out.TestOutput = j.TestOutput
		}
		if in.IncludeDiff {
			out.Diff, out.DiffTruncated, err = jobs.Diff(ctx, j, maxDiffBytes)
			if err != nil {
				return nil, taskOut{}, err
			}
		}
		switch {
		case j.Goal != nil && j.Status != jobs.Done:
			out.Next = "The goal's work is preserved. Resolve the reported issue and call resume_goal to continue on the same branch."
		case j.Status == jobs.Done && j.Commits > 0:
			out.Next = fmt.Sprintf("The work is on branch %s in the user's local repo. To ask for changes or answer the agent, use send_input.", j.Branch)
		case j.Status == jobs.Done:
			out.Next = "The agent made no changes. Its notes say why; send_input can follow up."
		default:
			out.Next = "The task did not finish. Start a new task, or after the user cleans up the worktree, follow up with send_input."
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:  "send_input",
		Title: "Follow up on a task",
		Description: "Answer a question the agent asked in its notes, or give further instructions, once a task has " +
			"finished. This starts a follow-up task on the same branch that knows what was asked and done before. " +
			"Returns the new task_id.",
		Annotations: &mcp.ToolAnnotations{Title: "Follow up on a task", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sendInputIn) (*mcp.CallToolResult, taskOut, error) {
		j, err := svc.Submit(ctx, jobs.Request{Continue: in.TaskID, Prompt: in.Input})
		if err != nil {
			logf("send_input refused: %v", err)
			return nil, taskOut{}, err
		}
		logf("send_input: job %s follows up %s on %s", j.ID, in.TaskID, j.Branch)
		out := describe(j)
		out.Next = "Poll task_status with the new task_id until finished is true."
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cancel_task",
		Title:       "Cancel a task",
		Description: "Stop a task that is still running. Its branch and any partial work are kept on the user's machine.",
		Annotations: &mcp.ToolAnnotations{Title: "Cancel a task", DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in taskIn) (*mcp.CallToolResult, cancelOut, error) {
		if err := svc.Cancel(ctx, in.TaskID); err != nil {
			return nil, cancelOut{}, err
		}
		logf("cancel_task: job %s", in.TaskID)
		return nil, cancelOut{TaskID: in.TaskID, Status: string(jobs.Cancelled)}, nil
	})

	return s
}

func describe(j *jobs.Job) taskOut {
	out := taskOut{
		TaskID: j.ID, Status: string(j.Status), Finished: j.Status.Terminal(),
		WaitingForInput: j.Status == jobs.WaitingInput || j.Status == jobs.Blocked,
		Repo:            j.Repo, Branch: j.Branch, Model: j.Model, Instructions: j.Prompt, FollowsUp: j.Parent,
		CreatedAt: stamp(j.CreatedAt), StartedAt: stamp(j.StartedAt), FinishedAt: stamp(j.FinishedAt),
		Notes: j.Summary, Commits: j.Commits, Goal: j.Goal,
		ExecutionMode: j.ExecutionMode,
	}
	if out.ExecutionMode == "" {
		out.ExecutionMode = "api"
	}
	if j.Goal != nil {
		out.Tests = j.TestResult
		if j.TestResult == "failed" {
			out.TestOutput = j.TestOutput
		}
	}
	if j.Status != jobs.Cancelled {
		out.Error = j.Error
	}
	if !j.StartedAt.IsZero() {
		end := j.FinishedAt
		if end.IsZero() {
			end = time.Now()
		}
		out.Elapsed = end.Sub(j.StartedAt).Round(time.Second).String()
	}
	return out
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// tail returns about the last n bytes of a file, starting at a line break.
func tail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > n {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return ""
		}
	} else {
		n = 0
	}
	data, _ := io.ReadAll(f)
	s := string(data)
	if n > 0 {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return strings.TrimSpace(s)
}

func ptr[T any](v T) *T { return &v }
