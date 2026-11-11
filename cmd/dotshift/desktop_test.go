//go:build unix

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
	"github.com/gregwhitaker/dotshift/internal/jobs"
)

func TestDesktopPollingNeverStartsOrStopsService(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotshift.yaml")
	if err := os.WriteFile(path, []byte(strings.Replace(config.SubscriptionSample, "tunnel: tailscale", "disabled: true", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	path, _ = canonicalConfig(path)
	state, _ := stateDir(path)
	snapshot, err := desktopStatus(context.Background(), path)
	if err != nil || snapshot.Running || string(snapshot.Jobs) != "[]" {
		t.Fatalf("stopped status: %+v %v", snapshot, err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("status created service state")
	}
	t.Cleanup(func() { _, _ = cli(t, "stop", "-config", path) })
	if output, err := cli(t, "ui", "-no-open", "-config", path); err != nil {
		t.Fatalf("start: %v %s", err, output)
	}
	before, err := dashboard.Discover(context.Background(), state, path)
	if err != nil || before == nil {
		t.Fatalf("missing service: %v", err)
	}
	store, err := jobs.OpenStore(filepath.Join(state, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Save(context.Background(), &jobs.Job{ID: "attention", Repo: "test", Prompt: "Keep my work", Status: jobs.Blocked, CreatedAt: time.Now()})
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Every poll process exits. The same service must survive those exits.
	for range 3 {
		output, err := cli(t, "desktop", "status", "-config", path)
		if err != nil {
			t.Fatalf("poll: %v %s", err, output)
		}
		var got desktopSnapshot
		if err := json.Unmarshal([]byte(output), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Running || got.MCPEnabled || !strings.Contains(string(got.Jobs), "Keep my work") {
			t.Fatalf("wrong snapshot: %+v", got)
		}
		if strings.Contains(output, before.Token) || strings.Contains(output, "Bearer") {
			t.Fatal("bridge exposed session credentials")
		}
	}
	after, err := dashboard.Discover(context.Background(), state, path)
	if err != nil || after == nil || before.PID != after.PID || before.Instance != after.Instance {
		t.Fatalf("polling disturbed the service: %+v %v", after, err)
	}
	output, err := cli(t, "ui", "-no-open", "-path", "/models", "-config", path)
	if err != nil || !strings.Contains(output, "/models#token=") {
		t.Fatalf("deep link: %s %v", output, err)
	}
}

func TestDesktopConnectionsCheckSignInWithoutModelRequests(t *testing.T) {
	dir := t.TempDir()
	agentPath := filepath.Join(dir, "codex")
	trace := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + trace + "'\ncase \"$*\" in\n 'exec --help') echo '--ignore-user-config --json';;\n 'login status') echo 'Logged in using ChatGPT';;\n *) exit 99;;\nesac\n"
	if err := os.WriteFile(agentPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dotshift.yaml")
	content := "aliases:\n  smart: [openai/default]\njobs:\n  mode: subscription\n  model: smart\n  agent:\n    command: " + agentPath + "\nmcp:\n  disabled: true\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := desktopConnections(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Status != "Stopped" || rows[1].Status != "Off" || rows[2].Name != "Codex" || rows[2].Status != "Signed in" {
		t.Fatalf("wrong connections: %+v", rows)
	}
	calls, _ := os.ReadFile(trace)
	if string(calls) != "exec --help\nlogin status\n" {
		t.Fatalf("unexpected agent calls: %s", calls)
	}
}
