// Package router runs the LiteLLM proxy in Docker and checks on it.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/litellm"
)

// Router manages the LiteLLM proxy container described by a dotshift config.
type Router struct {
	cfg        *config.Config
	configPath string // generated LiteLLM config on the host
	masterKey  string
	docker     string
	client     *http.Client
	// usageEndpoint and usageToken, when set, are where the proxy posts usage
	// records and the token it sends with them.
	usageEndpoint string
	usageToken    string
}

// masterKeyEnv is the variable LiteLLM reads its master key from.
const masterKeyEnv = "LITELLM_MASTER_KEY"

// Variables LiteLLM's generic_api callback reads its endpoint and headers from.
const (
	usageEndpointEnv = "GENERIC_LOGGER_ENDPOINT"
	usageHeadersEnv  = "GENERIC_LOGGER_HEADERS"
)

// HostAlias is the name the container uses to reach this machine.
const HostAlias = "host.docker.internal"

// New returns a Router that mounts the LiteLLM config at configPath and
// protects the proxy with masterKey.
func New(cfg *config.Config, configPath, masterKey string) *Router {
	return &Router{cfg: cfg, configPath: configPath, masterKey: masterKey, docker: "docker", client: &http.Client{Timeout: 5 * time.Second}}
}

// BaseURL is the OpenAI-compatible base URL coding agents should use.
func (r *Router) BaseURL() string {
	return fmt.Sprintf("http://%s:%d/v1", r.cfg.Router.Host, r.cfg.Router.Port)
}

// APIKey is the key clients send to the proxy.
func (r *Router) APIKey() string { return r.masterKey }

func (r *Router) rootURL() string {
	return fmt.Sprintf("http://%s:%d", r.cfg.Router.Host, r.cfg.Router.Port)
}

// RunArgs returns the `docker run` arguments for the proxy container. Keys are
// passed with `-e NAME` so Docker copies them from dotshift's environment and
// their values never appear on the command line.
func (r *Router) RunArgs() []string {
	c := r.cfg.Router
	args := []string{
		"run", "--rm", "--detach",
		"--name", c.Container,
		"--publish", fmt.Sprintf("%s:%d:%d", c.Host, c.Port, litellm.ContainerPort),
		"--add-host", HostAlias + ":host-gateway",
		"--volume", fmt.Sprintf("%s:%s:ro", r.configPath, litellm.ContainerConfigPath),
	}
	envs := append(r.cfg.KeyEnvs(), masterKeyEnv)
	if r.usageEndpoint != "" {
		envs = append(envs, usageEndpointEnv, usageHeadersEnv)
	}
	for _, env := range envs {
		args = append(args, "--env", env)
	}
	args = append(args, c.Image,
		"--config", litellm.ContainerConfigPath,
		"--port", fmt.Sprint(litellm.ContainerPort))
	return args
}

// SendUsage makes the proxy post a usage record for every call to endpoint,
// authenticated with token. Call it before Start.
func (r *Router) SendUsage(endpoint, token string) {
	r.usageEndpoint, r.usageToken = endpoint, token
}

// BridgeGateway returns the host's address on Docker's default bridge
// network, which is where host.docker.internal points on Linux.
func (r *Router) BridgeGateway(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, r.docker, "network", "inspect", "bridge",
		"--format", "{{range .IPAM.Config}}{{.Gateway}} {{end}}").Output()
	if err != nil {
		return "", fmt.Errorf("docker network inspect bridge: %w", err)
	}
	for _, f := range strings.Fields(string(out)) {
		if ip := net.ParseIP(f); ip != nil && ip.To4() != nil {
			return f, nil
		}
	}
	return "", errors.New("docker's bridge network has no IPv4 gateway")
}

// MissingEnv lists key variables named in the config but unset here.
func (r *Router) MissingEnv() []string {
	var missing []string
	for _, env := range r.cfg.KeyEnvs() {
		if os.Getenv(env) == "" {
			missing = append(missing, env)
		}
	}
	return missing
}

// Start removes any old proxy container and starts a fresh one.
func (r *Router) Start(ctx context.Context) error {
	if _, err := exec.LookPath(r.docker); err != nil {
		return errors.New("docker not found on PATH; dotshift runs LiteLLM in Docker")
	}
	_ = r.Stop(ctx)
	cmd := exec.CommandContext(ctx, r.docker, r.RunArgs()...)
	cmd.Env = append(os.Environ(), masterKeyEnv+"="+r.masterKey)
	if r.usageEndpoint != "" {
		cmd.Env = append(cmd.Env,
			usageEndpointEnv+"="+r.usageEndpoint,
			usageHeadersEnv+"=Authorization=Bearer "+r.usageToken)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop removes the proxy container if it exists.
func (r *Router) Stop(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, r.docker, "rm", "--force", r.cfg.Router.Container)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("docker rm failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Logs streams the container's logs to w until ctx is cancelled.
func (r *Router) Logs(ctx context.Context, w io.Writer) error {
	cmd := exec.CommandContext(ctx, r.docker, "logs", "--follow", r.cfg.Router.Container)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

// Running reports whether the proxy container is up.
func (r *Router) Running(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, r.docker, "inspect", "--format", "{{.State.Running}}", r.cfg.Router.Container).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// WaitHealthy polls the proxy's liveness endpoint until it answers or the
// timeout passes. It fails early if the container exits.
func (r *Router) WaitHealthy(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if r.Healthy(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("router did not become healthy within %s (see `docker logs %s`)", timeout, r.cfg.Router.Container)
		case <-tick.C:
			if !r.Running(ctx) {
				return fmt.Errorf("router container exited during startup (see `docker logs %s`)", r.cfg.Router.Container)
			}
		}
	}
}

// Healthy reports whether the proxy answers its liveness check.
func (r *Router) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.rootURL()+"/health/liveliness", nil)
	if err != nil {
		return false
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Models returns the model names the proxy serves.
func (r *Router) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.BaseURL()+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.masterKey)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /v1/models: %s", resp.Status)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	ids := make([]string, len(body.Data))
	for i, m := range body.Data {
		ids[i] = m.ID
	}
	return ids, nil
}
