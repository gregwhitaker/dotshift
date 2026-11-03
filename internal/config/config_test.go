package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSampleParses(t *testing.T) {
	c, err := Parse([]byte(Sample))
	if err != nil {
		t.Fatalf("sample config is invalid: %v", err)
	}
	if got, want := c.AliasNames(), []string{"fast", "local", "smart"}; !reflect.DeepEqual(got, want) {
		t.Errorf("aliases = %v, want %v", got, want)
	}
	if got, want := c.KeyEnvs(), []string{"ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENAI_API_KEY"}; !reflect.DeepEqual(got, want) {
		t.Errorf("key envs = %v, want %v", got, want)
	}
}

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte("providers:\n  openai: {api_key_env: OPENAI_API_KEY}\naliases:\n  smart: [openai/gpt-5]\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := c.Router
	if r.Host != "127.0.0.1" || r.Port != 4000 || r.Container != "dotshift-litellm" || !strings.Contains(r.Image, "litellm") {
		t.Errorf("unexpected defaults: %+v", r)
	}
	if c.MCP.Listen != "127.0.0.1:8787" || c.MCP.Disabled || c.MCP.Tunnel != TunnelTailscale {
		t.Errorf("unexpected MCP defaults: %+v", c.MCP)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{"no aliases", "providers:\n  openai: {}\n", "at least one alias"},
		{"unknown provider", "providers:\n  acme: {}\naliases:\n  a: [acme/x]\n", "providers.acme: unknown provider"},
		{"provider not configured", "providers:\n  openai: {}\naliases:\n  a: [anthropic/claude]\n", `uses provider "anthropic"`},
		{"model without provider", "providers:\n  openai: {}\naliases:\n  a: [gpt-5]\n", "must look like provider/model"},
		{"empty alias", "providers:\n  openai: {}\naliases:\n  a: []\n", "needs at least one model"},
		{"bad alias name", "providers:\n  openai: {}\naliases:\n  Smart: [openai/gpt-5]\n", "aliases.Smart"},
		{"bad env name", "providers:\n  openai: {api_key_env: 'my key'}\naliases:\n  a: [openai/gpt-5]\n", "not a valid environment variable"},
		{"bad port", "router: {port: 70000}\nproviders:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\n", "out of range"},
		{"job model not an alias", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\njobs: {model: b}\n", `jobs.model "b"`},
		{"bad timeout", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\njobs: {timeout: soon}\n", "not a duration"},
		{"unknown agent", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\njobs: {agent: {name: aider}}\n", "not a known agent"},
		{"repo without path", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\nrepos:\n  app: {test: make}\n", "repos.app.path is required"},
		{"unknown field", "providers:\n  openai: {apikey: X}\naliases:\n  a: [openai/gpt-5]\n", "field apikey not found"},
		{"unknown tunnel", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\nmcp: {tunnel: ngrok}\n", "must be tailscale or none"},
		{"bad mcp listen", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\nmcp: {listen: 8787}\n", "mcp.listen"},
		{"mcp public url with path", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\nmcp: {public_url: https://x.example/mcp}\n", "mcp.public_url"},
		{"mcp public url without scheme", "providers:\n  openai: {}\naliases:\n  a: [openai/gpt-5]\nmcp: {public_url: x.example}\n", "mcp.public_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestSplitModel(t *testing.T) {
	p, m, ok := SplitModel("openai/org/model")
	if !ok || p != "openai" || m != "org/model" {
		t.Errorf("SplitModel = %q %q %v", p, m, ok)
	}
	if _, _, ok := SplitModel("/x"); ok {
		t.Error("empty provider accepted")
	}
}

func TestJobDefaults(t *testing.T) {
	c, err := Parse([]byte("providers:\n  openai: {}\naliases:\n  fast: [openai/a]\n  smart: [openai/b]\n"))
	if err != nil {
		t.Fatal(err)
	}
	j := c.Jobs
	if j.Model != "smart" || j.Timeout.Duration != time.Hour || j.Agent.Name != "codex" || j.Agent.Command != "codex" {
		t.Errorf("unexpected job defaults: %+v", j)
	}
	c, err = Parse([]byte("providers:\n  openai: {}\naliases:\n  quick: [openai/a]\njobs: {timeout: 45m}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Jobs.Model != "quick" || c.Jobs.Timeout.Duration != 45*time.Minute {
		t.Errorf("unexpected jobs: %+v", c.Jobs)
	}
}
