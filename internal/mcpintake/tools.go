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
	Repos []repoOut `json:"repos"`
	// DefaultModel is the alias start_task uses when none is given.
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
}

type repoOut struct {
	Name     string `json:"name"`
	HasTests bool   `json:"has_tests"`
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
	Next string `json:"next,omitempty"`
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
			"touches the user's working copy. Call list_repos first, then start_task. Poll task_status until " +
			"finished is true (tasks take minutes), then task_result. If the agent's notes ask a question, " +
			"answer it with send_input, which continues the work on the same branch.",
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_repos",
		Title:       "List repos",
		Description: "List the repositories dotshift may work in, and the model aliases tasks can use.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listReposOut, error) {
		out := listReposOut{Repos: []repoOut{}, DefaultModel: defaultModel, Models: models}
		for _, r := range svc.Repos() {
			out.Repos = append(out.Repos, repoOut{Name: r.Name, HasTests: r.HasTests})
		}
		logf("list_repos: %d repo(s)", len(out.Repos))
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
			if repos := svc.Repos(); len(repos) == 1 {
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
		WaitingForInput: j.Status == jobs.WaitingInput,
		Repo:            j.Repo, Branch: j.Branch, Model: j.Model, Instructions: j.Prompt, FollowsUp: j.Parent,
		CreatedAt: stamp(j.CreatedAt), StartedAt: stamp(j.StartedAt), FinishedAt: stamp(j.FinishedAt),
		Notes: j.Summary, Commits: j.Commits,
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
