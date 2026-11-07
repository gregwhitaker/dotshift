package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/mcpintake"
)

// `dotshift connect` shows how to reach the MCP intake from ChatGPT and the
// passcode that approves a client. -revoke disconnects every client and
// -new-passcode replaces the generated passcode.
func init() {
	var revoke, newPasscode *bool
	commands["connect"] = command("connect",
		func(fs *flag.FlagSet) {
			revoke = fs.Bool("revoke", false, "disconnect every client; each must be approved again")
			newPasscode = fs.Bool("new-passcode", false, "replace the generated passcode (clients already connected stay connected)")
		},
		func(ctx context.Context, configPath string, fs *flag.FlagSet) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			dir, err := stateDir(configPath)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			store, err := mcpintake.OpenAuthStore(filepath.Join(dir, authDB))
			if err != nil {
				return err
			}
			defer store.Close()

			if *revoke {
				n, err := store.RevokeAll(ctx)
				if err != nil {
					return err
				}
				fmt.Printf("Disconnected %d client(s). Each must be added and approved again.\n", n)
				return nil
			}
			if *newPasscode {
				if cfg.MCP.PasscodeEnv != "" {
					return fmt.Errorf("the passcode comes from $%s (mcp.passcode_env); change it there", cfg.MCP.PasscodeEnv)
				}
				if err := os.Remove(filepath.Join(dir, mcpintake.PasscodeFile)); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			passcode, err := mcpintake.Passcode(cfg, dir)
			if err != nil {
				return err
			}
			printConnect(ctx, cfg, passcode)
			registered, connected, err := store.Clients(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("\nConnected clients: %d (registered: %d). `dotshift connect -revoke` disconnects them all.\n", connected, registered)
			return nil
		})
}

func printConnect(ctx context.Context, cfg *config.Config, passcode string) {
	if cfg.MCP.Disabled {
		fmt.Println("Note: the MCP intake is off (mcp.disabled), so `dotshift serve` won't start it.")
	}
	_, port, _ := net.SplitHostPort(cfg.MCP.Listen)
	mcpURL := "https://<your tunnel host>/mcp"
	if cfg.MCP.PublicURL != "" {
		mcpURL = cfg.MCP.PublicURL + "/mcp"
	}

	fmt.Printf("dotshift's MCP intake runs inside `dotshift serve` at http://%s.\n\n", cfg.MCP.Listen)
	if cfg.MCP.Tunnel == config.TunnelTailscale {
		_, url, err := findFunnel(ctx)
		if cfg.MCP.PublicURL == "" {
			mcpURL = "https://<machine>.<tailnet>.ts.net/mcp"
			if err == nil {
				mcpURL = url + "/mcp"
			}
		}
		fmt.Printf(`1. %s starts Tailscale Funnel for you, which serves it at
   this machine's https://<machine>.<tailnet>.ts.net name. Only this endpoint
   goes on the internet; the router stays on this machine. Once per machine:
     - Install Tailscale and log in (tailscale up).
     - Allow Funnel: run "tailscale funnel %s" once. If Funnel isn't on for
       your tailnet yet, it prints a link to turn it on; then press Ctrl-C.
`, "`dotshift serve`", port)
		if err != nil {
			fmt.Printf("   Not ready yet: %v\n", err)
		} else {
			fmt.Printf("   Tailscale is up. Check it while serve runs: curl %s/healthz\n", strings.TrimSuffix(mcpURL, "/mcp"))
		}
	} else {
		fmt.Printf(`1. Point your tunnel at http://%s (mcp.tunnel is none). Only this
   endpoint should go through it; the router stays on this machine.
   Check it with: curl https://<your tunnel host>/healthz
`, cfg.MCP.Listen)
	}
	fmt.Printf(`
2. Open https://chatgpt.com/plugins in your browser.
   Use + > Add custom MCP server, then enter:
     Name:            dotshift
     Connection:      Server URL
     Server URL:      %s
     Authentication:  OAuth
   If asked for OAuth client registration, choose dynamic registration (DCR).
   dotshift registers the client automatically; no static credentials are needed.
   Review the warning, confirm it, and choose Create as a plugin.

3. Install dotshift from your personal plugins. When its authorization page
   opens, enter this passcode to approve the connection:

     %s

4. Start a Work chat, type @, and select dotshift. Ask it to list your repos
   and model strategies to verify the connection before starting a goal.

Current setup guide:
https://developers.openai.com/api/docs/guides/custom-mcp-server
`, mcpURL, passcode)
}
