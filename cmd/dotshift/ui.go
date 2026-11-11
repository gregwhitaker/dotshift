package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
)

func init() {
	var noOpen, localOnly bool
	var route string
	commands["ui"] = command("ui", func(fs *flag.FlagSet) {
		fs.BoolVar(&noOpen, "no-open", false, "start or reuse the dashboard and print its URL without opening a browser")
		fs.BoolVar(&localOnly, "local-only", false, "disable MCP and its tunnel for this run without changing the config")
		fs.StringVar(&route, "path", "/", "monitor path to open, such as /models")
	}, func(ctx context.Context, path string, fs *flag.FlagSet) error {
		if fs.NArg() != 0 {
			return errors.New("usage: dotshift ui [-config FILE] [-no-open] [-local-only] [-path PATH]")
		}
		if _, err := (dashboard.Record{}).BrowserRoute(route); err != nil {
			return err
		}
		record, err := ensureUI(ctx, path, localOnly)
		if err != nil {
			return err
		}
		browserURL, err := record.BrowserRoute(route)
		if err != nil {
			return err
		}
		fmt.Printf("Dashboard: %s\n", browserURL)
		if !record.MCPEnabled {
			fmt.Println("MCP and its tunnel are disabled for this running service.")
		}
		if !noOpen {
			if err := launchBrowser(browserURL); err != nil {
				fmt.Fprintf(os.Stderr, "Could not open the browser: %v. Open the URL above.\n", err)
			}
		}
		return nil
	})
}

func ensureUI(ctx context.Context, path string, localOnly bool) (*dashboard.Record, error) {
	path, err := canonicalConfig(path)
	if err != nil {
		return nil, fmt.Errorf("load config: %w (run `dotshift init` first)", err)
	}
	if _, err := config.Load(path); err != nil {
		return nil, err
	}
	dir, err := stateDir(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	discover := func() (*dashboard.Record, error) {
		record, err := dashboard.Discover(ctx, dir, path)
		if record != nil && localOnly && record.MCPEnabled {
			return nil, errors.New("the existing service has MCP enabled; stop it before starting with -local-only")
		}
		return record, err
	}
	if record, err := discover(); record != nil || err != nil {
		return record, err
	}
	managed, err := startInstalledService(ctx, path, localOnly)
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(dir, "service.log")
	var exited chan error
	if !managed {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(exe, "serve", "-config", path)
		if localOnly {
			cmd.Args = append(cmd.Args, "-local-only")
		}
		// Inherit the caller's working directory so relative repo paths retain
		// the same meaning as a foreground `dotshift serve` invocation.
		cmd.Stdout, cmd.Stderr = log, log
		detachService(cmd)
		err = cmd.Start()
		log.Close()
		if err != nil {
			return nil, err
		}
		exited = make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
	}
	fmt.Printf("Starting dotshift. Service log: %s\n", logPath)
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	var childErr error
	var exitedAt time.Time
	for {
		if record, err := discover(); record != nil || err != nil {
			return record, err
		}
		// A concurrent launcher can lose the lock while the winning process
		// is still starting. Only fail if no process owns that lock anymore.
		if !exitedAt.IsZero() && time.Since(exitedAt) > time.Second {
			if unlock, err := lockService(dir); err == nil {
				unlock()
				return nil, fmt.Errorf("dotshift could not start (%v); see %s", childErr, logPath)
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for dashboard: %w; see %s", ctx.Err(), logPath)
		case childErr = <-exited:
			exitedAt = time.Now()
		case <-ticker.C:
		}
	}
}

func launchBrowser(url string) error {
	var command string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		return errors.New("automatic browser opening is unsupported on this platform")
	}
	cmd := exec.Command(command, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func stopService(ctx context.Context, path string) (bool, error) {
	path, err := canonicalConfig(path)
	if err != nil {
		return false, err
	}
	dir, err := stateDir(path)
	if err != nil {
		return false, err
	}
	record, err := dashboard.Discover(ctx, dir, path)
	if err != nil || record == nil {
		return false, err
	}
	res, err := record.Request(ctx, http.MethodPost, "/api/shutdown")
	if err != nil {
		return true, err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		return true, fmt.Errorf("service refused shutdown: HTTP %d", res.StatusCode)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if unlock, err := lockService(dir); err == nil {
			unlock()
			fmt.Println("Dotshift stopped. Active goals are saved for the next start.")
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, fmt.Errorf("waiting for service shutdown: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
