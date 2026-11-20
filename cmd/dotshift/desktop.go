package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/dashboard"
)

// The menu app is a client of the existing service. These read-only commands
// neither open the job database nor start/stop a runner. No session key crosses
// the JSON bridge, and connection checks never make model requests.
func init() {
	commands["desktop"] = command("desktop", nil, func(ctx context.Context, path string, fs *flag.FlagSet) error {
		if fs.NArg() != 1 {
			return errors.New("usage: dotshift desktop [-config FILE] status|connections|restart")
		}
		switch fs.Arg(0) {
		case "status":
			status, err := desktopStatus(ctx, path)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(status)
		case "connections":
			connections, err := desktopConnections(ctx, path)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(connections)
		case "restart":
			if err := runStop(ctx, path); err != nil {
				return err
			}
			_, err := ensureUI(ctx, path, false)
			return err
		default:
			return errors.New("unknown desktop action")
		}
	})
	var bundle string
	commands["menubar"] = command("menubar", func(fs *flag.FlagSet) {
		fs.StringVar(&bundle, "app", "", "path to Dotshift.app")
	}, func(ctx context.Context, path string, fs *flag.FlagSet) error {
		if fs.NArg() != 0 {
			return errors.New("usage: dotshift menubar [-config FILE] [-app /path/to/Dotshift.app]")
		}
		return launchMenuBar(ctx, path, bundle)
	})
}

type desktopSnapshot struct {
	Running       bool                 `json:"running"`
	ExecutionMode string               `json:"execution_mode"`
	MCPEnabled    bool                 `json:"mcp_enabled"`
	ConfigPath    string               `json:"config_path"`
	Jobs          json.RawMessage      `json:"jobs"`
	Readiness     *dashboard.Readiness `json:"readiness,omitempty"`
}

func desktopConfig(ctx context.Context, path string) (*config.Config, *dashboard.Record, string, error) {
	path, err := canonicalConfig(path)
	if err != nil {
		return nil, nil, "", err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, "", err
	}
	dir, err := stateDir(path)
	if err != nil {
		return nil, nil, "", err
	}
	record, err := dashboard.Discover(ctx, dir, path)
	return cfg, record, path, err
}

func desktopRead(ctx context.Context, record *dashboard.Record, path string, out any) error {
	return desktopReadMethod(ctx, record, http.MethodGet, path, out)
}

func desktopReadMethod(ctx context.Context, record *dashboard.Record, method, path string, out any) error {
	timeout := 2 * time.Second
	if path == "/api/readiness/check" {
		timeout = 30 * time.Second
	}
	response, err := record.RequestWithTimeout(ctx, method, path, timeout)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("dashboard returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(out)
}

func desktopStatus(ctx context.Context, path string) (*desktopSnapshot, error) {
	cfg, record, path, err := desktopConfig(ctx, path)
	if err != nil {
		return nil, err
	}
	snapshot := &desktopSnapshot{ExecutionMode: cfg.ExecutionMode(), ConfigPath: path, Jobs: json.RawMessage("[]")}
	if record == nil {
		return snapshot, nil
	}
	// Report the running configuration, which may differ from an edited file.
	if err := desktopRead(ctx, record, "/api/info", snapshot); err != nil {
		return nil, err
	}
	snapshot.Running = true
	if err := desktopRead(ctx, record, "/api/jobs", &snapshot.Jobs); err != nil {
		return nil, err
	}
	return snapshot, nil
}

type desktopConnection struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"`
	Detail  string     `json:"detail,omitempty"`
	RetryAt *time.Time `json:"retry_at,omitempty"`
}

func desktopConnections(ctx context.Context, path string) ([]desktopConnection, error) {
	cfg, record, _, err := desktopConfig(ctx, path)
	if err != nil {
		return nil, err
	}
	if record != nil {
		var readiness dashboard.Readiness
		if err := desktopReadMethod(ctx, record, http.MethodPost, "/api/readiness/check", &readiness); err != nil {
			return nil, err
		}
		rows := []desktopConnection{{Name: "Local coding", Status: readiness.Label, Detail: readiness.Detail}}
		for _, check := range readiness.Checks {
			detail := check.Detail
			if check.Name == "Dot connection" && readiness.LastContact != nil {
				detail += " Last authenticated contact: " + readiness.LastContact.Local().Format(time.RFC822) + "."
			}
			rows = append(rows, desktopConnection{Name: check.Name, Status: check.Label, Detail: detail})
		}
		return rows, nil
	}
	rows := []desktopConnection{{Name: "Local service", Status: "Stopped"}, {Name: "Remote access", Status: "Off"}}
	if cfg.UsesSubscriptions() {
		providers := map[string]bool{}
		for _, alias := range cfg.AliasNames() {
			routes, err := cfg.StrategyModels(alias, "alias")
			if err != nil {
				continue
			}
			for _, route := range routes {
				providers[route.Provider] = true
			}
		}
		checks := []desktopConnection{}
		for _, provider := range []string{"openai", "anthropic"} {
			if providers[provider] {
				checks = append(checks, desktopConnection{Name: provider})
			}
		}
		subscriptions := &agent.Subscriptions{CodexPath: cfg.Jobs.Agent.Command, ClaudePath: cfg.Jobs.ClaudeCommand}
		var wg sync.WaitGroup
		for i := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				provider := checks[i].Name
				checks[i].Name = map[string]string{"openai": "Codex", "anthropic": "Claude"}[provider]
				if err := subscriptions.Check(ctx, provider); err != nil {
					checks[i].Status, checks[i].Detail = "Needs setup", err.Error()
				} else {
					checks[i].Status = "Signed in"
				}
			}()
		}
		wg.Wait()
		rows = append(rows, checks...)
	} else {
		rows = append(rows, desktopConnection{Name: "Models", Status: "API mode", Detail: "Ask Dot to choose a configured model. Connection checks make no model requests."})
	}
	return rows, nil
}

func desktopRemoteHealth(ctx context.Context, publicURL string) error {
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("remote access requires a valid HTTPS address")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(publicURL, "/")+"/healthz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("the remote health endpoint did not respond; check Tailscale and the service log")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("remote health endpoint returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64))
	if err != nil || strings.TrimSpace(string(body)) != "ok" {
		return errors.New("the connection address did not return a dotshift health response")
	}
	return nil
}

type menuLaunch struct {
	Config    string `json:"config"`
	CLI       string `json:"cli"`
	Directory string `json:"directory"`
	Path      string `json:"path"`
}

func writeMenuLaunch(path string) (string, error) {
	path, err := canonicalConfig(path)
	if err != nil {
		return "", err
	}
	if _, err := config.Load(path); err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, err := stateDir(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".menubar-launch-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(menuLaunch{Config: path, CLI: exe, Directory: cwd, Path: os.Getenv("PATH")})
	if err := errors.Join(err, f.Close()); err != nil {
		return "", err
	}
	destination := filepath.Join(dir, "menubar-launch.json")
	return destination, os.Rename(f.Name(), destination)
}
