//go:build unix

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
	"github.com/gregwhitaker/dotshift/internal/jobs"
)

// The test executable exercises the real CLI entrypoint in child processes.
// No installed agents, Docker, tunnels, or model calls are involved.
func TestMain(m *testing.M) {
	if os.Getenv("DOTSHIFT_UI_TEST_HELPER") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func cli(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), "DOTSHIFT_UI_TEST_HELPER=1")
	data, err := cmd.CombinedOutput()
	return string(data), err
}

func TestInitDefaultsToSubscriptionsAndKeepsAPIOptIn(t *testing.T) {
	for _, mode := range []string{"", config.ModeAPI} {
		path := filepath.Join(t.TempDir(), "dotshift.yaml")
		args := []string{"init", "-config", path}
		if mode != "" {
			args = append(args, "-mode", mode)
		}
		if output, err := cli(t, args...); err != nil {
			t.Fatalf("init: %v %s", err, output)
		}
		cfg, err := config.Load(path)
		if err != nil || cfg.UsesSubscriptions() != (mode == "") {
			t.Fatalf("wrong init mode %q: %+v %v", mode, cfg, err)
		}
		if mode == "" {
			for _, alias := range []string{"smart", "codex", "claude"} {
				if len(cfg.Aliases[alias]) == 0 {
					t.Fatalf("missing model choice %s", alias)
				}
			}
			if cfg.Jobs.Strategy != "prefer_openai" || cfg.Jobs.ReviewerModel != "auto" {
				t.Fatal("default setup lost fallback or separate review")
			}
		}
		before, _ := os.ReadFile(path)
		if _, err := cli(t, args...); err == nil {
			t.Fatal("init overwrote an existing configuration")
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("existing configuration changed")
		}
	}
}

func TestUILaunchReuseStopAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotshift.yaml")
	content := strings.Replace(config.SubscriptionSample, "tunnel: tailscale", "disabled: true", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := canonicalConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := stateDir(path)
	t.Cleanup(func() { _, _ = cli(t, "stop", "-config", path) })
	// Concurrent ui launches must converge on one service, even with MCP off.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := cli(t, "ui", "-no-open", "-config", path); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			log, _ := os.ReadFile(filepath.Join(state, "service.log"))
			t.Fatalf("ui launch: %v\n%s", err, log)
		}
	}
	record, err := dashboard.Discover(context.Background(), state, path)
	if err != nil || record == nil {
		t.Fatalf("service missing: %v", err)
	}
	res, err := record.Request(context.Background(), "GET", "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		MCPEnabled bool   `json:"mcp_enabled"`
		Mode       string `json:"execution_mode"`
	}
	json.NewDecoder(res.Body).Decode(&info)
	res.Body.Close()
	if info.MCPEnabled || info.Mode != "subscription" {
		t.Fatalf("wrong service: %+v", info)
	}
	if out, err := cli(t, "ui", "-no-open", "-config", path); err != nil || !strings.Contains(out, record.URL) {
		t.Fatalf("did not reuse service: %v", err)
	}
	if _, err := cli(t, "serve", "-config", path); err == nil {
		t.Fatal("second serve acquired the same state")
	}
	store, err := jobs.OpenStore(filepath.Join(state, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Save(context.Background(), &jobs.Job{ID: "preserved", Repo: "test", Prompt: "Keep this goal", Status: jobs.Blocked, CreatedAt: time.Now(), Goal: &jobs.GoalProgress{Phase: "working", Iterations: 2}})
	if err == nil {
		// A queued goal must be recovered even with MCP disabled. This one
		// intentionally fails repository validation before any agent can run.
		err = store.Save(context.Background(), &jobs.Job{ID: "recover", Repo: "missing", Model: "smart", ExecutionMode: config.ModeSubscription, Prompt: "Recover this goal", Status: jobs.Queued, CreatedAt: time.Now(), Goal: &jobs.GoalProgress{Phase: "working", Iterations: 3}})
	}
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cli(t, "stop", "-config", path); err != nil {
		t.Fatalf("stop: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(state, "service.json")); !os.IsNotExist(err) {
		t.Fatalf("receipt survived shutdown: %v", err)
	}
	if _, err := cli(t, "ui", "-no-open", "-config", path); err != nil {
		t.Fatal(err)
	}
	next, err := dashboard.Discover(context.Background(), state, path)
	if err != nil || next == nil || next.Instance == record.Instance {
		t.Fatalf("restart: %v", err)
	}
	res, err = next.Request(context.Background(), "GET", "/api/jobs/preserved")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("saved goal missing after restart: %d", res.StatusCode)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		res, err := next.Request(context.Background(), "GET", "/api/jobs/recover")
		if err != nil {
			t.Fatal(err)
		}
		var recovered struct {
			Status string            `json:"status"`
			Error  string            `json:"error"`
			Goal   jobs.GoalProgress `json:"goal"`
		}
		err = json.NewDecoder(res.Body).Decode(&recovered)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if recovered.Status == "blocked" && strings.Contains(recovered.Error, "not allowlisted") && recovered.Goal.Iterations == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("goal was not recovered with MCP disabled: %+v", recovered)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServiceLockReleasedAndCanonicalConfig(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockService(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := lockService(dir); err == nil {
		other()
		t.Fatal("duplicate lock acquired")
	}
	unlock()
	unlock, err = lockService(dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	path := filepath.Join(dir, "config.yaml")
	link := filepath.Join(dir, "alias.yaml")
	if err := os.WriteFile(path, []byte(config.SubscriptionSample), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	a, err := canonicalConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalConfig(link)
	if err != nil || a != b {
		t.Fatalf("config aliases differ: %v", err)
	}
}

func TestUILocalOnlyPreservesConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotshift.yaml")
	// The normal configuration enables Tailscale. The runtime flag must
	// suppress all MCP setup without rewriting that user-owned file.
	if err := os.WriteFile(path, []byte(config.SubscriptionSample), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := canonicalConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cli(t, "stop", "-config", path) })
	if out, err := cli(t, "ui", "-local-only", "-no-open", "-config", path); err != nil {
		t.Fatalf("local launch: %v %s", err, out)
	}
	state, _ := stateDir(path)
	record, err := dashboard.Discover(context.Background(), state, path)
	if err != nil || record == nil || record.MCPEnabled {
		t.Fatalf("local-only service: %+v %v", record, err)
	}
	if _, err := os.Stat(filepath.Join(state, "auth.db")); !os.IsNotExist(err) {
		t.Fatalf("MCP auth was initialized: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != config.SubscriptionSample {
		t.Fatal("local-only launch changed the saved config")
	}
}

func TestUILocalOnlyRefusesExistingMCPService(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dotshift.yaml")
	content := strings.Replace(config.SubscriptionSample, "tunnel: tailscale", "tunnel: none\n  listen: 127.0.0.1:0", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cli(t, "stop", "-config", path) })
	if out, err := cli(t, "ui", "-no-open", "-config", path); err != nil {
		t.Fatalf("MCP launch: %v %s", err, out)
	}
	if out, err := cli(t, "ui", "-local-only", "-no-open", "-config", path); err == nil || !strings.Contains(out, "existing service has MCP enabled") {
		t.Fatalf("local-only silently reused MCP: %v %s", err, out)
	}
}
