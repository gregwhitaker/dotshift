package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStartTaskOverStreamableHTTP(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "tasks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st := &store{tasks: map[string]*task{}, file: f}
	srv := newServer(st, []string{"gregwhitaker/dotshift"})

	mux := http.NewServeMux()
	mux.Handle("/s3cret/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/s3cret/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.Name == "start_task" && tool.Annotations.ReadOnlyHint {
			t.Error("start_task must not be marked read-only")
		}
	}
	if got := strings.Join(names, ","); !strings.Contains(got, "start_task") || !strings.Contains(got, "fetch") {
		t.Fatalf("tools = %s", got)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "start_task", Arguments: map[string]any{
		"repo": "gregwhitaker/dotshift", "prompt": "add a README badge",
	}})
	if err != nil || res.IsError {
		t.Fatalf("start_task: %v %+v", err, res)
	}
	var out startTaskOut
	b, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(b, &out)
	if out.TaskID == "" || len(st.list()) != 1 {
		t.Fatalf("task not recorded: %+v", out)
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "task_status", Arguments: map[string]any{"task_id": out.TaskID}})
	if err != nil || res.IsError {
		t.Fatalf("task_status: %v %+v", err, res)
	}

	// Wrong path must not expose the server.
	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unsecret path status = %d", resp.StatusCode)
	}
}
