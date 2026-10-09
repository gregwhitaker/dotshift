// Command dotshift runs a local model router for coding agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/litellm"
	"github.com/gregwhitaker/dotshift/internal/router"
)

const usage = `dotshift: a local model router for coding agents

Usage:
  dotshift init     [-config FILE]   write a starter dotshift.yaml
  dotshift serve    [-config FILE]   start the router, job runner and MCP intake (Ctrl-C stops them)
  dotshift status   [-config FILE]   show whether the router is up and which models it serves
  dotshift stop     [-config FILE]   stop the router
  dotshift key      [-config FILE]   print the API key clients send to the router
  dotshift litellm-config [-config FILE]   print the generated LiteLLM config
  dotshift connect  [-revoke] [-new-passcode]
                                   show how to connect ChatGPT, and the approval passcode

Jobs (the router must be running):
  dotshift run [-repo NAME] [-model ALIAS] [-branch NAME] [-base REF] INSTRUCTIONS
                                   run a coding job in a new worktree and wait for it
  dotshift jobs [ID]               list jobs, or show one job's result
  dotshift logs ID                 print a job's agent output
  dotshift cancel ID               stop a running job
  dotshift clean ID                remove the worktree a failed or cancelled job left
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if c, ok := commands[cmd]; ok {
		os.Exit(c(args))
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	path := fs.String("config", config.DefaultPath, "path to dotshift.yaml")
	force := fs.Bool("force", false, "init: overwrite an existing config")
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "init":
		err = runInit(*path, *force)
	case "serve":
		err = runServe(ctx, *path)
	case "status":
		err = runStatus(ctx, *path)
	case "stop":
		err = runStop(ctx, *path)
	case "key":
		err = runKey(*path)
	case "litellm-config":
		err = runLiteLLMConfig(*path, os.Stdout)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runInit(path string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists (use -force to overwrite)", path)
	}
	if err := os.WriteFile(path, []byte(config.Sample), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s. Set your provider keys in the environment, then run `dotshift serve`.\n", path)
	return nil
}

// stateDir holds files dotshift generates, next to the config it came from.
func stateDir(configPath string) (string, error) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(abs), ".dotshift"), nil
}

// prepare loads the config and writes the generated LiteLLM config to disk.
func prepare(path string) (*config.Config, *router.Router, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	dir, err := stateDir(path)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	out, err := litellm.Marshal(litellm.Build(cfg))
	if err != nil {
		return nil, nil, err
	}
	generated := filepath.Join(dir, "litellm.yaml")
	if err := os.WriteFile(generated, out, 0o644); err != nil {
		return nil, nil, err
	}
	key, err := router.MasterKey(cfg, dir)
	if err != nil {
		return nil, nil, err
	}
	return cfg, router.New(cfg, generated, key), nil
}

func runServe(ctx context.Context, path string) error {
	cfg, r, err := prepare(path)
	if err != nil {
		return err
	}
	if missing := r.MissingEnv(); len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "warning: not set, models using them will fail: %s\n", strings.Join(missing, ", "))
	}
	fmt.Printf("Starting router (%s)...\n", cfg.Router.Image)
	if err := r.Start(ctx); err != nil {
		return err
	}
	// Use a fresh context for cleanup so Ctrl-C still removes the container.
	defer func() {
		fmt.Println("\nStopping router...")
		_ = r.Stop(context.Background())
	}()
	if err := r.WaitHealthy(ctx, 3*time.Minute); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return err
	}
	fmt.Printf("\nRouter is up at %s\nAliases: %s\n\n", r.BaseURL(), strings.Join(cfg.AliasNames(), ", "))
	stopIntake, err := startIntake(path, cfg, r)
	if err != nil {
		return err
	}
	defer stopIntake()
	fmt.Println()
	fmt.Print(codexSnippet(r.BaseURL(), cfg))
	fmt.Println("\nFollowing router logs. Press Ctrl-C to stop.")
	if err := r.Logs(ctx, os.Stdout); err != nil && ctx.Err() == nil {
		return fmt.Errorf("router stopped: %w", err)
	}
	return nil
}

func codexSnippet(baseURL string, cfg *config.Config) string {
	keyEnv := cfg.Router.MasterKeyEnv
	export := ""
	if keyEnv == "" {
		keyEnv = "DOTSHIFT_API_KEY"
		export = "export DOTSHIFT_API_KEY=$(dotshift key)\n"
	}
	return fmt.Sprintf(`To use it from Codex CLI, add to ~/.codex/config.toml:

[model_providers.dotshift]
name = "dotshift"
base_url = %q
env_key = %q
wire_api = "responses"

then run:
%scodex -c model_provider=dotshift -m smart
`, baseURL, keyEnv, export)
}

func runStatus(ctx context.Context, path string) error {
	_, r, err := prepare(path)
	if err != nil {
		return err
	}
	if !r.Running(ctx) {
		fmt.Println("Router is not running. Start it with `dotshift serve`.")
		return nil
	}
	if !r.Healthy(ctx) {
		fmt.Println("Router container is running but not answering yet.")
		return nil
	}
	models, err := r.Models(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Router is up at %s\nModels: %s\n", r.BaseURL(), strings.Join(models, ", "))
	return nil
}

func runStop(ctx context.Context, path string) error {
	_, r, err := prepare(path)
	if err != nil {
		return err
	}
	if err := r.Stop(ctx); err != nil {
		return err
	}
	fmt.Println("Router stopped.")
	return nil
}

func runLiteLLMConfig(path string, w io.Writer) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	out, err := litellm.Marshal(litellm.Build(cfg))
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func runKey(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	dir, err := stateDir(path)
	if err != nil {
		return err
	}
	key, err := router.MasterKey(cfg, dir)
	if err != nil {
		return err
	}
	fmt.Println(key)
	return nil
}
