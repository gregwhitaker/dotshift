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
	"github.com/gregwhitaker/dotshift/internal/jobs"
	"github.com/gregwhitaker/dotshift/internal/mcpintake"
	"github.com/gregwhitaker/dotshift/internal/tunnel"
)

// authDB is the OAuth database, inside the state directory.
const authDB = "auth.db"

// startIntake attaches MCP to the shared runner. Its lifetime is independent
// of the dashboard, and disabling MCP does not disable goal recovery.
func startIntake(cfg *config.Config, runner *jobs.Runner, dir string) (func(), error) {
	if cfg.MCP.Disabled {
		fmt.Println("MCP intake is off (mcp.disabled in the config).")
		return func() {}, nil
	}
	passcode, err := mcpintake.Passcode(cfg, dir)
	if err != nil {
		return nil, err
	}
	authStore, err := mcpintake.OpenAuthStore(filepath.Join(dir, authDB))
	if err != nil {
		return nil, err
	}
	closeAll := func() { authStore.Close() }

	publicURL := cfg.MCP.PublicURL
	if cfg.MCP.Tunnel == config.TunnelTailscale {
		_, url, err := findFunnel(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: Tailscale status: %v\n  Tunnel setup will retry automatically.\n", err)
		} else {
			if publicURL == "" {
				publicURL = url
			}
		}
	}
	handler, err := mcpintake.NewHandler(mcpintake.Options{
		Jobs: runner, DefaultModel: cfg.Jobs.Model, Models: cfg.AliasNames(),
		Auth: authStore, Passcode: passcode, PublicURL: publicURL, Logf: logLine,
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
	stopFunnel := func() {}
	if cfg.MCP.Tunnel == config.TunnelTailscale {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		go func() {
			defer close(done)
			tunnel.SuperviseFunnel(ctx, port, os.Stdout, logLine, dir)
		}()
		stopFunnel = func() { cancel(); <-done }
	} else if publicURL != "" && cfg.MCP.Tunnel == config.TunnelNone {
		fmt.Printf("Clients connect to %s/mcp\n", publicURL)
	}
	fmt.Println("Run `dotshift connect` for the ChatGPT steps and the passcode.")

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		stopFunnel()
		_ = srv.Shutdown(ctx)
		closeAll()
	}, nil
}

func logLine(format string, args ...any) {
	fmt.Fprintf(os.Stdout, "%s dotshift: %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// findFunnel finds the tailscale CLI and the URL Funnel will serve at.
func findFunnel(ctx context.Context) (bin, url string, err error) {
	if bin, err = tunnel.FindTailscale(); err != nil {
		return "", "", err
	}
	url, err = tunnel.PublicURL(ctx, bin)
	return bin, url, err
}
