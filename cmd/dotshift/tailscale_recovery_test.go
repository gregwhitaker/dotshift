//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
)

func TestUIStartsBeforeTailscaleRespondsAndRecoversSameService(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOTSHIFT_TAILSCALE_STARTUP_TEST_DIR", dir)
	script := `#!/bin/sh
case "$1" in
status)
  if [ -f "$DOTSHIFT_TAILSCALE_STARTUP_TEST_DIR/online" ]; then
    echo '{"BackendState":"Running","Self":{"DNSName":"laptop.tail1234.ts.net."}}'
  else
    touch "$DOTSHIFT_TAILSCALE_STARTUP_TEST_DIR/probed"
    sleep 3
    echo '{"BackendState":"Stopped"}'
  fi ;;
funnel)
  echo "$2" > "$DOTSHIFT_TAILSCALE_STARTUP_TEST_DIR/port"
  trap 'exit 0' INT TERM
  while :; do sleep 0.05; done ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(dir, "dotshift.yaml")
	content := strings.Replace(config.SubscriptionSample, "tunnel: tailscale", "tunnel: tailscale\n  listen: 127.0.0.1:0", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cli(t, "stop", "-config", path) })
	started := time.Now()
	if out, err := cli(t, "ui", "-no-open", "-config", path); err != nil {
		t.Fatalf("offline startup: %v %s", err, out)
	}
	if time.Since(started) >= 2*time.Second {
		t.Fatal("local startup waited for the offline Tailscale command")
	}
	state, _ := stateDir(path)
	before, err := dashboard.Discover(context.Background(), state, path)
	if err != nil || before == nil || !before.MCPEnabled {
		t.Fatalf("local service unavailable offline: %v", err)
	}
	// Confirm the slow probe really started before bringing the daemon online.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "probed")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Tailscale was not probed in the background")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "online"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var port string
	deadline = time.Now().Add(10 * time.Second)
	for port == "" {
		data, _ := os.ReadFile(filepath.Join(dir, "port"))
		port = strings.TrimSpace(string(data))
		if time.Now().After(deadline) {
			t.Fatal("Tailscale becoming available did not start Funnel")
		}
		if port == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/.well-known/oauth-protected-resource", port))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var metadata map[string]any
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["resource"] != "https://laptop.tail1234.ts.net/mcp" {
		t.Fatalf("recovered OAuth metadata lost its public address: %v", metadata)
	}
	after, err := dashboard.Discover(context.Background(), state, path)
	if err != nil || after == nil || after.Instance != before.Instance || after.PID != before.PID {
		t.Fatalf("recovery required a local service restart: %+v, %v", after, err)
	}
}
