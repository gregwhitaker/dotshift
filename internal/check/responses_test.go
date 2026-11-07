package check

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/agent"
)

func writeCompleted(w http.ResponseWriter, output []any) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	data, _ := json.Marshal(map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"id": "resp_test", "status": "completed", "output": output},
	})
	fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", data)
}

func textOutput(text string) []any {
	return []any{map[string]any{
		"type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text}},
	}}
}

func fakeResponses(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.URL.Path != "/responses" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("unexpected Responses request: %s %s", r.Method, r.URL.Path)
	}
	var req struct {
		Model            string            `json:"model"`
		Stream           bool              `json:"stream"`
		Store            *bool             `json:"store"`
		Include          []string          `json:"include"`
		DisableFallbacks bool              `json:"disable_fallbacks"`
		Input            json.RawMessage   `json:"input"`
		Tools            []json.RawMessage `json:"tools"`
		ToolChoice       json.RawMessage   `json:"tool_choice"`
		Text             struct {
			Format struct {
				Type   string         `json:"type"`
				Strict bool           `json:"strict"`
				Schema map[string]any `json:"schema"`
			} `json:"format"`
		} `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Error(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !req.Stream || req.Store == nil || *req.Store || !req.DisableFallbacks || !reflect.DeepEqual(req.Include, []string{"reasoning.encrypted_content"}) {
		t.Errorf("missing streaming, stateless replay, or fallback settings: %+v", req)
	}
	if req.Model == "broken" || req.Model == "chatty" {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"AuthenticationError: invalid x-api-key"}}`)
		return
	}
	if req.Model == "buffered" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"completed","output":[]}`)
		return
	}
	if len(req.Tools) > 0 {
		if string(req.ToolChoice) != `"none"` {
			callID := "call_test"
			if req.Model == "missing-call-id" {
				callID = ""
			}
			writeCompleted(w, []any{
				map[string]any{"type": "reasoning", "id": "reasoning_test", "summary": []any{}, "encrypted_content": "opaque"},
				map[string]any{"type": "function_call", "id": "fc_test", "call_id": callID, "name": "get_weather", "arguments": `{"city":"Paris"}`},
			})
			return
		}
		var input []map[string]any
		if err := json.Unmarshal(req.Input, &input); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(input) != 4 || input[1]["encrypted_content"] != "opaque" || input[2]["call_id"] != "call_test" ||
			input[3]["type"] != "function_call_output" || input[3]["call_id"] != "call_test" || input[3]["output"] != `{"weather":"sunny"}` {
			t.Errorf("tool continuation lost history or correlation: %s", req.Input)
		}
		answer := "It is sunny in Paris."
		if req.Model == "ignored-tool" {
			answer = "I do not know."
		}
		writeCompleted(w, textOutput(answer))
		return
	}
	if req.Text.Format.Type != "" {
		var want map[string]any
		json.Unmarshal([]byte(agent.GoalOutputSchema), &want)
		if req.Text.Format.Type != "json_schema" || !req.Text.Format.Strict || !reflect.DeepEqual(req.Text.Format.Schema, want) {
			t.Error("structured check did not use the goal runner's strict output schema")
		}
		answer := `{"status":"complete","summary":"schema-ok","next_steps":""}`
		if req.Model == "missing-field" {
			answer = `{"status":"complete","summary":"schema-ok"}`
		}
		if req.Model == "extra-field" {
			answer = `{"status":"complete","summary":"schema-ok","next_steps":"","extra":"ignored schema"}`
		}
		if req.Model == "null-field" {
			answer = `{"status":"complete","summary":"schema-ok","next_steps":null}`
		}
		writeCompleted(w, textOutput(answer))
		return
	}
	writeCompleted(w, textOutput("ok"))
}

func TestResponsesCompatibility(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fakeResponses(t, w, r) }))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/", Key: "sk-test", HTTP: srv.Client()}
	for _, tc := range []struct {
		name  string
		model string
		check func(context.Context, string) error
		want  string
	}{
		{"stream", "good", c.ResponseStream, ""},
		{"tool continuation", "good", c.ResponseToolLoop, ""},
		{"goal output", "good", c.GoalOutput, ""},
		{"buffered response", "buffered", c.ResponseStream, "did not return an event stream"},
		{"missing call ID", "missing-call-id", c.ResponseToolLoop, "invalid get_weather"},
		{"ignored tool result", "ignored-tool", c.ResponseToolLoop, "did not use"},
		{"missing goal field", "missing-field", c.GoalOutput, "did not match"},
		{"extra goal field", "extra-field", c.GoalOutput, "did not match"},
		{"null goal field", "null-field", c.GoalOutput, "did not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.check(context.Background(), tc.model)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResponsesStreamFailures(t *testing.T) {
	for _, tc := range []struct{ name, stream, want string }{
		{"truncated", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n", "closed before response.completed"},
		{"done without completion", "data: [DONE]\n\n", "ended before response.completed"},
		{"bad JSON", "data: {\n\n", "decode Responses event"},
		{"empty completion", "data: {\"type\":\"response.completed\"}\n\n", "has no response"},
		{"incomplete", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", "max_output_tokens"},
		{"failed", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"provider down\"}}}\n\n", "provider down"},
		{"stream error", "data: {\"type\":\"error\",\"message\":\"rate limited\"}\n\n", "rate limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readResponseStream(strings.NewReader(tc.stream))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResponsesMultilineEvents(t *testing.T) {
	stream := ": keepalive\r\n\r\nevent: response.completed\r\ndata: {\"type\":\"response.completed\",\r\ndata: \"response\":{\"status\":\"completed\",\"output\":[]}}\r\n\r\n"
	resp, err := readResponseStream(strings.NewReader(stream))
	if err != nil || resp.Status != "completed" {
		t.Fatalf("multiline stream = %+v, %v", resp, err)
	}
	if _, err := resp.text(); err == nil {
		t.Fatal("empty output passed the text check")
	}
}
