//go:build unix

package tunnel

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The macOS installation puts a shell wrapper on PATH. Interrupting just
// that shell leaves the actual CLI alive, still owning Funnel's listener.
func TestStopFunnelReachesWrapperChild(t *testing.T) {
	previousGrace := startupGrace
	startupGrace = 50 * time.Millisecond
	defer func() { startupGrace = previousGrace }()
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	stopped := filepath.Join(dir, "stopped")
	script := "#!/bin/sh\ntrap 'echo stopped > \"$1\"; exit 0' INT TERM\necho child-ready=$$\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(child, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Preserve the wrapper's usual foreground child instead of exec'ing it.
	bin := fakeTailscale(t, "{}", "\""+child+"\" \""+stopped+"\"")
	f, err := StartFunnel(bin, "8787", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Stop)
	waitFunnelOutput(t, f, "child-ready=")
	pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(f.out.String(), "child-ready=")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child, err := os.FindProcess(pid); err == nil {
			_ = child.Kill()
		}
	})
	f.Stop()
	data, err := os.ReadFile(stopped)
	if err != nil || strings.TrimSpace(string(data)) != "stopped" {
		t.Fatalf("the wrapper's child did not receive the stop signal: %v", err)
	}
}
