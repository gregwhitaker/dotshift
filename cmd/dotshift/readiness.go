package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
	"github.com/gregwhitaker/dotshift/internal/jobs"
	"github.com/gregwhitaker/dotshift/internal/router"
)

// One cache serves the browser and menu. Slow checks run outside the mutex so
// routine status polling never waits for a CLI or network probe.
type readinessMonitor struct {
	mu       sync.Mutex
	cached   *dashboard.Readiness
	inflight chan struct{}
	lastSeen time.Time
	now      func() time.Time
	probe    func(context.Context) []dashboard.ReadinessCheck
	setup    string
}

func newReadinessMonitor(path string, cfg *config.Config, runner *jobs.Runner, proxy *router.Router) *readinessMonitor {
	m := &readinessMonitor{now: time.Now, setup: "dotshift connect -config '" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"}
	m.probe = func(ctx context.Context) []dashboard.ReadinessCheck {
		checks := make([]dashboard.ReadinessCheck, 3)
		var wg sync.WaitGroup
		for i, check := range []func(context.Context) dashboard.ReadinessCheck{
			func(ctx context.Context) dashboard.ReadinessCheck { return checkWorkspace(ctx, runner) },
			func(ctx context.Context) dashboard.ReadinessCheck { return checkCoding(ctx, cfg, runner, proxy) },
			func(ctx context.Context) dashboard.ReadinessCheck { return checkDotConnection(ctx, cfg) },
		} {
			wg.Add(1)
			go func() { defer wg.Done(); checks[i] = check(ctx) }()
		}
		wg.Wait()
		return checks
	}
	return m
}

func (m *readinessMonitor) contact() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSeen = m.now().UTC()
}

func (m *readinessMonitor) Current() *dashboard.Readiness {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentLocked()
}

func (m *readinessMonitor) currentLocked() *dashboard.Readiness {
	if m.cached == nil {
		return nil
	}
	out := *m.cached
	out.Checks = append([]dashboard.ReadinessCheck(nil), out.Checks...)
	out.SetupCommand = m.setup
	if !m.lastSeen.IsZero() {
		seen := m.lastSeen
		out.LastContact = &seen
	}
	// A reachable public endpoint is not evidence that Dot is connected.
	for i := range out.Checks {
		check := &out.Checks[i]
		if check.Name == "Dot connection" && check.State != "attention" {
			if m.lastSeen.IsZero() || m.now().Sub(m.lastSeen) > 24*time.Hour {
				check.State, check.Label = "unchecked", "Waiting for Dot"
				check.Detail = "Ask Dot to check its dotshift connection. A successful sign-in request confirms the connection; a local health check alone cannot."
			} else {
				check.State, check.Label = "ready", "Contact confirmed"
				check.Detail = "An authenticated request reached this service during this session. The last contact time is shown below."
			}
		}
	}
	out.State, out.Label = "ready", "Ready for Dot"
	out.Detail = "Local checks passed and Dot has reached this service. Tell Dot what to build, test, and apply locally."
	out.Message = ""
	for _, state := range []string{"attention", "unchecked"} {
		for _, check := range out.Checks {
			if check.State == state {
				out.State, out.Label, out.Detail = state, check.Label, check.Detail
				out.Message = "Check my dotshift connection and local coding setup. List the available repositories, inspect any access problem, and handle routine recovery. Preserve my saved work and existing limits. Ask me only for a sign-in or permission that you cannot complete."
				break
			}
		}
		if out.State != "ready" {
			break
		}
	}
	if m.now().Sub(out.CheckedAt) > 90*time.Second {
		out.State, out.Label, out.Detail = "unchecked", "Connection check needed", "The previous checks are out of date. Check again for the current connection status."
	}
	return &out
}

func (m *readinessMonitor) Check(ctx context.Context, force bool) (*dashboard.Readiness, error) {
	m.mu.Lock()
	if m.inflight != nil {
		pending := m.inflight
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
			return m.Check(ctx, false)
		}
	}
	if m.cached != nil && m.now().Sub(m.cached.CheckedAt) < time.Minute && (!force || m.now().Sub(m.cached.CheckedAt) < 3*time.Second) {
		out := m.currentLocked()
		m.mu.Unlock()
		return out, nil
	}
	m.inflight = make(chan struct{})
	m.mu.Unlock()
	checkCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	checks := m.probe(checkCtx)
	err := checkCtx.Err()
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.cached = &dashboard.Readiness{CheckedAt: m.now().UTC(), Checks: checks}
	}
	close(m.inflight)
	m.inflight = nil
	return m.currentLocked(), err
}

