package mcpintake

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestContactRequiresAuthenticatedMCPRequest(t *testing.T) {
	runner := newRunner(t, editScript)
	store, err := OpenAuthStore(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var contacts atomic.Int32
	h, err := NewHandler(Options{Jobs: runner, DefaultModel: runner.Config.Jobs.Model, Models: runner.Config.AliasNames(), Auth: store, Passcode: testPasscode, OnAuthenticated: func() { contacts.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.issue(context.Background(), accessToken, "test-client", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/mcp"} {
		for _, bearer := range []string{"", "wrong", token} {
			r := httptest.NewRequest("POST", "http://127.0.0.1:9090"+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			if bearer != "" {
				r.Header.Set("Authorization", "Bearer "+bearer)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)
			want := int32(0)
			if path == "/mcp" && bearer == token {
				want = 1
			}
			if contacts.Load() != want {
				t.Fatalf("path=%s recorded unauthenticated contact", path)
			}
		}
	}
}
