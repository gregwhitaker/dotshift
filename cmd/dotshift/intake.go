package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/mcpintake"
	"github.com/gregwhitaker/dotshift/internal/router"
)

// authDB is the OAuth database, inside the state directory.
const authDB = "auth.db"

// startIntake runs the job runner and the MCP intake inside `dotshift serve`.
// The returned function stops taking requests, cancels jobs still running
// and closes the stores.
func startIntake(configPath string, cfg *config.Config, r *router.Router) (func(), error) {
	if cfg.MCP.Disabled {
		fmt.Println("MCP intake is off (mcp.disabled in the config).")
		return func() {}, nil
	}
	dir, err := stateDir(configPath)
	if err != nil {
		return nil, err
	}
	passcode, err := mcpintake.Passcode(cfg, dir)
	if err != nil {
		return nil, err
	}
	runner, closeJobs, err := newRunner(cfg, r, dir)
	if err != nil {
		return nil, err
	}
	authStore, err := mcpintake.OpenAuthStore(filepath.Join(dir, authDB))
	if err != nil {
		closeJobs()
		return nil, err
	}
	closeAll := func() { authStore.Close(); closeJobs() }
	handler, err := mcpintake.NewHandler(mcpintake.Options{
		Jobs: runner, DefaultModel: cfg.Jobs.Model, Models: cfg.AliasNames(),
		Auth: authStore, Passcode: passcode, PublicURL: cfg.MCP.PublicURL, Logf: logLine,
	})
	if err != nil {
		closeAll()
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.MCP.Listen)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("MCP intake: %w (change mcp.listen, or set mcp.disabled)", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logLine("MCP intake stopped: %v", err)
		}
	}()

	fmt.Printf("MCP intake listening on http://%s (endpoint /mcp)\n", ln.Addr())
	if cfg.MCP.PublicURL != "" {
		fmt.Printf("Clients connect to %s/mcp\n", cfg.MCP.PublicURL)
	}
	fmt.Println("Run `dotshift connect` for the tunnel and ChatGPT steps and the passcode.")

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		runner.Shutdown(ctx, "dotshift serve stopped")
		closeAll()
	}, nil
}

func logLine(format string, args ...any) {
	fmt.Fprintf(os.Stdout, "%s dotshift: %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
