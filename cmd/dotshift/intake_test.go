package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestIntakeRetriesOfflineTunnelAndUsesBoundPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOTSHIFT_INTAKE_TEST_STATE", dir)
	bin := filepath.Join(dir, "tailscale")
	script := `#!/bin/sh
case "$1" in
status)
  if [ -f "$DOTSHIFT_INTAKE_TEST_STATE/online" ]; then
    echo '{"BackendState":"Running","Self":{"DNSName":"laptop.tail1234.ts.net."}}'
  else
    echo '{"BackendState":"Stopped"}'
  fi ;;
funnel)
  trap 'echo stopped > "$DOTSHIFT_INTAKE_TEST_STATE/stopped"; exit 0' INT
  echo "$2" > "$DOTSHIFT_INTAKE_TEST_STATE/port"
  while :; do sleep 0.05; done ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg, err := config.Parse([]byte(config.SubscriptionSample))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MCP.Listen = "127.0.0.1:0"
	runner, closeStore, err := newRunner(cfg, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeStore)
	stop, err := startIntake(cfg, runner, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	// Recovery must happen even when the first Tailscale status check failed.
	if err := os.WriteFile(filepath.Join(dir, "online"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var port string
	deadline := time.Now().Add(5 * time.Second)
	for port == "" && time.Now().Before(deadline) {
		data, _ := os.ReadFile(filepath.Join(dir, "port"))
		port = strings.TrimSpace(string(data))
		if port == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if port == "" || port == "0" {
		t.Fatalf("tunnel did not recover with the actual listener port: %q", port)
	}
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get(fmt.Sprintf("http://%s/healthz", net.JoinHostPort("127.0.0.1", port)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("recovered intake: HTTP %d", res.StatusCode)
	}
	stop()
	if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
		t.Fatalf("intake shutdown left the tunnel running: %v", err)
	}
}
