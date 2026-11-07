package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/jobs"
	"github.com/gregwhitaker/dotshift/internal/router"
)

// commands are subcommands that parse their own flags. Each registers itself
// from its own file, so main's switch stays as it is. A command returns the
// process exit code.
var commands = map[string]func(args []string) int{}

// command wraps a subcommand body with the -config flag, signal handling and
// error printing shared by every command.
func command(name string, setup func(fs *flag.FlagSet), body func(ctx context.Context, configPath string, fs *flag.FlagSet) error) func([]string) int {
	return func(args []string) int {
		fs := flag.NewFlagSet(name, flag.ExitOnError)
		path := fs.String("config", config.DefaultPath, "path to dotshift.yaml")
		if setup != nil {
			setup(fs)
		}
		parseInterspersed(fs, args)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := body(ctx, *path, fs); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	}
}

// parseInterspersed parses flags wherever they appear, so both
// `dotshift run -model fast "fix it"` and `dotshift run "fix it" -model fast`
// work. Everything after "--" is positional.
func parseInterspersed(fs *flag.FlagSet, args []string) {
	var positional []string
	for len(args) > 0 {
		_ = fs.Parse(args) // stops at the first positional argument or after "--"
		rest := fs.Args()
		if used := len(args) - len(rest); used > 0 && args[used-1] == "--" {
			positional = append(positional, rest...)
			break
		}
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	_ = fs.Parse(append([]string{"--"}, positional...))
}

// openRunner loads the config and opens the job store. The runner it returns
// can read, cancel and clean jobs; runJob adds what's needed to start them.
func openRunner(configPath string) (*jobs.Runner, *router.Router, func(), error) {
	cfg, r, err := prepare(configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	dir, err := stateDir(configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	runner, closeStore, err := newRunner(cfg, r, dir)
	if err != nil {
		return nil, nil, nil, err
	}
	return runner, r, closeStore, nil
}

// newRunner opens the job store in dir and returns a runner whose agents call r.
func newRunner(cfg *config.Config, r *router.Router, dir string) (*jobs.Runner, func(), error) {
	store, err := jobs.OpenStore(filepath.Join(dir, "jobs.db"))
	if err != nil {
		return nil, nil, err
	}
	var a agent.Agent
	if cfg.UsesSubscriptions() {
		a = &agent.Subscriptions{CodexPath: cfg.Jobs.Agent.Command, ClaudePath: cfg.Jobs.ClaudeCommand}
	} else {
		a, err = agent.New(cfg.Jobs.Agent)
	}
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	var key, baseURL string
	if !cfg.UsesSubscriptions() {
		key, err = router.MasterKey(cfg, dir)
		if err != nil {
			store.Close()
			return nil, nil, err
		}
		baseURL = r.BaseURL()
	}
	runner := &jobs.Runner{Config: cfg, Store: store, Agent: a, BaseURL: baseURL, APIKey: key, StateDir: dir}
	return runner, func() { store.Close() }, nil
}
