package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is the private receipt used to reconnect to the service. Its token
// stays in a mode-0600 file and browser URL fragments, never query strings.
type Record struct {
	URL        string `json:"url"`
	Token      string `json:"token"`
	Instance   string `json:"instance"`
	ConfigPath string `json:"config_path"`
	PID        int    `json:"pid"`
	MCPEnabled bool   `json:"mcp_enabled"`
}

// Save atomically publishes a ready service while its process holds the lock.
func (r Record) Save(dir string) error {
	f, err := os.CreateTemp(dir, ".service-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "service.json"))
}

// BrowserURL exchanges a fragment token for a private browser session on load.
func (r Record) BrowserURL() string { return r.URL + "/#token=" + r.Token }

// BrowserRoute keeps authenticated deep links on the local dashboard. Never
// resolve an arbitrary URL here: the fragment contains the private session key.
func (r Record) BrowserRoute(route string) (string, error) {
	u, err := url.Parse(route)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") || strings.ContainsAny(route, "\\\r\n") {
		return "", errors.New("monitor path must be a local path such as /models or /goals/ID")
	}
	return r.URL + u.String() + "#token=" + r.Token, nil
}

// Discover verifies a receipt against the live, authenticated service. Stale
// receipts return nil. A different configuration in the same state directory
// is an error, because both would otherwise share the same jobs database.
func Discover(ctx context.Context, dir, configPath string) (*Record, error) {
	data, err := os.ReadFile(filepath.Join(dir, "service.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("invalid service receipt: %w", err)
	}
	u, err := url.Parse(record.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(record.Token) != 64 {
		return nil, errors.New("invalid local service address or token")
	}
	res, err := record.Request(ctx, http.MethodGet, "/api/info")
	if err != nil {
		return nil, nil
	}
	defer res.Body.Close()
	var info struct {
		Instance   string `json:"instance"`
		ConfigPath string `json:"config_path"`
		MCPEnabled bool   `json:"mcp_enabled"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&info) != nil || info.Instance != record.Instance || info.ConfigPath != record.ConfigPath || info.MCPEnabled != record.MCPEnabled {
		return nil, nil
	}
	if record.ConfigPath != configPath {
		return nil, fmt.Errorf("a service for %s already owns this .dotshift directory", record.ConfigPath)
	}
	return &record, nil
}

// Request sends an authenticated local control request, without proxies or redirects.
func (r Record) Request(ctx context.Context, method, path string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, r.URL+path, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+r.Token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client.Do(request)
}
