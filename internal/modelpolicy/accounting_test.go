package modelpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type receiptAccountant struct {
	mu                             sync.Mutex
	beginError                     error
	known                          bool
	cost                           float64
	input, output, begun, finished int
}

func (a *receiptAccountant) Begin(context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.begun++
	return "request-1", a.beginError
}
func (a *receiptAccountant) Record(id string, cost float64, input, output int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id != "request-1" {
		return errors.New("wrong correlation ID")
	}
	a.known, a.cost, a.input, a.output = true, max(a.cost, cost), input, output
	return nil
}
func (a *receiptAccountant) Finish(context.Context, string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finished++
	if !a.known {
		return errors.New("no receipt")
	}
	return nil
}

func TestGatewayAccountsTerminalCostsAndKeepsUnknownCostsPending(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, header, body, path string
		status                                int
		known                                 bool
		cost                                  float64
	}{
		{"stream final", "text/event-stream", "0", "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"cost\":0.25,\"input_tokens\":10,\"output_tokens\":20}}}\n\n", "/responses", 200, true, .25},
		{"stream missing", "text/event-stream", "0", "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n", "/responses", 200, false, 0},
		{"stream explicit zero", "text/event-stream", "", "data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"cost\":0}}}\n\n", "/responses", 200, true, 0},
		{"stream premature event", "text/event-stream", "", "data: {\"type\":\"response.created\",\"response\":{\"usage\":{\"cost\":0}}}\n\n", "/responses", 200, false, 0},
		{"compact header", "application/json", "0.2", `{"usage":{"input_tokens":3}}`, "/responses/compact", 200, true, .2},
		{"compact body", "application/json", "", `{"usage":{"cost":0.1,"input_tokens":3}}`, "/responses/compact", 200, true, .1},
		{"body null", "application/json", "", `{"usage":{"cost":null}}`, "/responses", 200, false, 0},
		{"body negative", "application/json", "", `{"usage":{"cost":-1}}`, "/responses", 200, false, 0},
		{"invalid header", "application/json", "NaN", `{}`, "/responses", 200, false, 0},
		{"rejection", "application/json", "", `{"error":{"code":"insufficient_quota"}}`, "/responses", 429, true, 0},
		{"server failure unknown", "application/json", "", `{"error":{"code":"server_error"}}`, "/responses", 500, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &receiptAccountant{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body["metadata"]) != `{"user":"value"}` || !strings.Contains(string(body["litellm_metadata"]), "request-1") || !strings.Contains(req.Header.Get("X-Litellm-Spend-Logs-Metadata"), "request-1") {
					t.Error("receipt correlation or provider metadata lost")
				}
				w.Header().Set("Content-Type", tc.contentType)
				if tc.header != "" {
					w.Header().Set("X-Litellm-Response-Cost", tc.header)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			gateway, err := StartGateway(upstream.URL+"/v1", "master", "route", nil, GatewayOptions{Accountant: a})
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			status, data := gatewayRequest(t, gateway, gateway.Key, tc.path, `{"metadata":{"user":"value"}}`)
			gateway.Close()
			if status != tc.status || data != tc.body {
				t.Fatal("accounting altered response")
			}
			if a.known != tc.known || a.cost != tc.cost || a.begun != 1 {
				t.Fatalf("receipt known=%v cost=%v begun=%d", a.known, a.cost, a.begun)
			}
			if (gateway.AccountingError() != nil) == tc.known {
				t.Fatalf("accounting error %v", gateway.AccountingError())
			}
		})
	}
}

func TestGatewayStopsBeforeForwardingRejectedBudgetAdmission(t *testing.T) {
	var calls, stopped atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	a := &receiptAccountant{beginError: errors.New("cap reached")}
	gateway, err := StartGateway(upstream.URL+"/v1", "master", "route", func() { stopped.Add(1) }, GatewayOptions{Accountant: a})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if status, _ := gatewayRequest(t, gateway, "wrong", "/responses", `{}`); status != 401 {
		t.Fatal(status)
	}
	if status, _ := gatewayRequest(t, gateway, gateway.Key, "/responses", `{}`); status != 402 {
		t.Fatal(status)
	}
	if status, _ := gatewayRequest(t, gateway, gateway.Key, "/responses", `{}`); status != 503 {
		t.Fatal(status)
	}
	gateway.Close()
	if calls.Load() != 0 || stopped.Load() != 1 || a.begun != 1 {
		t.Fatal("rejected request reached provider or was billed")
	}
}
