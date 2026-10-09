package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

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
			printConnect(cfg, passcode)
			registered, connected, err := store.Clients(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("\nConnected clients: %d (registered: %d). `dotshift connect -revoke` disconnects them all.\n", connected, registered)
			return nil
		})
}

func printConnect(cfg *config.Config, passcode string) {
	if cfg.MCP.Disabled {
		fmt.Println("Note: the MCP intake is off (mcp.disabled), so `dotshift serve` won't start it.")
	}
	_, port, _ := net.SplitHostPort(cfg.MCP.Listen)
	local := "http://" + cfg.MCP.Listen
	mcpURL := "https://<your tunnel host>/mcp"
	if cfg.MCP.PublicURL != "" {
		mcpURL = cfg.MCP.PublicURL + "/mcp"
	}
	fmt.Printf(`dotshift's MCP intake runs inside `+"`dotshift serve`"+` at %s.

1. Give it a public https URL with a tunnel (pick one):
     cloudflared tunnel --url %s
     tailscale funnel %s
     ngrok http %s
   Only this endpoint goes through the tunnel; the router stays on this machine.
   Check it with: curl https://<your tunnel host>/healthz

2. In ChatGPT: Settings > Apps & Connectors > Advanced settings, turn on
   Developer mode. Then Settings > Apps & Connectors > Create:
     MCP server URL:  %s
     Authentication:  OAuth

3. ChatGPT opens dotshift's approval page. Enter this passcode to allow it:

     %s
`, local, local, port, port, mcpURL, passcode)
}
