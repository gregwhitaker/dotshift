package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
)

func TestSubscriptionEnvironment(t *testing.T) {
	env := SubscriptionEnv([]string{"HOME=/home/me", "CODEX_HOME=/home/me/.codex", "PATH=/bin", "CLAUDE_CONFIG_DIR=/home/me/.claude", "OPENAI_API_KEY=secret", "CODEX_API_KEY=secret", "CODEX_ACCESS_TOKEN=secret", "ANTHROPIC_AUTH_TOKEN=secret", "ANTHROPIC_API_KEY=secret", "CLAUDE_CODE_OAUTH_TOKEN=secret", "CLAUDE_CODE_USE_BEDROCK=1", "OPENAI_BASE_URL=http://local", "DOTSHIFT_API_KEY=secret", "CLAUDECODE=1"})
	if got := strings.Join(env, "\n"); strings.Contains(got, "secret") || len(env) != 4 {
		t.Fatalf("unexpected environment: %v", env)
	}
}

func TestSubscriptionCommands(t *testing.T) {
	s := &Subscriptions{CodexPath: "codex", ClaudePath: "claude"}
	for _, provider := range []string{"openai", "anthropic"} {
		for _, review := range []bool{false, true} {
			cmd := s.Command(context.Background(), Task{Dir: "/worktree", Model: provider + "/default", ReadOnly: review, LastMessageFile: "/result"})
			args := strings.Join(cmd.Args, " ")
			if strings.Contains(args, "--model") || strings.Contains(args, "bypass") || strings.Contains(args, "--bare") {
				t.Fatalf("unexpected defaults: %s", args)
			}
			if provider == "openai" {
				if !strings.Contains(args, `forced_login_method="chatgpt"`) || !strings.Contains(args, "--ignore-user-config") || review && !strings.Contains(args, "--sandbox read-only") {
					t.Fatal(args)
				}
			} else {
				if !strings.Contains(args, "--restricted --safe-mode") || !strings.Contains(args, `"failIfUnavailable":true`) {
					t.Fatal(args)
				}
				if review && (!strings.Contains(args, "--tools Read,Glob,Grep --allowedTools Read,Glob,Grep") || strings.Contains(args, ",Bash") || strings.Contains(args, ",Edit") || strings.Contains(args, ",Write")) {
					t.Fatal("review has write tools: " + args)
				}
			}
		}
	}
}

func TestSubscriptionCheckRejectsAPILogin(t *testing.T) {
	for _, tc := range []struct {
		provider, status string
		valid            bool
	}{
		{"openai", "Logged in using ChatGPT", true},
		{"openai", "Logged in using an API key", false},
		{"anthropic", `{"loggedIn":true,"authMethod":"claude.ai"}`, true},
		{"anthropic", `{"loggedIn":true,"authMethod":"api_key"}`, false},
		{"anthropic", `{"loggedIn":true,"authMethod":"oauth_token"}`, false},
		{"anthropic", `{"loggedIn":false,"authMethod":"claude.ai"}`, false},
	} {
		t.Run(tc.provider+tc.status, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cli")
			script := "#!/bin/sh\ncase \"$*\" in *--help*) echo '--ignore-user-config --json --restricted --safe-mode --json-schema';; *) printf '%s\\n' '" + tc.status + "';; esac\n"
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			s := &Subscriptions{CodexPath: path, ClaudePath: path}
			err := s.Check(context.Background(), tc.provider)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}

func TestSubscriptionLimitEvents(t *testing.T) {
	for _, tc := range []struct {
		provider, line string
		limit          bool
	}{
		{"openai", `{"type":"turn.failed","error":{"message":"You've hit your usage limit"}}`, true},
		{"openai", `{"type":"error","error":{"type":"usage_limit_reached"}}`, true},
		{"openai", `{"type":"error","code":"usage_limit_reached"}`, true},
		{"openai", `{"type":"turn.failed","error":{"codexErrorInfo":"usageLimitExceeded"}}`, true},
		{"openai", `{"type":"turn.failed","error":{"message":"exceeded retry limit, last status: 429 Too Many Requests"}}`, true},
		{"openai", `{"type":"item.completed","item":{"type":"agent_message","text":"You've hit your usage limit"}}`, false},
		{"openai", `{"type":"item.completed","item":{"type":"command_execution","output":"usage_limit_reached"}}`, false},
		{"anthropic", `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`, true},
		{"anthropic", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning"}}`, false},
		{"anthropic", `{"type":"assistant","error":"rate_limit"}`, true},
		{"anthropic", `{"type":"assistant","message":{"content":[{"text":"usage limit reached"}]}}`, false},
		{"anthropic", `{"type":"assistant","error":"authentication_failed"}`, false},
	} {
		t.Run(tc.line, func(t *testing.T) {
			cancelled := false
			s := &subscriptionStream{provider: tc.provider, out: io.Discard, cancel: func() { cancelled = true }}
			// Exercise line framing across writes, including an unterminated last line.
			s.Write([]byte(tc.line[:13]))
			s.Write([]byte(tc.line[13:]))
			err := s.finish()
			var failure *modelpolicy.Failure
			limited := errors.As(err, &failure)
			if limited != tc.limit || cancelled != tc.limit {
				t.Fatalf("failure=%v, cancelled=%v", err, cancelled)
			}
			if limited && (!failure.ProviderWide || !failure.RetryAt.IsZero()) {
				t.Fatal(failure)
			}
		})
	}
}

func TestSubscriptionStructuredResultAndReset(t *testing.T) {
	last := filepath.Join(t.TempDir(), "summary")
	s := &subscriptionStream{provider: "anthropic", task: Task{OutputSchemaFile: "schema", LastMessageFile: last}, out: io.Discard}
	s.Write([]byte(`{"type":"result","subtype":"success","is_error":false,"structured_output":{"status":"complete","summary":"done","next_steps":""}}` + "\n"))
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(last)
	if err != nil || !strings.Contains(string(data), `"status":"complete"`) {
		t.Fatalf("report=%s, err=%v", data, err)
	}
	s = &subscriptionStream{provider: "anthropic", out: io.Discard}
	s.event([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true}`))
	if s.finish() == nil {
		t.Fatal("unsuccessful result accepted")
	}
	s = &subscriptionStream{provider: "anthropic", task: Task{OutputSchemaFile: "schema"}, out: io.Discard}
	s.event([]byte(`{"type":"result","subtype":"success","result":"I finished"}`))
	if s.finish() == nil {
		t.Fatal("unstructured goal accepted")
	}
	s = &subscriptionStream{provider: "anthropic", out: io.Discard}
	reset := time.Now().Add(time.Hour).Unix()
	s.limit(reset)
	s.event([]byte(`{"type":"assistant","error":"rate_limit"}`))
	var failure *modelpolicy.Failure
	if !errors.As(s.finish(), &failure) || failure.RetryAt.Unix() != reset {
		t.Fatal("reset not preserved")
	}
}

func TestSubscriptionRecoversFromTransientError(t *testing.T) {
	s := &subscriptionStream{provider: "openai", out: io.Discard}
	s.event([]byte(`{"type":"error","message":"Reconnecting... 1/5"}`))
	s.event([]byte(`{"type":"turn.completed"}`))
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}
