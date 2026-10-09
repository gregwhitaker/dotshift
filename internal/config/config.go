// Package config loads and validates dotshift.yaml.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strings"

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
}

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
}

// Validate reports every problem in the config at once.
func (c *Config) Validate() error {
	var errs []string
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
	if len(errs) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(errs, "\n  - "))
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
