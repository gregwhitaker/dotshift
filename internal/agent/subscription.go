package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
)

// SetupError needs local installation/sign-in, not repeated model attempts.
type SetupError struct{ Message string }

func (e *SetupError) Error() string { return e.Message }

// Subscriptions delegates authentication to the unmodified, official CLIs.
// It never reads, copies or serves their OAuth credentials.
type Subscriptions struct{ CodexPath, ClaudePath string }

func (s *Subscriptions) Name() string { return "subscription CLI" }

// SubscriptionEnv prevents ambient API credentials or provider overrides from
// turning a subscription invocation into a separately billed API invocation.
// HOME and the CLI configuration directories remain available for native login.
func SubscriptionEnv(env []string) []string {
	var out []string
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "OPENAI_") || strings.HasPrefix(key, "ANTHROPIC_") ||
			strings.HasPrefix(key, "CLAUDE_CODE_") || strings.HasPrefix(key, "CODEX_") && key != "CODEX_HOME" ||
			key == "DOTSHIFT_API_KEY" || key == "CLAUDECODE" {
			continue
		}
		out = append(out, item)
	}
	return out
}

const claudeSettings = `{"forceLoginMethod":"claudeai","disableAllHooks":true,"permissions":{"blockWritesOutsideWorkingDirectories":true},"sandbox":{"enabled":true,"failIfUnavailable":true,"autoAllowBashIfSandboxed":true,"allowUnsandboxedCommands":false}}`

func claudeBaseArgs() []string {
	return []string{"--restricted", "--safe-mode", "--settings", claudeSettings}
}

// Check verifies local CLI capabilities and subscription authentication without
// generating any model output. Raw auth output (which can include email) stays local.
func (s *Subscriptions) Check(ctx context.Context, provider string) error {
	path, helpArgs, authArgs, needed, login := s.CodexPath, []string{"exec", "--help"}, []string{"login", "status"}, []string{"--ignore-user-config", "--json"}, "codex login"
	if provider == "anthropic" {
		path, helpArgs, authArgs = s.ClaudePath, []string{"--help"}, append(claudeBaseArgs(), "auth", "status")
		needed, login = []string{"--restricted", "--safe-mode", "--json-schema"}, "claude auth login"
	} else if provider != "openai" {
		return &SetupError{"subscription mode only supports openai and anthropic"}
	}
	if _, err := exec.LookPath(path); err != nil {
		return &SetupError{fmt.Sprintf("%s CLI not found at %q; install the official CLI, then run `%s` with your subscription account", provider, path, login)}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	run := func(args []string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = SubscriptionEnv(os.Environ())
		// Never load repository-specific configuration during authentication.
		cmd.Dir = os.TempDir()
		return cmd.CombinedOutput()
	}
	help, err := run(helpArgs)
	if err != nil {
		return &SetupError{fmt.Sprintf("cannot inspect %s CLI; update the official CLI and retry", provider)}
	}
	for _, flag := range needed {
		if !strings.Contains(string(help), flag) {
			return &SetupError{fmt.Sprintf("update the %s CLI: subscription mode requires %s", provider, flag)}
		}
	}
	data, err := run(authArgs)
	ok := err == nil
	if provider == "openai" {
		ok = ok && strings.Contains(strings.ToLower(string(data)), "logged in using chatgpt")
	} else {
		var status struct {
			LoggedIn   bool   `json:"loggedIn"`
			AuthMethod string `json:"authMethod"`
		}
		ok = ok && json.Unmarshal(data, &status) == nil && status.LoggedIn && status.AuthMethod == "claude.ai"
	}
	if !ok {
		return &SetupError{fmt.Sprintf("%s subscription sign-in not confirmed; run `%s` using your subscription account (API-key sign-in is not accepted)", provider, login)}
	}
	return nil
}

func (s *Subscriptions) Command(ctx context.Context, t Task) *exec.Cmd {
	provider, model, _ := config.SplitModel(t.Model)
	var cmd *exec.Cmd
	if provider == "anthropic" {
		toolset := "Read,Glob,Grep,Edit,Write,Bash"
		if t.ReadOnly {
			toolset = "Read,Glob,Grep"
		}
		args := append(claudeBaseArgs(), "-p", "--output-format", "stream-json", "--verbose",
			"--no-session-persistence", "--permission-mode", "dontAsk", "--tools", toolset, "--allowedTools", toolset)
		if model != "default" {
			args = append(args, "--model", model)
		}
		if t.OutputSchemaFile != "" {
			// Goal reports use the same schema as Codex. Reading/validating the
			// supplied schema is done by Prepare before starting the command.
			schema, _ := os.ReadFile(t.OutputSchemaFile)
			args = append(args, "--json-schema", string(schema))
		}
		cmd = exec.CommandContext(ctx, s.ClaudePath, args...)
	} else {
		sandbox := "workspace-write"
		if t.ReadOnly {
			sandbox = "read-only"
		}
		args := []string{"exec", "--ignore-user-config", "--ephemeral", "--cd", t.Dir, "--sandbox", sandbox,
			"--color", "never", "--json", "-c", `model_provider="openai"`,
			"-c", `forced_login_method="chatgpt"`, "-c", `approval_policy="never"`,
			"--output-last-message", t.LastMessageFile}
		if model != "default" {
			args = append(args, "--model", model)
		}
		if t.OutputSchemaFile != "" {
			args = append(args, "--output-schema", t.OutputSchemaFile)
		}
		cmd = exec.CommandContext(ctx, s.CodexPath, append(args, "-")...)
	}
	cmd.Dir, cmd.Env = t.Dir, SubscriptionEnv(os.Environ())
	return cmd
}

// Prepare attaches the CLI's structured event observer. Existing API agents
// retain their command implementation and raw logging behavior.
func Prepare(ctx context.Context, a Agent, t Task, out io.Writer, cancel context.CancelFunc) (*exec.Cmd, func() error, error) {
	s, subscription := a.(*Subscriptions)
	if !subscription {
		cmd := a.Command(ctx, t)
		cmd.Stdout, cmd.Stderr = out, out
		return cmd, func() error { return nil }, nil
	}
	provider, _, ok := config.SplitModel(t.Model)
	if !ok {
		return nil, nil, &SetupError{"subscription task requires provider/model"}
	}
	if err := s.Check(ctx, provider); err != nil {
		return nil, nil, err
	}
	if t.OutputSchemaFile != "" {
		data, err := os.ReadFile(t.OutputSchemaFile)
		if err != nil {
			return nil, nil, err
		}
		if !json.Valid(data) {
			return nil, nil, errors.New("invalid output schema")
		}
	}
	cmd := s.Command(ctx, t)
	stream := &subscriptionStream{provider: provider, task: t, out: out, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stream, out
	return cmd, stream.finish, nil
}
