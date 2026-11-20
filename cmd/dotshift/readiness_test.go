package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
)

func TestReadinessRequiresContactAndExpiresWithoutClaimingConnection(t *testing.T) {
	now := time.Now()
	calls := 0
	m := &readinessMonitor{now: func() time.Time { return now }, probe: func(context.Context) []dashboard.ReadinessCheck {
		calls++
		return []dashboard.ReadinessCheck{{Name: "Coding access", State: "ready"}, {Name: "Dot connection", State: "unchecked"}}
	}}
	first, err := m.Check(context.Background(), false)
	if err != nil || first.State != "unchecked" || first.Label != "Waiting for Dot" {
		t.Fatalf("health alone claimed contact: %+v %v", first, err)
	}
	m.contact()
	ready := m.Current()
	if ready.State != "ready" || ready.LastContact == nil {
		t.Fatalf("missing authenticated contact: %+v", ready)
	}
	ready.Checks[0].State = "attention"
	if m.Current().State != "ready" {
		t.Fatal("caller mutated the shared cache")
	}
	_, _ = m.Check(context.Background(), false)
	if calls != 1 {
		t.Fatal("polling repeated expensive checks")
	}
	now = now.Add(4 * time.Second)
	_, _ = m.Check(context.Background(), true)
	if calls != 2 {
		t.Fatal("Check again did not refresh")
	}
	now = now.Add(2 * time.Minute)
	if m.Current().State != "unchecked" {
		t.Fatal("stale readiness stayed green")
	}
	now = now.Add(25 * time.Hour)
	old, _ := m.Check(context.Background(), false)
	if old.Label != "Waiting for Dot" {
		t.Fatal("old contact claimed a current connection")
	}
}

func TestReadinessDoesNotHideProblemsBehindRecentContact(t *testing.T) {
	for _, name := range []string{"Local workspace", "Coding access", "Dot connection"} {
		m := &readinessMonitor{now: time.Now, lastSeen: time.Now(), cached: &dashboard.Readiness{CheckedAt: time.Now(), Checks: []dashboard.ReadinessCheck{{Name: name, State: "attention", Label: "Access needed", Detail: "Restore access"}}}}
		got := m.Current()
		if got.State != "attention" || got.Detail != "Restore access" || got.Message == "" {
			t.Fatalf("hidden %s failure: %+v", name, got)
		}
	}
}

func TestReadinessRecoversPromptlyFromOlderTunnelFailure(t *testing.T) {
	now := time.Now()
	calls := 0
	m := &readinessMonitor{now: func() time.Time { return now }, probe: func(context.Context) []dashboard.ReadinessCheck {
		calls++
		return []dashboard.ReadinessCheck{{Name: "Coding access", State: "ready"}, {Name: "Dot connection", State: "attention", Label: "Waiting for Tailscale"}}
	}}
	_, _ = m.Check(context.Background(), false)
	now = now.Add(6 * time.Second)
	_, _ = m.Check(context.Background(), false)
	if calls != 2 {
		t.Fatal("an offline connection waited a full minute to retry")
	}
	now = now.Add(time.Second)
	m.contact()
	if got := m.Current(); got.State != "ready" {
		t.Fatalf("new authenticated contact left a stale outage: %+v", got)
	}
	m.cached.Checks[0].State = "attention"
	if got := m.Current(); got.State != "attention" {
		t.Fatal("contact hid an unrelated coding-access problem")
	}
}

func TestReadinessCoalescesChecksWithoutBlockingStatus(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m := &readinessMonitor{now: time.Now, probe: func(context.Context) []dashboard.ReadinessCheck {
		calls.Add(1)
		close(started)
		<-finish
		return []dashboard.ReadinessCheck{{Name: "Dot connection", State: "unchecked"}}
	}}
	done := make(chan error, 2)
	go func() { _, err := m.Check(context.Background(), false); done <- err }()
	<-started
	if m.Current() != nil {
		t.Fatal("unfinished probe returned a successful status")
	}
	go func() { _, err := m.Check(context.Background(), true); done <- err }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Check(ctx, true); err == nil {
		t.Fatal("cancelled waiter did not stop")
	}
	close(finish)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent windows repeated the check")
	}
}

func TestCodingReadinessUsesConfiguredFallbackAndReviewerWithoutCoding(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "calls")
	codex := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + trace + "'\ncase \"$*\" in\n 'exec --help') echo '--ignore-user-config --json';;\n 'login status') echo 'Logged in using ChatGPT';;\n *) exit 99;;\nesac\n"
	if err := os.WriteFile(codex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, reviewer := range []string{"auto", "review"} {
		workers := "anthropic/default, openai/default"
		if reviewer == "review" {
			workers = "openai/default"
		}
		cfg, err := config.Parse([]byte("aliases:\n  smart: [" + workers + "]\n  review: [anthropic/default]\njobs:\n  mode: subscription\n  model: smart\n  reviewer_model: " + reviewer + "\n  agent:\n    command: " + codex + "\n  claude_command: " + filepath.Join(dir, "missing-claude") + "\nmcp:\n  disabled: true\n"))
		if err != nil {
			t.Fatal(err)
		}
		runner, closeRunner, err := newRunner(cfg, nil, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		state := checkCoding(context.Background(), cfg, runner, nil)
		closeRunner()
		if (state.State == "ready") != (reviewer == "auto") {
			t.Fatalf("reviewer=%s readiness=%+v", reviewer, state)
		}
	}
	calls, _ := os.ReadFile(trace)
	if strings.TrimSpace(string(calls)) != "exec --help\nlogin status\nexec --help\nlogin status" {
		t.Fatalf("unexpected model call: %s", calls)
	}
}
