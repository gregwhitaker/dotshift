package check

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/gregwhitaker/dotshift/internal/agent"
)

type modelResponse struct {
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	Error             *responseError    `json:"error"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type responseError struct {
	Message string `json:"message"`
}

type outputItem struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

// ResponseStream checks the streaming endpoint used by the coding agent.
func (c *Client) ResponseStream(ctx context.Context, name string) error {
	resp, err := c.responses(ctx, map[string]any{
		"model": name, "input": "Reply with the single word: ok",
	})
	if err != nil {
		return err
	}
	_, err = resp.text()
	return err
}

// ResponseToolLoop returns a deterministic function result and checks that the
// model can continue using the original output items and matching call ID.
func (c *Client) ResponseToolLoop(ctx context.Context, name string) error {
	input := []any{map[string]any{"role": "user", "content": "Call get_weather for Paris, then report the returned weather."}}
	tool := map[string]any{
		"type": "function", "name": toolName, "description": "Get the weather for a city.", "strict": true,
		"parameters": map[string]any{
			"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required": []string{"city"}, "additionalProperties": false,
		},
	}
	resp, err := c.responses(ctx, map[string]any{
		"model": name, "input": input, "tools": []any{tool},
		"tool_choice": map[string]any{"type": "function", "name": toolName}, "parallel_tool_calls": false,
	})
	if err != nil {
		return err
	}
	var calls []outputItem
	for _, raw := range resp.Output {
		var item outputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return fmt.Errorf("decode output item: %w", err)
		}
		// Preserve reasoning items as well as tool calls for stateless replay.
		input = append(input, raw)
		if item.Type == "function_call" {
			var args struct {
				City string `json:"city"`
			}
			if item.Name != toolName || item.CallID == "" || json.Unmarshal([]byte(item.Arguments), &args) != nil || !strings.EqualFold(args.City, "Paris") {
				return errors.New("Responses returned an invalid get_weather function call")
			}
			calls = append(calls, item)
		}
	}
	if len(calls) != 1 {
		return fmt.Errorf("Responses returned %d function calls; expected one", len(calls))
	}
	input = append(input, map[string]any{
		"type": "function_call_output", "call_id": calls[0].CallID, "output": `{"weather":"sunny"}`,
	})
	resp, err = c.responses(ctx, map[string]any{
		"model": name, "input": input, "tools": []any{tool}, "tool_choice": "none",
	})
	if err != nil {
		return fmt.Errorf("continuing after the tool result: %w", err)
	}
	text, err := resp.text()
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(text), "sunny") {
		return errors.New("Responses did not use the supplied tool result in its answer")
	}
	return nil
}

// GoalOutput checks the same strict JSON schema supplied to goal agents.
func (c *Client) GoalOutput(ctx context.Context, name string) error {
	resp, err := c.responses(ctx, map[string]any{
		"model": name,
		"input": `Return a JSON object with status "complete", summary "schema-ok", and next_steps "".`,
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": "dotshift_goal", "strict": true,
			"schema": json.RawMessage(agent.GoalOutputSchema),
		}},
	})
	if err != nil {
		return err
	}
	text, err := resp.text()
	if err != nil {
		return err
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		return fmt.Errorf("goal output is not the required JSON object: %w", err)
	}
	if len(report) != 3 || report["status"] != "complete" || report["summary"] != "schema-ok" || report["next_steps"] != "" {
		return errors.New("goal output did not match the requested status, summary, and next_steps")
	}
	return nil
}

func (r *modelResponse) text() (string, error) {
	var text strings.Builder
	for _, raw := range r.Output {
		var item outputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return "", fmt.Errorf("decode output item: %w", err)
		}
		for _, content := range item.Content {
			if content.Type == "refusal" {
				return "", fmt.Errorf("model refused the check: %s", content.Refusal)
			}
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("Responses completed without text output")
	}
	return text.String(), nil
}

func (c *Client) responses(ctx context.Context, body map[string]any) (*modelResponse, error) {
	body["stream"], body["store"] = true, false
	body["include"] = []string{"reasoning.encrypted_content"}
	resp, err := c.post(ctx, "/responses", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return nil, fmt.Errorf("Responses did not return an event stream (Content-Type %q)", resp.Header.Get("Content-Type"))
	}
	return readResponseStream(resp.Body)
}

// readResponseStream requires a successful completion event. EOF, [DONE], or
// text deltas alone cannot turn a truncated or failed request into a pass.
func readResponseStream(reader io.Reader) (*modelResponse, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if data.Len() > 1<<20 {
				return nil, errors.New("Responses event exceeded 1 MiB")
			}
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		if strings.TrimSpace(data.String()) == "[DONE]" {
			return nil, errors.New("Responses stream ended before response.completed")
		}
		var event struct {
			Type     string         `json:"type"`
			Response *modelResponse `json:"response"`
			Message  string         `json:"message"`
			Error    *responseError `json:"error"`
		}
		if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
			return nil, fmt.Errorf("decode Responses event: %w", err)
		}
		data.Reset()
		switch event.Type {
		case "response.completed", "response.failed", "response.incomplete":
			if event.Response == nil {
				return nil, fmt.Errorf("%s event has no response", event.Type)
			}
			if event.Response.Error != nil {
				return nil, fmt.Errorf("Responses failed: %s", event.Response.Error.Message)
			}
			if event.Type != "response.completed" || event.Response.Status != "completed" {
				return nil, fmt.Errorf("Responses ended %s (%s)", event.Response.Status, event.Response.IncompleteDetails.Reason)
			}
			return event.Response, nil
		case "error":
			if event.Error != nil {
				event.Message = event.Error.Message
			}
			return nil, fmt.Errorf("Responses stream error: %s", event.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Responses stream: %w", err)
	}
	return nil, errors.New("Responses stream closed before response.completed")
}
