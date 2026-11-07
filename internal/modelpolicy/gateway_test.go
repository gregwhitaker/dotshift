package modelpolicy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func gatewayRequest(t *testing.T, gateway *Gateway, key, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, gateway.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data)
}

func TestGatewayPinsDeploymentPreservesStreamAndAuthenticates(t *testing.T) {
	const stream = "event: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{\"output_text\":\"ok\"}}\r\n\r\n"
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.URL.Path != "/v1/responses" || req.Header.Get("Authorization") != "Bearer master-secret" || req.Header.Get("X-Litellm-Num-Retries") != "0" {
			t.Errorf("unexpected request path or credentials")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["model"]) != `"smart~1"` || string(body["disable_fallbacks"]) != "true" || string(body["num_retries"]) != "0" || body["fallbacks"] != nil {
			t.Errorf("routing was not pinned: %s", body)
		}
		if string(body["input"]) != `[{"type":"reasoning","encrypted_content":"opaque"},{"type":"function_call_output","call_id":"call-1","output":"ok"}]` {
			t.Errorf("history changed: %s", body["input"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, stream)
	}))
	defer upstream.Close()
	gateway, err := StartGateway(upstream.URL+"/v1", "master-secret", "smart~1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if status, _ := gatewayRequest(t, gateway, "wrong", "/responses", `{}`); status != 401 {
		t.Fatal(status)
	}
	if status, _ := gatewayRequest(t, gateway, gateway.Key, "/chat/completions", `{}`); status != 404 {
		t.Fatal(status)
	}
	if status, _ := gatewayRequest(t, gateway, gateway.Key, "/responses", `null`); status != 400 {
		t.Fatal(status)
	}
	body := `{"model":"anything","disable_fallbacks":false,"num_retries":9,"fallbacks":["other"],"input":[{"type":"reasoning","encrypted_content":"opaque"},{"type":"function_call_output","call_id":"call-1","output":"ok"}],"stream":true}`
	status, got := gatewayRequest(t, gateway, gateway.Key, "/responses", body)
	if status != 200 || got != stream || calls.Load() != 1 || gateway.Failure() != nil {
		t.Fatalf("status %d, stream %q, calls %d, failure %+v", status, got, calls.Load(), gateway.Failure())
	}
}

func TestGatewayObservesHTTPAndStreamingFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType, kind string
		status                        int
	}{
		{"HTTP quota", `{"error":{"code":"insufficient_quota"}}`, "application/json", Quota, 429},
		{"stream quota", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"insufficient_quota\"}}}\n\n", "text/event-stream", Quota, 200},
		{"flat stream rate", "data: {\"type\":\"error\",\"code\":\"rate_limit_exceeded\"}\n\n", "text/event-stream", RateLimit, 200},
		{"stream overload", "event: error\r\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\r\n\r\n", "text/event-stream", Unavailable, 200},
		{"context", `{"error":{"code":"context_length_exceeded"}}`, "application/json", "", 400},
		{"auth", `{"error":{"code":"invalid_api_key"}}`, "application/json", "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stopped, calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			gateway, err := StartGateway(upstream.URL+"/v1", "master", "smart", func() { stopped.Add(1) })
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			gatewayRequest(t, gateway, gateway.Key, "/responses", `{}`)
			failure := gateway.Failure()
			if tc.kind == "" {
				if failure != nil || stopped.Load() != 0 {
					t.Fatalf("unexpected switch: %+v", failure)
				}
				return
			}
			if failure == nil || failure.Kind != tc.kind || stopped.Load() != 1 {
				t.Fatalf("failure = %+v, stops = %d", failure, stopped.Load())
			}
			// A client racing cancellation must not keep calling the provider.
			gatewayRequest(t, gateway, gateway.Key, "/responses", `{}`)
			if calls.Load() != 1 || stopped.Load() != 1 {
				t.Fatal("retried an ended attempt")
			}
		})
	}
}

func TestSSEObserverHandlesFragmentsAndBoundsMemory(t *testing.T) {
	stream := ": keepalive\r\n\r\ndata: " + strings.Repeat("x", (1<<20)+20) + "\n\nevent: error\r\ndata: {\"type\":\"error\",\r\ndata: \"code\":\"rate_limit_exceeded\"}\r\n\r\n"
	var observed [][]byte
	reader := &eventReader{ReadCloser: io.NopCloser(strings.NewReader(stream)), observe: func(event []byte) { observed = append(observed, bytes.Clone(event)) }}
	var output bytes.Buffer
	buffer := make([]byte, 7)
	for {
		n, err := reader.Read(buffer)
		output.Write(buffer[:n])
		if len(reader.buffer) > 1<<20 {
			t.Fatal("unbounded event buffer")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if output.String() != stream || len(observed) != 2 || !bytes.Contains(observed[1], []byte("rate_limit_exceeded")) {
		t.Fatalf("changed stream or lost later event, events = %d", len(observed))
	}
}
