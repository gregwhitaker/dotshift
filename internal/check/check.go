// Package check sends small test requests through the router to every model
// behind every alias, so a bad key or model ID shows up before a coding agent
// hits it.
package check

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/litellm"
)

// Target is one model behind an alias, addressed by its router model name.
type Target struct {
	Alias string
	// Position is 0 for the alias's primary model and n for its nth fallback.
	Position int
	// Name is what the router calls this model: the alias for the primary,
	// litellm.FallbackName for fallbacks.
	Name string
	// Model is the provider/model string from dotshift.yaml.
	Model string
}

// Targets lists every model of the given aliases (all aliases when none are
// named), in alias order then fallback order.
func Targets(cfg *config.Config, aliases []string) ([]Target, error) {
	if len(aliases) == 0 {
		aliases = cfg.AliasNames()
	}
	var out []Target
	for _, alias := range aliases {
		models, ok := cfg.Aliases[alias]
		if !ok {
			return nil, fmt.Errorf("no alias %q in config (have: %s)", alias, strings.Join(cfg.AliasNames(), ", "))
		}
		for i, m := range models {
			name := alias
			if i > 0 {
				name = litellm.FallbackName(alias, i)
			}
			out = append(out, Target{Alias: alias, Position: i, Name: name, Model: m})
		}
	}
	return out, nil
}

// Outcome is the result of one test request.
type Outcome struct {
	Err     error
	Latency time.Duration
}

// OK reports whether the request passed.
func (o Outcome) OK() bool { return o.Err == nil }

// Result holds both test requests for one target.
type Result struct {
	Target
	Chat  Outcome
	Tools Outcome
}

// OK reports whether both requests passed.
func (r Result) OK() bool { return r.Chat.OK() && r.Tools.OK() }

// Client sends test requests to the router's OpenAI-compatible API.
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:4000/v1
	Key     string
	HTTP    *http.Client
}

// Run checks every target, a few at a time, and returns results in target order.
func (c *Client) Run(ctx context.Context, targets []Target) []Result {
	results := make([]Result, len(targets))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = Result{Target: t, Chat: c.timed(ctx, t.Name, c.Chat), Tools: c.timed(ctx, t.Name, c.ToolCall)}
		}()
	}
	wg.Wait()
	return results
}

func (c *Client) timed(ctx context.Context, name string, f func(context.Context, string) error) Outcome {
	start := time.Now()
	err := f(ctx, name)
	return Outcome{Err: err, Latency: time.Since(start)}
}

// Chat asks the model for a one-word reply and passes if it answers at all.
func (c *Client) Chat(ctx context.Context, name string) error {
	resp, err := c.complete(ctx, map[string]any{
		"model":    name,
		"messages": []any{map[string]any{"role": "user", "content": "Reply with the single word: ok"}},
	})
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return fmt.Errorf("response had no choices")
	}
	return nil
}

// toolName is the function the tool-call check asks the model to call.
const toolName = "get_weather"

// ToolCall offers the model one function, requires it to be called, and passes
// only if the reply contains that call. Coding agents depend on tool calls, so
// a model that chats but can't call tools is not usable for them.
func (c *Client) ToolCall(ctx context.Context, name string) error {
	resp, err := c.complete(ctx, map[string]any{
		"model":    name,
		"messages": []any{map[string]any{"role": "user", "content": "What is the weather in Paris? Use the tool."}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        toolName,
				"description": "Get the current weather for a city.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"city": map[string]any{"type": "string"}},
					"required":   []string{"city"},
				},
			},
		}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": toolName}},
	})
	if err != nil {
		return err
	}
	for _, ch := range resp.Choices {
		for _, tc := range ch.Message.ToolCalls {
			if tc.Function.Name == toolName {
				return nil
			}
		}
	}
	return fmt.Errorf("model answered without calling the tool")
}

type completion struct {
	Choices []struct {
		Message struct {
			ToolCalls []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *Client) complete(ctx context.Context, body map[string]any) (*completion, error) {
	// Test exactly this model: without this LiteLLM would fall back to the
	// next model and hide the failure.
	body["disable_fallbacks"] = true
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, errorMessage(raw))
	}
	var out completion
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

// errorMessage pulls the message out of an OpenAI-style error body and keeps
// it to one short line.
func errorMessage(raw []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		msg = body.Error.Message
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 200 {
		msg = msg[:197] + "..."
	}
	return msg
}
