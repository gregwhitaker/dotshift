package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
	"github.com/gregwhitaker/dotshift/internal/router"
)

func canonicalConfig(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// startRuntime gives both intakes one runner and one recovery/shutdown path.
func startRuntime(path string, cfg *config.Config, router *router.Router, stop context.CancelFunc) (func(), error) {
	dir, err := stateDir(path)
	if err != nil {
		return nil, err
	}
	runner, closeStore, err := newRunner(cfg, router, dir)
	if err != nil {
		return nil, err
	}
	stopMCP, err := startIntake(cfg, runner, dir)
	if err != nil {
		closeStore()
		return nil, err
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// Reject new submissions and save active goals before waiting for the
		// remote intake or its tunnel to drain.
		runner.Shutdown(ctx, "dotshift service stopped")
		stopMCP()
		closeStore()
	}
	if err := runner.ResumeGoals(context.Background()); err != nil {
		cleanup()
		return nil, fmt.Errorf("recovering goals: %w", err)
	}
	ui, err := dashboard.Start(dashboard.Options{Jobs: runner, ConfigPath: path, ExecutionMode: cfg.ExecutionMode(), DefaultModel: cfg.Jobs.Model, MCPEnabled: !cfg.MCP.Disabled, Stop: stop})
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := ui.Record.Save(dir); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ui.Close(ctx)
		cleanup()
		return nil, err
	}
	fmt.Printf("Dashboard listening on %s. Open it with `dotshift ui -config %s`.\n", ui.Record.URL, path)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = ui.Close(ctx)
		cleanup()
		_ = os.Remove(filepath.Join(dir, "service.json"))
	}, nil
}
