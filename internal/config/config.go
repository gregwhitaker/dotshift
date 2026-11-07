// Package config loads and validates dotshift.yaml.
package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where dotshift looks for its config when none is given.
const DefaultPath = "dotshift.yaml"

// Config is the top-level dotshift.yaml document.
type Config struct {
	Router    Router              `yaml:"router"`
	Providers map[string]Provider `yaml:"providers"`
	// Aliases maps a name a coding agent asks for (e.g. "smart") to an ordered
	// list of models. The first model serves the request; the rest are
	// fallbacks tried in order when it fails.
	Aliases map[string][]string `yaml:"aliases"`
	// Repos is the allowlist of repositories jobs may run in, keyed by a short
	// name intakes use to refer to them.
	Repos map[string]Repo `yaml:"repos"`
	// Projects enables MCP project creation and registration inside one root.
	Projects Projects `yaml:"projects"`
	Jobs     Jobs     `yaml:"jobs"`
	MCP      MCP      `yaml:"mcp"`
}

// MCP controls the MCP intake `dotshift serve` runs: the endpoint your dot
// (or any MCP client) calls through a tunnel to start and follow jobs.
type MCP struct {
	// Disabled turns the MCP intake off.
	Disabled bool `yaml:"disabled"`
	// Listen is the local address the MCP endpoint serves on. Point your
	// tunnel here.
	Listen string `yaml:"listen"`
	// Tunnel is how ChatGPT reaches the endpoint. "tailscale" (the default)
	// has `dotshift serve` run Tailscale Funnel, which serves it at this
	// machine's https://<machine>.<tailnet>.ts.net name. "none" leaves the
	// tunnel to you.
	Tunnel string `yaml:"tunnel"`
	// PublicURL is the https URL clients reach the endpoint at. Requests for
	// any other host are refused. With the Tailscale tunnel it defaults to the
	// Funnel URL; with none and no public_url, dotshift takes the URL from
	// each request, as the tunnel forwarded it.
	PublicURL string `yaml:"public_url"`
	// PasscodeEnv names an environment variable holding the passcode you type
	// to approve a client. Empty means dotshift generates one and stores it
	// in .dotshift/mcp_passcode next to this config.
	PasscodeEnv string `yaml:"passcode_env"`
}

// Repo is one repository jobs are allowed to work in.
type Repo struct {
	// Path is the repository's working copy. A leading ~ is the home folder.
	Path string `yaml:"path"`
	// Test is an optional shell command run in the job's worktree after the
	// agent finishes, e.g. "go test ./...". Its result is recorded on the job.
	Test string `yaml:"test"`
}

// Projects is the local policy for repositories managed through MCP. Clients
// choose a test profile, not an arbitrary host shell command.
type Projects struct {
	Root         string            `yaml:"root"`
	TestProfiles map[string]string `yaml:"test_profiles"`
}

// Jobs controls how coding jobs run.
type Jobs struct {
	// Mode chooses API routing or the official CLIs with subscription sign-in.
	Mode string `yaml:"mode"`
	// ClaudeCommand is the Claude Code executable used in subscription mode.
	ClaudeCommand string `yaml:"claude_command"`
	// Model is the alias jobs use when the request doesn't name one.
	Model string `yaml:"model"`
	// Strategy is the default model selection policy for project goals.
	Strategy string `yaml:"strategy"`
	// ReviewerModel is an alias, or "auto" to choose a different model from
	// the coding alias. Empty preserves review with the coding model.
	ReviewerModel string `yaml:"reviewer_model"`
	// EscalateAfter is the number of stalled cycles before adaptive escalates.
	EscalateAfter int `yaml:"escalate_after"`
	// MaxCostUSD is a reported-spend cap for each new project goal. Zero disables it.
	MaxCostUSD float64 `yaml:"max_cost_usd"`
	// Timeout stops a job's agent after this long, e.g. "45m".
	Timeout Duration `yaml:"timeout"`
	// WorktreeDir is where job worktrees are created. Defaults to
	// .dotshift/worktrees next to the config file.
	WorktreeDir string `yaml:"worktree_dir"`
	Agent       Agent  `yaml:"agent"`
}

