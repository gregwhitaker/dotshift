package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestCodexCommand(t *testing.T) {
	a, err := New(config.Agent{Name: "codex", Command: "codex", Args: []string{"--ephemeral"}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.Command(context.Background(), Task{
		Dir: "/wt/abc", Model: "smart", BaseURL: "http://127.0.0.1:4000/v1",
		APIKeyEnv: "DOTSHIFT_API_KEY", LastMessageFile: "/state/abc.last",
	})
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"codex exec --cd /wt/abc --sandbox workspace-write",
		`-c model_provider="dotshift"`,
		`-c model_providers.dotshift.base_url="http://127.0.0.1:4000/v1"`,
		`-c model_providers.dotshift.env_key="DOTSHIFT_API_KEY"`,
		`-c model_providers.dotshift.wire_api="responses"`,
		"--model smart --output-last-message /state/abc.last --ephemeral -",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	if cmd.Dir != "/wt/abc" {
		t.Errorf("dir = %q", cmd.Dir)
	}
}

func TestUnknownAgent(t *testing.T) {
	if _, err := New(config.Agent{Name: "aider"}); err == nil {
		t.Fatal("want error")
	}
}
