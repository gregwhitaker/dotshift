// Package jobs runs coding jobs: each job gets its own git worktree on a new
// branch, the coding agent works there against the router, and the job's
// state is kept in SQLite.
//
// Intakes (the CLI, MCP, ...) use the Service interface and nothing else, so
// the runner never knows which intake started a job.
package jobs

import (
	"context"
	"time"
)

// Status is where a job is in its life.
type Status string

const (
	Queued  Status = "queued"
	Running Status = "running"
	// WaitingInput is reserved for agents that can ask a question mid-task.
	// Codex runs non-interactively, so jobs don't enter it yet.
	WaitingInput Status = "waiting_input"
	Done         Status = "done"
	Failed       Status = "failed"
	Cancelled    Status = "cancelled"
)

// Terminal reports whether a job in this status will not change again.
func (s Status) Terminal() bool {
	return s == Done || s == Failed || s == Cancelled
}

// Request is what an intake asks the runner to do.
type Request struct {
	// Repo is an allowlisted repo name, or the path of an allowlisted repo.
	Repo   string
	Prompt string
	// Model is a router alias. Empty means the configured default.
	Model string
	// Branch is the new branch the job works on. Empty means dotshift/<id>.
	Branch string
	// Base is the commit-ish the branch starts from. Empty means the repo's HEAD.
	Base string
}

// Job is a job's recorded state.
type Job struct {
	ID       string
	Repo     string // allowlisted repo name
	RepoPath string
	Prompt   string
	Model    string
	Branch   string
	// BaseCommit is the commit the branch started from.
	BaseCommit string
	Worktree   string
	Status     Status
	// Error says why a job failed or was cancelled.
	Error string
	// Summary is the agent's final message: its notes on what it did.
	Summary string
	// Commits is how many commits the branch has on top of BaseCommit.
	Commits  int
	DiffStat string
	// TestResult is "passed", "failed" or empty when the repo has no test
	// command; TestOutput holds the end of the test command's output.
	TestResult string
	TestOutput string
	ExitCode   int
	LogPath    string
	// RunnerPID is the dotshift process running the job and AgentPID the
	// agent it launched; they let another dotshift process cancel the job or
	// notice that its runner died.
	RunnerPID  int
	AgentPID   int
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// RepoInfo describes an allowlisted repo.
type RepoInfo struct {
	Name string
	Path string
}

// Service is the job runner as intakes see it.
type Service interface {
	// Repos lists the repos jobs may run in.
	Repos() []RepoInfo
	// Submit validates a request, creates the job's worktree and starts the
	// agent in the background. It returns once the job is recorded.
	Submit(ctx context.Context, req Request) (*Job, error)
	Get(ctx context.Context, id string) (*Job, error)
	// List returns jobs, newest first.
	List(ctx context.Context) ([]*Job, error)
	// Cancel stops a job that hasn't finished.
	Cancel(ctx context.Context, id string) error
	// Wait blocks until the job finishes or ctx ends.
	Wait(ctx context.Context, id string) (*Job, error)
}
