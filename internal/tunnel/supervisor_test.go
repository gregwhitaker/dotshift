package tunnel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runSupervisorTest(t *testing.T, minDelay, maxDelay time.Duration) (*lockedBuffer, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	log := &lockedBuffer{}
	go func() {
		defer close(done)
		superviseFunnel(ctx, "8787", io.Discard, func(format string, args ...any) {
			fmt.Fprintf(log, format+"\n", args...)
		}, minDelay, maxDelay)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("tunnel supervisor did not stop")
		}
	}
	t.Cleanup(stop)
	return log, stop
}

func waitSupervisorLog(t *testing.T, log *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(log.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("missing %q in supervisor log: %s", want, log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSuperviseFunnelRetriesStartupAndUnexpectedExit(t *testing.T) {
	previousGrace := startupGrace
	startupGrace = 30 * time.Millisecond
	defer func() { startupGrace = previousGrace }()
	dir := t.TempDir()
	t.Setenv("DOTSHIFT_TUNNEL_TEST_STATE", dir)
	fakeTailscale(t, `{"BackendState":"Running","Self":{"DNSName":"laptop.tail1234.ts.net."}}`, `
count=0
if [ -f "$DOTSHIFT_TUNNEL_TEST_STATE/attempts" ]; then count=$(cat "$DOTSHIFT_TUNNEL_TEST_STATE/attempts"); fi
count=$((count+1))
echo "$count" > "$DOTSHIFT_TUNNEL_TEST_STATE/attempts"
case "$count" in
1) echo 'listener already exists'; exit 1 ;;
2) sleep 0.2; exit 1 ;;
*) trap 'echo stopped > "$DOTSHIFT_TUNNEL_TEST_STATE/stopped"; exit 0' INT; while :; do sleep 0.05; done ;;
esac`)
	log, stop := runSupervisorTest(t, 10*time.Millisecond, 20*time.Millisecond)
	waitSupervisorLog(t, log, "listener already exists")
	waitSupervisorLog(t, log, "unavailable: exit status 1")
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(log.String(), "is serving MCP") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("tunnel did not recover from its exit: %s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if count, _ := os.ReadFile(filepath.Join(dir, "attempts")); strings.TrimSpace(string(count)) != "3" {
		t.Fatalf("expected exactly three attempts, got %s", count)
	}
	if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
		t.Fatalf("active tunnel was not stopped: %v", err)
	}
}

func TestSuperviseFunnelRecoversWhenTailscaleComesOnline(t *testing.T) {
	previousGrace := startupGrace
	startupGrace = 30 * time.Millisecond
	defer func() { startupGrace = previousGrace }()
	dir := t.TempDir()
	t.Setenv("DOTSHIFT_TUNNEL_TEST_STATE", dir)
	bin := filepath.Join(dir, "tailscale")
	script := `#!/bin/sh
case "$1" in
status)
  if [ -f "$DOTSHIFT_TUNNEL_TEST_STATE/online" ]; then
    echo '{"BackendState":"Running","Self":{"DNSName":"laptop.tail1234.ts.net."}}'
  else
    echo '{"BackendState":"Stopped"}'
  fi ;;
funnel) trap 'exit 0' INT; while :; do sleep 0.05; done ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log, _ := runSupervisorTest(t, 10*time.Millisecond, 20*time.Millisecond)
	waitSupervisorLog(t, log, "Tailscale is stopped")
	if err := os.WriteFile(filepath.Join(dir, "online"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitSupervisorLog(t, log, "is serving MCP at https://laptop.tail1234.ts.net/mcp")
}

func TestSuperviseFunnelCancelsBackoff(t *testing.T) {
	fakeTailscale(t, `{"BackendState":"Stopped"}`, "exit 1")
	log, stop := runSupervisorTest(t, time.Minute, time.Minute)
	waitSupervisorLog(t, log, "retrying in 1m0s")
	stop() // must return promptly, rather than waiting for the retry timer
}

func TestSuperviseFunnelCancelsStartup(t *testing.T) {
	previousGrace := startupGrace
	startupGrace = time.Minute
	defer func() { startupGrace = previousGrace }()
	bin := fakeTailscale(t, "{}", `trap 'exit 0' INT; echo started; while :; do sleep 0.05; done`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() {
		_, err := startFunnel(ctx, bin, "8787", out)
		done <- err
	}()
	waitSupervisorLog(t, out, "started")
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("startup cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation waited for the startup grace period")
	}
}