func checkWorkspace(ctx context.Context, runner *jobs.Runner) dashboard.ReadinessCheck {
	check := dashboard.ReadinessCheck{Name: "Local workspace", State: "attention", Label: "Workspace access needed", Detail: "Ask Dot to check the authorized workspace. Your saved work is unchanged."}
	repos, err := runner.Repos(ctx)
	if err != nil {
		return check
	}
	usable := 0
	for _, repo := range repos {
		if info, err := os.Stat(repo.Path); err == nil && info.IsDir() && repo.HasTests {
			usable++
		}
	}
	options := runner.ProjectOptions()
	if options.Enabled {
		if _, err := os.ReadDir(options.Root); err != nil {
			return check
		}
		check.State, check.Label = "ready", "Workspace available"
		check.Detail = fmt.Sprintf("%s · %d repositories available. Dot can build new apps here and handle their tests.", options.Root, usable)
	} else if usable > 0 {
		check.State, check.Label, check.Detail = "ready", "Repositories available", fmt.Sprintf("Dot can work in %d authorized repositories using their configured checks.", usable)
	} else {
		check.Detail = "Choose a local workspace once during setup. Dot will handle the repositories and tests inside it."
	}
	return check
}

func checkDotConnection(ctx context.Context, cfg *config.Config) dashboard.ReadinessCheck {
	check := dashboard.ReadinessCheck{Name: "Dot connection", State: "unchecked", Label: "Waiting for Dot"}
	if cfg.MCP.Disabled {
		check.State, check.Label, check.Detail = "attention", "Connect Dot", "Dot access is off for this service. Ask Dot to help enable the existing integration, or open connection instructions below. Saved local work is still available."
		return check
	}
	publicURL := cfg.MCP.PublicURL
	if publicURL == "" && cfg.MCP.Tunnel == config.TunnelTailscale {
		_, detected, err := findFunnel(ctx)
		if err != nil {
			check.State, check.Label, check.Detail = "attention", "Dot connection unavailable", "The connection to this computer could not be checked. Dotshift retries its Tailscale connection automatically. Check again after restoring access."
			return check
		}
		publicURL = detected
	}
	if publicURL != "" {
		if err := desktopRemoteHealth(ctx, publicURL); err != nil {
			check.State, check.Label, check.Detail = "attention", "Dot connection unavailable", "This computer's connection endpoint is not responding. Check again after restoring access; saved local work remains available."
		}
	}
	return check
}

func checkCoding(ctx context.Context, cfg *config.Config, runner *jobs.Runner, proxy *router.Router) dashboard.ReadinessCheck {
	check := dashboard.ReadinessCheck{Name: "Coding access", State: "attention", Label: "Coding access needed", Detail: "Ask Dot to check coding access. Work and limits are preserved."}
	catalog, err := runner.Strategies(ctx)
	if err != nil {
		return check
	}
	workers, err := cfg.StrategyModels(cfg.Jobs.Model, cfg.Jobs.Strategy)
	if err != nil {
		return check
	}
	available := map[string]bool{}
	for _, strategy := range catalog.Strategies {
		for _, alias := range strategy.Aliases {
			for _, route := range alias.Models {
				available[route.Route+":"+route.Model] = route.Available
			}
		}
	}
	signedIn := map[string]bool{}
	if cfg.UsesSubscriptions() {
		providers := map[string]bool{}
		for _, worker := range workers {
			providers[worker.Provider] = true
			if cfg.Jobs.ReviewerModel != "" {
				reviewers, _ := cfg.ReviewerRoutes(cfg.Jobs.Model, cfg.Jobs.ReviewerModel, worker.Model)
				for _, review := range reviewers {
					providers[review.Provider] = true
				}
			}
		}
		subscriptions := &agent.Subscriptions{CodexPath: cfg.Jobs.Agent.Command, ClaudePath: cfg.Jobs.ClaudeCommand}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var issues []string
		for provider := range providers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := subscriptions.Check(ctx, provider)
				mu.Lock()
				defer mu.Unlock()
				signedIn[provider] = err == nil
				if err != nil {
					issues = append(issues, err.Error())
				}
			}()
		}
		wg.Wait()
		if len(issues) > 0 {
			sort.Strings(issues)
			check.Detail = strings.Join(issues, ". ")
		}
	} else {
		if proxy == nil || !proxy.Healthy(ctx) {
			check.Detail = "The configured API connection needs attention. Ask Dot to check the local router and required credentials."
			return check
		}
	}
	usable := func(route config.ModelRoute) bool {
		if !available[route.Route+":"+route.Model] {
			return false
		}
		if cfg.UsesSubscriptions() {
			return signedIn[route.Provider]
		}
		env := cfg.Providers[route.Provider].APIKeyEnv
		return env == "" || os.Getenv(env) != ""
	}
	for _, worker := range workers {
		if !usable(worker) {
			continue
		}
		reviewOK := cfg.Jobs.ReviewerModel == ""
		if !reviewOK {
			reviewers, _ := cfg.ReviewerRoutes(cfg.Jobs.Model, cfg.Jobs.ReviewerModel, worker.Model)
			for _, review := range reviewers {
				if usable(review) {
					reviewOK = true
				}
			}
		}
		if reviewOK {
			check.State, check.Label = "ready", "Coding and review available"
			check.Detail = "The configured coding and review access checks passed. Dot handles model choice and fallback. No coding request was made; remaining usage allowance is not measured."
			return check
		}
	}
	if check.Detail == "Ask Dot to check coding access. Work and limits are preserved." {
		check.Label, check.Detail = "Coding access is waiting", "The configured coding or review options have a recorded access limit. Dot handles supported retries and fallback; ask Dot to inspect any work that remains stopped."
	}
	return check
}
