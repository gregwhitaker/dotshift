package mcpintake

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSessionlessToolDiscovery(t *testing.T) {
	srv, store := newTestServer(t, newRunner(t, editScript), "")
	token, err := store.issue(context.Background(), accessToken, "discovery-client", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2025-06-18", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			for _, authenticated := range []bool{false, true} {
				params := map[string]any{}
				if version == "2026-07-28" {
					params["_meta"] = map[string]any{
						mcp.MetaKeyProtocolVersion:    version,
						mcp.MetaKeyClientCapabilities: map[string]any{},
						mcp.MetaKeyClientInfo:         map[string]any{"name": "discovery-test", "version": "1"},
					}
				}
				payload, err := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": params,
				})
				if err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequest(http.MethodPost, srv.URL+mcpPath, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("MCP-Protocol-Version", version)
				req.Header.Set("Mcp-Method", "tools/list")
				if authenticated {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if !authenticated {
					if resp.StatusCode != http.StatusUnauthorized {
						t.Fatalf("unauthenticated tools/list: status=%d, want 401", resp.StatusCode)
					}
					continue
				}
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("tools/list without a session: status=%d, body=%s", resp.StatusCode, data)
				}
				if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
					t.Fatalf("Content-Type=%q, want application/json", got)
				}
				if got := resp.Header.Get("Mcp-Session-Id"); got != "" {
					t.Fatal("discovery must not require an HTTP session")
				}
				var result struct {
					Result struct {
						Tools []struct{ Name string }
					}
				}
				if err := json.Unmarshal(data, &result); err != nil {
					t.Fatal(err)
				}
				found := map[string]bool{}
				for _, tool := range result.Result.Tools {
					found[tool.Name] = true
				}
				for _, name := range []string{"list_repos", "create_project", "start_goal", "resume_goal"} {
					if !found[name] {
						t.Errorf("discovery missing %s", name)
					}
				}
			}
		})
	}
}

func TestOAuthDiscoveryDoesNotAdvertiseOpenID(t *testing.T) {
	srv, _ := newTestServer(t, newRunner(t, editScript), "")
	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-authorization-server/mcp",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]any
		err = json.NewDecoder(resp.Body).Decode(&metadata)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d, decode error=%v", path, resp.StatusCode, err)
		}
		if metadata["issuer"] != srv.URL || metadata["registration_endpoint"] != srv.URL+registerPath {
			t.Fatalf("%s: wrong OAuth metadata: %v", path, metadata)
		}
	}
	// Serving OAuth metadata as an OpenID configuration makes clients infer
	// identity-provider features that dotshift does not implement.
	for _, path := range []string{
		"/.well-known/openid-configuration",
		"/.well-known/openid-configuration/mcp",
		"/mcp/.well-known/openid-configuration",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404", path, resp.StatusCode)
		}
	}
}
