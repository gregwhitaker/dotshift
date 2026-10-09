package check

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestTargets(t *testing.T) {
	c, err := config.Parse([]byte("providers:\n  openai: {}\n  anthropic: {}\naliases:\n  smart: [anthropic/claude-sonnet-5-5, openai/gpt-5]\n  fast: [openai/gpt-5-mini]\n"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := Targets(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tg := range all {
		names = append(names, tg.Name+"="+tg.Model)
	}
	if got := strings.Join(names, " "); got != "fast=openai/gpt-5-mini smart=anthropic/claude-sonnet-5-5 smart~1=openai/gpt-5" {
		t.Errorf("Targets = %s", got)
	}
	if _, err := Targets(c, []string{"nope"}); err == nil {
		t.Error("unknown alias accepted")
	}
}

// fakeRouter answers like LiteLLM: "good" chats and calls tools, "chatty"
// chats but ignores tools, anything else is a provider error.
func fakeRouter(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Model            string `json:"model"`
			Tools            []any  `json:"tools"`
			DisableFallbacks bool   `json:"disable_fallbacks"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !req.DisableFallbacks {
			t.Error("request did not disable fallbacks")
		}
		switch {
		case req.Model == "good" && len(req.Tools) > 0:
			w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"get_weather","arguments":"{}"}}]}}]}`))
		case req.Model == "good" || req.Model == "chatty":
			w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"message":"AuthenticationError: invalid x-api-key"}}`))
		}
	}))
}

func TestRun(t *testing.T) {
	srv := fakeRouter(t)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "sk-test", HTTP: srv.Client()}
	results := c.Run(context.Background(), []Target{{Name: "good"}, {Name: "chatty"}, {Name: "broken"}})

	if !results[0].OK() {
		t.Errorf("good: %+v", results[0])
	}
	if !results[1].Chat.OK() || results[1].Tools.OK() {
		t.Errorf("chatty: chat %v, tools %v", results[1].Chat.Err, results[1].Tools.Err)
	}
	if err := results[2].Chat.Err; err == nil || !strings.Contains(err.Error(), "invalid x-api-key") {
		t.Errorf("broken chat error = %v", err)
	}
}
