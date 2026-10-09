// Package agent launches the coding agent that does a job's work.
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/gregwhitaker/dotshift/internal/config"
)

// Task is everything an agent needs to work on one job.
type Task struct {
	// Dir is the job's worktree; the agent works only there.
	Dir    string
	Prompt string
	// Model is the router alias the agent asks for, e.g. "smart".
	Model string
	// BaseURL is the router's OpenAI-compatible endpoint.
	BaseURL string
	// APIKeyEnv names the environment variable holding the router key. The
	// caller sets it in the command's environment.
	APIKeyEnv string
	// LastMessageFile is where the agent should write its final message,
	// which becomes the job's summary.
	LastMessageFile string
}

// Agent builds the command that runs a coding agent on a task.
type Agent interface {
	Name() string
	Command(ctx context.Context, t Task) *exec.Cmd
}

// New returns the agent the config selects.
func New(c config.Agent) (Agent, error) {
	switch c.Name {
	case "codex":
		return &Codex{Path: c.Command, Args: c.Args}, nil
	default:
		return nil, fmt.Errorf("unknown agent %q", c.Name)
	}
}

// Codex runs OpenAI's Codex CLI non-interactively (`codex exec`). The router
// is passed as a custom model provider with -c overrides, so the user's
// ~/.codex/config.toml needs no changes.
type Codex struct {
	Path string
	Args []string
}

// Name implements Agent.
func (c *Codex) Name() string { return "codex" }

// Command implements Agent. The prompt is read from stdin.
func (c *Codex) Command(ctx context.Context, t Task) *exec.Cmd {
	args := []string{
		"exec",
		"--cd", t.Dir,
		"--sandbox", "workspace-write",
		"--color", "never",
		"-c", `model_provider="dotshift"`,
		"-c", `model_providers.dotshift.name="dotshift"`,
		"-c", "model_providers.dotshift.base_url=" + strconv.Quote(t.BaseURL),
		"-c", "model_providers.dotshift.env_key=" + strconv.Quote(t.APIKeyEnv),
		"-c", `model_providers.dotshift.wire_api="responses"`,
		"--model", t.Model,
		"--output-last-message", t.LastMessageFile,
	}
	args = append(args, c.Args...)
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Dir = t.Dir
	return cmd
}
