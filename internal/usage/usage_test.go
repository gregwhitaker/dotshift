package usage

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A trimmed StandardLoggingPayload batch as LiteLLM's generic_api callback
// sends it: a primary that failed, then the fallback that served the call.
const batch = `[
 {"model_group":"smart","model":"anthropic/claude-sonnet-5-5","status":"failure","prompt_tokens":0,"completion_tokens":0,
  "response_cost":0.0,"startTime":1791516067.5,"error_str":"litellm.RateLimitError: slow down\nmore detail",
  "messages":[{"role":"user","content":"redacted-by-litellm"}]},
 {"model_group":"smart~1","model":"openai/gpt-5","status":"success","prompt_tokens":1200,"completion_tokens":300,
  "response_cost":0.0045,"startTime":1791516068.0,"error_str":null}
]`

func TestDecode(t *testing.T) {
	got, err := Decode([]byte(batch))
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{Time: time.Unix(1791516067, 5e8).UTC(), Alias: "smart", Route: "smart", Model: "anthropic/claude-sonnet-5-5", CostKnown: true, Error: "litellm.RateLimitError: slow down"},
		{Time: time.Unix(1791516068, 0).UTC(), Alias: "smart", Route: "smart~1", Model: "openai/gpt-5", OK: true, InputTokens: 1200, OutputTokens: 300, Cost: 0.0045, CostKnown: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decode =\n%+v\nwant\n%+v", got, want)
	}

	nd, err := Decode([]byte(`{"model_group":"fast","model":"gemini/x","status":"success"}` + "\n" + `{"model_group":"fast","model":"gemini/x","status":"success"}`))
	if err != nil || len(nd) != 2 {
		t.Errorf("ndjson Decode = %v, %v", nd, err)
	}
}

func TestCostReceiptMustBePresentAndFinal(t *testing.T) {
	for _, tc := range []struct {
		fields string
		known  bool
		cost   float64
	}{
		{``, false, 0}, {`,"response_cost":null`, false, 0},
		{`,"response_cost":-1`, false, 0}, {`,"response_cost":0`, true, 0},
		{`,"response_cost":0.25,"cost_finalized":false`, false, 0},
		{`,"response_cost":0.25,"cost_finalized":true`, true, 0.25},
	} {
		records, err := Decode([]byte(`{"model":"openai/test","metadata":{"spend_logs_metadata":{"dotshift_request_id":"receipt-1"}}` + tc.fields + `}`))
		if err != nil || len(records) != 1 {
			t.Fatalf("decode: %v, %v", records, err)
		}
		if r := records[0]; r.CostKnown != tc.known || r.Cost != tc.cost || r.RequestID != "receipt-1" {
			t.Fatalf("%s: %+v", tc.fields, r)
		}
	}
}

func TestReceiverAppendsAndSummarizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), File)
	srv := httptest.NewServer(&Receiver{Path: path, Token: "tok"})
	defer srv.Close()

	post := func(token string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(batch))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d", code)
	}
	for range 2 {
		if code := post("tok"); code != http.StatusNoContent {
			t.Fatalf("status %d", code)
		}
	}

	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("loaded %d records, want 4", len(records))
	}
	rows := Summarize(records, time.Time{})
	want := []Row{
		{Alias: "smart", Model: "anthropic/claude-sonnet-5-5", Requests: 2, Failures: 2},
		{Alias: "smart", Model: "openai/gpt-5", Requests: 2, InputTokens: 2400, OutputTokens: 600, Cost: 0.009},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("Summarize =\n%+v\nwant\n%+v", rows, want)
	}
	if rows := Summarize(records, time.Unix(1791516068, 0)); len(rows) != 1 || rows[0].Model != "openai/gpt-5" {
		t.Errorf("Summarize since = %+v", rows)
	}
}

func TestLoadMissingFile(t *testing.T) {
	records, err := Load(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil || records != nil {
		t.Errorf("Load = %v, %v", records, err)
	}
}

func TestParsePeriod(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "24h": 24 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := ParsePeriod(in); err != nil || got != want {
			t.Errorf("ParsePeriod(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "d", "-1d", "soon", "0h"} {
		if _, err := ParsePeriod(bad); err == nil {
			t.Errorf("ParsePeriod(%q) succeeded", bad)
		}
	}
}