// Agent selects the coding agent jobs run.
type Agent struct {
	// Name is the agent type. Only "codex" exists today.
	Name string `yaml:"name"`
	// Command is the agent binary, if not the default on PATH.
	Command string `yaml:"command"`
	// Args are extra arguments passed to the agent on every job.
	Args []string `yaml:"args"`
}

// Duration is a time.Duration written in YAML as "30m" or "1h".
type Duration struct{ time.Duration }

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration like 30m or 1h", n.Line, n.Value)
	}
	d.Duration = v
	return nil
}

// Values of mcp.tunnel.
const (
	ModeAPI          = "api"
	ModeSubscription = "subscription"
	TunnelTailscale  = "tailscale"
	TunnelNone       = "none"
)

// KnownAgents are the coding agents dotshift can launch.
var KnownAgents = map[string]bool{"codex": true}

// Router controls how the LiteLLM proxy container is run.
type Router struct {
	// Host is the interface the proxy is published on. Keep it on loopback
	// unless you know you want it reachable from other machines.
	Host  string `yaml:"host"`
	Port  int    `yaml:"port"`
	Image string `yaml:"image"`
	// Container is the Docker container name dotshift manages.
	Container string `yaml:"container"`
	// MasterKeyEnv names an environment variable holding the key clients must
	// send as their API key. Empty means dotshift generates one and stores it
	// in .dotshift/master_key next to this config.
	MasterKeyEnv string `yaml:"master_key_env"`
	// UsageListen is the host address `dotshift serve` listens on for the
	// router's per-call usage records. Empty picks one that the container can
	// reach (loopback with Docker Desktop, the Docker bridge on Linux); "off"
	// turns usage recording off.
	UsageListen string `yaml:"usage_listen"`
}

// UsageOff is the router.usage_listen value that turns usage recording off.
const UsageOff = "off"

// UsageEnabled reports whether the router should send usage records.
func (r Router) UsageEnabled() bool { return r.UsageListen != UsageOff }

// Provider holds connection settings for one model provider.
type Provider struct {
	// APIKeyEnv names the environment variable holding this provider's key.
	// The key itself never goes in the config file.
	APIKeyEnv string `yaml:"api_key_env"`
	// BaseURL overrides the provider endpoint, e.g. a local Ollama server.
	BaseURL string `yaml:"base_url"`
}

// KnownProviders are the provider names dotshift understands. The name is also
// the LiteLLM model prefix used in aliases, e.g. "anthropic/claude-sonnet-5-5".
var KnownProviders = map[string]bool{
	"openai":      true,
	"anthropic":   true,
	"gemini":      true,
	"ollama":      true,
	"ollama_chat": true,
}

var (
	aliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	envPattern   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// Load reads and validates the config at path, filling in defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no config at %s (run `dotshift init` to create one)", path)
		}
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and validates a dotshift.yaml document.
func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Jobs.Mode == "" {
		c.Jobs.Mode = ModeAPI
	}
	if c.Jobs.ClaudeCommand == "" {
		c.Jobs.ClaudeCommand = "claude"
	}
	if c.UsesSubscriptions() {
		if c.Providers == nil {
			c.Providers = map[string]Provider{}
		}
		for _, models := range c.Aliases {
			for _, model := range models {
				p, _, _ := SplitModel(model)
				if p == "openai" || p == "anthropic" {
					if _, exists := c.Providers[p]; !exists {
						c.Providers[p] = Provider{}
					}
				}
			}
		}
	}
	if c.Router.Host == "" {
		c.Router.Host = "127.0.0.1"
	}
	if c.Router.Port == 0 {
		c.Router.Port = 4000
	}
	if c.Router.Image == "" {
		c.Router.Image = "ghcr.io/berriai/litellm:main-stable"
	}
	if c.Router.Container == "" {
		c.Router.Container = "dotshift-litellm"
	}
	if c.Jobs.Model == "" {
		if _, ok := c.Aliases["smart"]; ok {
			c.Jobs.Model = "smart"
		} else if names := c.AliasNames(); len(names) > 0 {
			c.Jobs.Model = names[0]
		}
	}
	if c.Jobs.Timeout.Duration == 0 {
		c.Jobs.Timeout.Duration = time.Hour
	}
	if c.Jobs.Strategy == "" {
		c.Jobs.Strategy = StrategyAlias
	}
	if c.Jobs.EscalateAfter == 0 {
		c.Jobs.EscalateAfter = 2
	}
	if c.Jobs.Agent.Name == "" {
		c.Jobs.Agent.Name = "codex"
	}
	if c.Jobs.Agent.Command == "" {
		c.Jobs.Agent.Command = c.Jobs.Agent.Name
	}
	if c.MCP.Listen == "" {
		c.MCP.Listen = "127.0.0.1:8787"
	}
	if c.MCP.Tunnel == "" {
		c.MCP.Tunnel = TunnelTailscale
	}
	c.MCP.PublicURL = strings.TrimRight(c.MCP.PublicURL, "/")
}

