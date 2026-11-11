//go:build darwin

package tunnel

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoverRecordedTunnelAndRefuseReusedPID(t *testing.T) {
	previousGrace := startupGrace
	startupGrace = 50 * time.Millisecond
	defer func() { startupGrace = previousGrace }()
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover", true: "different_process"}[mismatch], func(t *testing.T) {
			dir := t.TempDir()
			bin := fakeTailscale(t, "{}", `trap 'exit 0' INT TERM; while :; do sleep 0.05; done`)
			f, err := startFunnel(context.Background(), bin, "8787", io.Discard, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(f.Stop)
			path := filepath.Join(dir, "funnel.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mismatch {
				var record funnelReceipt
				if err := json.Unmarshal(data, &record); err != nil {
					t.Fatal(err)
				}
				record.StartedMicros++
				data, _ = json.Marshal(record)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := recoverFunnel(dir); err != nil {
				t.Fatal(err)
			}
			if mismatch {
				select {
				case <-f.Done():
					t.Fatal("recovery killed a different process with the same PID")
				default:
				}
			} else {
				select {
				case <-f.Done():
				case <-time.After(3 * time.Second):
					t.Fatal("recorded tunnel survived recovery")
				}
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("stale receipt survived: %v", err)
			}
			f.Stop()
			// Missing/dead receipts must never prevent a later start.
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := recoverFunnel(dir); err != nil {
				t.Fatalf("dead tunnel prevented recovery: %v", err)
			}
		})
	}
}
