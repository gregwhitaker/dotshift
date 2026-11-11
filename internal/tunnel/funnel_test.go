package tunnel

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeTailscale writes a stand-in tailscale CLI and puts it first on PATH.
// status prints statusJSON; funnel runs funnelScript.
func fakeTailscale(t *testing.T, statusJSON, funnelScript string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\nstatus) cat <<'JSON'\n" + statusJSON + "\nJSON\n;;\nfunnel) " + funnelScript + " ;;\nesac\n"
	path := filepath.Join(dir, "tailscale")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestPublicURL(t *testing.T) {
	tests := []struct{ status, want, err string }{
		{`{"BackendState":"Running","Self":{"DNSName":"laptop.tail1234.ts.net."}}`, "https://laptop.tail1234.ts.net", ""},
		{`{"BackendState":"NeedsLogin","Self":{"DNSName":""}}`, "", "needslogin"},
		{`{"BackendState":"Running","Self":{"DNSName":""}}`, "", "MagicDNS"},
	}
	for _, tt := range tests {
		fakeTailscale(t, tt.status, "true")
		bin, err := FindTailscale()
		if err != nil {
			t.Fatal(err)
		}
		got, err := PublicURL(context.Background(), bin)
		if got != tt.want || (tt.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.err)) {
			t.Errorf("status %s: got %q, %v", tt.status, got, err)
		}
	}
}

func TestStartFunnel(t *testing.T) {
	startupGrace = 300 * time.Millisecond
	bin := fakeTailscale(t, "{}", `echo "Available on the internet: https://laptop.tail1234.ts.net/ -> proxy http://127.0.0.1:$2"; trap 'echo stopped; exit 0' INT; while :; do sleep 0.05; done`)
	var log bytes.Buffer
	f, err := StartFunnel(bin, "8787", &log)
	if err != nil {
		t.Fatal(err)
	}
	waitFunnelOutput(t, f, "proxy http://127.0.0.1:8787")
	f.Stop()
	select {
	case <-f.Done():
	default:
		t.Fatal("funnel still running after Stop")
	}
	if !strings.Contains(log.String(), "proxy http://127.0.0.1:8787") {
		t.Errorf("log = %q", log.String())
	}
}

func TestStartFunnelNotEnabled(t *testing.T) {
	startupGrace = 2 * time.Second
	bin := fakeTailscale(t, "{}", `echo "Funnel is not enabled on your tailnet. To enable, visit: https://login.tailscale.com/f/funnel?node=abc"; exit 1`)
	_, err := StartFunnel(bin, "8787", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "https://login.tailscale.com/f/funnel") {
		t.Fatalf("err = %v", err)
	}
}

func TestStopFunnelWithInheritedOutputPipe(t *testing.T) {
	previousGrace := startupGrace
	startupGrace = 50 * time.Millisecond
	defer func() { startupGrace = previousGrace }()
	// The child outlives the CLI and inherits its stdout/stderr. Wait must
	// still return after the CLI exits instead of waiting for the child.
	bin := fakeTailscale(t, "{}", `sh -c 'trap "" INT; exec sleep 10' & child=$!; trap 'exit 0' INT; echo "child=$child"; while :; do sleep 0.05; done`)
	f, err := StartFunnel(bin, "8787", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	waitFunnelOutput(t, f, "child=")
	pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(f.out.String(), "child=")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child, err := os.FindProcess(pid); err == nil {
			_ = child.Kill()
		}
	}()
	stopped := make(chan struct{})
	go func() { f.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown waited for a descendant's inherited output pipe")
	}
}

func waitFunnelOutput(t *testing.T, f *Funnel, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(f.out.String(), want) {
		if time.Now().After(deadline) {
			f.Stop()
			t.Fatalf("funnel output missing %q: %s", want, f.out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