// Validate reports every problem in the config at once.
func (c *Config) Validate() error {
	var errs []string
	errs = append(errs, c.Projects.validate()...)
	if c.Jobs.Mode != "" && c.Jobs.Mode != ModeAPI && c.Jobs.Mode != ModeSubscription {
		errs = append(errs, "jobs.mode must be api or subscription")
	}
	if c.UsesSubscriptions() {
		if c.Jobs.MaxCostUSD != 0 {
			errs = append(errs, "subscription mode cannot meter USD spending; use max_iterations and jobs.timeout instead of jobs.max_cost_usd")
		}
		if len(c.Jobs.Agent.Args) != 0 {
			errs = append(errs, "jobs.agent.args is only supported in api mode; subscription mode controls authentication and sandbox arguments")
		}
		for name, p := range c.Providers {
			if name != "openai" && name != "anthropic" {
				errs = append(errs, "subscription mode only supports openai and anthropic")
			}
			if p.APIKeyEnv != "" || p.BaseURL != "" {
				errs = append(errs, "subscription mode uses CLI sign-in; remove API key and base URL settings from providers."+name)
			}
		}
	}
	if c.Router.Port < 1 || c.Router.Port > 65535 {
		errs = append(errs, fmt.Sprintf("router.port %d is out of range", c.Router.Port))
	}
	if c.Router.MasterKeyEnv != "" && !envPattern.MatchString(c.Router.MasterKeyEnv) {
		errs = append(errs, fmt.Sprintf("router.master_key_env %q is not a valid environment variable name", c.Router.MasterKeyEnv))
	}
	if l := c.Router.UsageListen; l != "" && l != UsageOff && net.ParseIP(l) == nil {
		errs = append(errs, fmt.Sprintf("router.usage_listen %q must be an IP address or %q", l, UsageOff))
	}
	for name, p := range c.Providers {
		if !KnownProviders[name] {
			errs = append(errs, fmt.Sprintf("providers.%s: unknown provider", name))
		}
		if p.APIKeyEnv != "" && !envPattern.MatchString(p.APIKeyEnv) {
			errs = append(errs, fmt.Sprintf("providers.%s.api_key_env %q is not a valid environment variable name", name, p.APIKeyEnv))
		}
	}
	if len(c.Aliases) == 0 {
		errs = append(errs, "aliases: at least one alias is required")
	}
	for _, alias := range c.AliasNames() {
		if !aliasPattern.MatchString(alias) {
			errs = append(errs, fmt.Sprintf("aliases.%s: names must be lowercase letters, digits, - or _", alias))
		}
		models := c.Aliases[alias]
		if len(models) == 0 {
			errs = append(errs, fmt.Sprintf("aliases.%s: needs at least one model", alias))
		}
		for _, m := range models {
			provider, _, ok := SplitModel(m)
			if !ok {
				errs = append(errs, fmt.Sprintf("aliases.%s: model %q must look like provider/model", alias, m))
				continue
			}
			if _, configured := c.Providers[provider]; !configured {
				errs = append(errs, fmt.Sprintf("aliases.%s: model %q uses provider %q, which is not under providers", alias, m, provider))
			}
		}
	}
	if _, ok := c.Aliases[c.Jobs.Model]; !ok && len(c.Aliases) > 0 {
		errs = append(errs, fmt.Sprintf("jobs.model %q is not one of the aliases", c.Jobs.Model))
	}
	if _, err := c.StrategyModels(c.Jobs.Model, c.Jobs.Strategy); err != nil {
		errs = append(errs, "jobs.strategy: "+err.Error())
	}
	if c.Jobs.EscalateAfter < 1 {
		errs = append(errs, "jobs.escalate_after must be positive")
	}
	if err := ValidateMaxCost(c.Jobs.MaxCostUSD); err != nil {
		errs = append(errs, "jobs.max_cost_usd: "+err.Error())
	}
	if c.Jobs.MaxCostUSD > 0 && !c.Router.UsageEnabled() {
		errs = append(errs, "jobs.max_cost_usd requires usage recording")
	}
	if err := c.ValidateReviewer(c.Jobs.Model, c.Jobs.Strategy, c.Jobs.ReviewerModel); err != nil {
		errs = append(errs, "jobs.reviewer_model: "+err.Error())
	}
	if c.Jobs.Timeout.Duration < 0 {
		errs = append(errs, "jobs.timeout must be positive")
	}
	if !KnownAgents[c.Jobs.Agent.Name] {
		errs = append(errs, fmt.Sprintf("jobs.agent.name %q is not a known agent (codex)", c.Jobs.Agent.Name))
	}
	for _, name := range c.RepoNames() {
		if !aliasPattern.MatchString(name) {
			errs = append(errs, fmt.Sprintf("repos.%s: names must be lowercase letters, digits, - or _", name))
		}
		if c.Repos[name].Path == "" {
			errs = append(errs, fmt.Sprintf("repos.%s.path is required", name))
		}
	}
	if _, port, err := net.SplitHostPort(c.MCP.Listen); err != nil || port == "" {
		errs = append(errs, fmt.Sprintf("mcp.listen %q must look like host:port, e.g. 127.0.0.1:8787", c.MCP.Listen))
	}
	if c.MCP.PublicURL != "" {
		u, err := url.Parse(c.MCP.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			errs = append(errs, fmt.Sprintf("mcp.public_url %q must be a URL with no path, like https://dotshift.example.com", c.MCP.PublicURL))
		}
	}
	if c.MCP.Tunnel != TunnelTailscale && c.MCP.Tunnel != TunnelNone {
		errs = append(errs, fmt.Sprintf("mcp.tunnel %q must be tailscale or none", c.MCP.Tunnel))
	}
	if c.MCP.PasscodeEnv != "" && !envPattern.MatchString(c.MCP.PasscodeEnv) {
		errs = append(errs, fmt.Sprintf("mcp.passcode_env %q is not a valid environment variable name", c.MCP.PasscodeEnv))
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// ExecutionMode normalizes older configurations to API mode.
func (c *Config) ExecutionMode() string {
	if c.UsesSubscriptions() {
		return ModeSubscription
	}
	return ModeAPI
}

func (c *Config) UsesSubscriptions() bool { return c.Jobs.Mode == ModeSubscription }

// ValidateMaxCost also rejects NaN/Inf before they can enter a persistent budget.
func ValidateMaxCost(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1e9 {
		return errors.New("must be a finite USD amount between 0 and 1000000000; 0 disables the cap")
	}
	return nil
}

// AliasNames returns the alias names in a stable order.
func (c *Config) AliasNames() []string {
	names := make([]string, 0, len(c.Aliases))
	for n := range c.Aliases {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// KeyEnvs returns the provider API key variables the proxy needs, sorted.
func (c *Config) KeyEnvs() []string {
	seen := map[string]bool{}
	for _, p := range c.Providers {
		if p.APIKeyEnv != "" {
			seen[p.APIKeyEnv] = true
		}
	}
	envs := make([]string, 0, len(seen))
	for e := range seen {
		envs = append(envs, e)
	}
	sort.Strings(envs)
	return envs
}

// SplitModel splits "provider/model" into its parts. The model part may
// itself contain slashes (e.g. "openai/org/model").
func SplitModel(m string) (provider, model string, ok bool) {
	provider, model, ok = strings.Cut(m, "/")
	if !ok || provider == "" || model == "" {
		return "", "", false
	}
	return provider, model, true
}

// RepoNames returns the allowlisted repo names in a stable order.
func (c *Config) RepoNames() []string {
	names := make([]string, 0, len(c.Repos))
	for n := range c.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
