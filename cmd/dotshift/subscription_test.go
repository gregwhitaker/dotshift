package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestSubscriptionPrepareDoesNotCreateRouter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dotshift.yaml")
	if err := runInitMode(path, false, config.ModeSubscription); err != nil {
		t.Fatal(err)
	}
	cfg, router, err := prepare(path)
	if err != nil || router != nil || !cfg.UsesSubscriptions() {
		t.Fatalf("prepare: cfg=%+v, router=%v, err=%v", cfg, router, err)
	}
	dir, _ := stateDir(path)
	for _, name := range []string{"master_key", "litellm.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("router artifact %s: %v", name, err)
		}
	}
	runner, closeStore, err := newRunner(cfg, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	if _, ok := runner.Agent.(*agent.Subscriptions); !ok || runner.APIKey != "" || runner.BaseURL != "" {
		t.Fatalf("wrong subscription runner: %+v", runner)
	}
	if err := runKey(path); err == nil {
		t.Fatal("subscription mode exposed an API key")
	}
	if err := runLiteLLMConfig(path, io.Discard); err == nil {
		t.Fatal("subscription mode generated router config")
	}
	if err := runStatus(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if err := runStop(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if err := runUsage(path, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionServeWithoutDocker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dotshift.yaml")
	content := strings.Replace(config.SubscriptionSample, "tunnel: tailscale", "tunnel: none\n  listen: 127.0.0.1:0", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // neither Docker nor model CLIs installed
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runServe(ctx, path, false); err != nil {
		t.Fatal(err)
	}
}
