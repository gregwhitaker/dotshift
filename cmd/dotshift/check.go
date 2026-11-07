package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/check"
)

// runCheck tests chat and the coding agent's Responses protocol on every model.
func runCheck(ctx context.Context, path string, aliases []string) error {
	cfg, r, err := prepare(path)
	if err != nil {
		return err
	}
	if cfg.UsesSubscriptions() {
		if len(aliases) == 0 {
			aliases = cfg.AliasNames()
		}
		providers := map[string]bool{}
		for _, alias := range aliases {
			routes, err := cfg.StrategyModels(alias, "alias")
			if err != nil {
				return err
			}
			for _, route := range routes {
				providers[route.Provider] = true
			}
		}
		a := &agent.Subscriptions{CodexPath: cfg.Jobs.Agent.Command, ClaudePath: cfg.Jobs.ClaudeCommand}
		failed := 0
		for _, provider := range []string{"openai", "anthropic"} {
			if !providers[provider] {
				continue
			}
			if err := a.Check(ctx, provider); err != nil {
				fmt.Printf("%s: %v\n", provider, err)
				failed++
			} else {
				fmt.Printf("%s: CLI available; subscription sign-in confirmed\n", provider)
			}
		}
		fmt.Println("This check makes no model requests; it does not measure remaining allowance or test model access.")
		if failed > 0 {
			return fmt.Errorf("%d subscription CLI(s) need setup", failed)
		}
		return nil
	}
	targets, err := check.Targets(cfg, aliases)
	if err != nil {
		return err
	}
	if !r.Healthy(ctx) {
		return fmt.Errorf("router is not answering at %s; start it with `dotshift serve`", r.BaseURL())
	}
	fmt.Printf("Checking %d model(s) through %s...\n\n", len(targets), r.BaseURL())
	client := &check.Client{BaseURL: r.BaseURL(), Key: r.APIKey(), HTTP: &http.Client{Timeout: 90 * time.Second}}
	results := client.Run(ctx, targets)
	failed := printCheck(os.Stdout, results)
	if failed > 0 {
		return fmt.Errorf("%d of %d models failed", failed, len(results))
	}
	fmt.Println("\nAll models passed.")
	return nil
}

// printCheck writes one row per model, then the errors, and returns how many
// models failed.
func printCheck(w io.Writer, results []check.Result) int {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tMODEL\tROLE\tCHAT\tTOOLS\tSTREAM\tTOOL LOOP\tGOAL JSON")
	failed := 0
	for _, res := range results {
		role := "primary"
		if res.Position > 0 {
			role = fmt.Sprintf("fallback %d", res.Position)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", res.Alias, res.Model, role,
			outcome(res.Chat), outcome(res.Tools), outcome(res.Responses), outcome(res.ToolLoop), outcome(res.Structured))
		if !res.OK() {
			failed++
		}
	}
	tw.Flush()
	if failed > 0 {
		fmt.Fprintln(w, "\nFailures:")
		for _, res := range results {
			if res.Chat.Err != nil {
				fmt.Fprintf(w, "  %s chat: %v\n", res.Model, res.Chat.Err)
			}
			if res.Tools.Err != nil {
				fmt.Fprintf(w, "  %s tools: %v\n", res.Model, res.Tools.Err)
			}
			if res.Responses.Err != nil {
				fmt.Fprintf(w, "  %s Responses stream: %v\n", res.Model, res.Responses.Err)
			}
			if res.ToolLoop.Err != nil {
				fmt.Fprintf(w, "  %s Responses tool loop: %v\n", res.Model, res.ToolLoop.Err)
			}
			if res.Structured.Err != nil {
				fmt.Fprintf(w, "  %s goal JSON: %v\n", res.Model, res.Structured.Err)
			}
		}
	}
	return failed
}

func outcome(o check.Outcome) string {
	if !o.OK() {
		return "FAIL"
	}
	return fmt.Sprintf("ok %.1fs", o.Latency.Seconds())
}
