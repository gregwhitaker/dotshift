//go:build darwin

package main

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentEscapesPathsAndRestartsOnlyFailures(t *testing.T) {
	a := &launchAgent{
		config: `/tmp/a & b/"config".yaml`, executable: "/tmp/a & b/dotshift",
		directory: "/tmp/a & b", state: "/tmp/a & b/.dotshift", label: "com.dotshift.test",
		pathEnv: "/usr/bin:/a & b/bin",
	}
	data := string(a.plist())
	d := xml.NewDecoder(strings.NewReader(data))
	for {
		if _, err := d.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"<key>RunAtLoad</key><true/>", "<key>SuccessfulExit</key><false/>", "<key>ThrottleInterval</key><integer>10</integer>", "a &amp; b", "<string>serve</string>", "<key>PATH</key>"} {
		if !strings.Contains(data, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	if strings.Contains(data, "API_KEY") || strings.Contains(data, "<key>KeepAlive</key><true/>") {
		t.Fatal("unsafe persistent environment or unconditional restart")
	}
}

func TestLaunchAgentStartReuseStopAndRestart(t *testing.T) {
	state := t.TempDir()
	a := &launchAgent{state: state, label: "com.dotshift.test", domain: "gui/501", file: filepath.Join(state, "service.plist")}
	if err := os.WriteFile(a.file, a.plist(), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := false
	var calls []string
	a.run = func(ctx context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "print":
			if !loaded {
				return errors.New("not loaded")
			}
		case "bootstrap":
			loaded = true
		case "bootout":
			loaded = false
		}
		return ctx.Err()
	}
	ctx := context.Background()
	if ok, err := a.installed(); err != nil || !ok {
		t.Fatalf("installed: %v, %v", ok, err)
	}
	if err := a.start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.Join(calls, "\n"), "bootstrap"); got != 1 {
		t.Fatalf("reusing service bootstrapped %d times", got)
	}
	if err := a.unload(ctx); err != nil {
		t.Fatal(err)
	}
	if loaded {
		t.Fatal("intentional stop left service loaded")
	}
	if err := a.start(ctx); err != nil {
		t.Fatal(err)
	}
	if !loaded || strings.Count(strings.Join(calls, "\n"), "bootstrap") != 2 {
		t.Fatalf("restart did not restore service: %v", calls)
	}
	if strings.Contains(strings.Join(calls, "\n"), "kickstart -k") {
		t.Fatal("opening UI must not restart a healthy service")
	}
}
