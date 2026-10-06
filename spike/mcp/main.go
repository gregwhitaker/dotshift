// Command dotshift-mcp-spike is a throwaway MCP server for one question:
// will an OpenAI dot, through a custom ChatGPT developer-mode app, call a
// write tool (start_task) on a remote MCP server?
//
// It does no real work. start_task records the request to a JSONL file and
// logs it loudly; the read-only tools exist as a baseline so a failed write
// call can be told apart from a connector that isn't working at all.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type task struct {
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Prompt    string    `json:"prompt"`
	Model     string    `json:"model,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Client    string    `json:"client,omitempty"`
}

type store struct {
	mu    sync.Mutex
	tasks map[string]*task
	order []string
	file  *os.File
}

func (s *store) add(t *task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
	s.order = append(s.order, t.ID)
	return json.NewEncoder(s.file).Encode(t)
}

func (s *store) get(id string) (*task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	return t, ok
}

func (s *store) list() []*task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*task, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.tasks[id])
	}
	return out
}

func newID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func ptr[T any](v T) *T { return &v }

type startTaskIn struct {
	Repo   string `json:"repo" jsonschema:"repository the coding task is for, e.g. gregwhitaker/dotshift"`
	Prompt string `json:"prompt" jsonschema:"what the coding agent should do, in plain language"`
	Model  string `json:"model,omitempty" jsonschema:"optional model alias: fast, smart or local"`
}

type startTaskOut struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type taskStatusIn struct {
	TaskID string `json:"task_id" jsonschema:"id returned by start_task"`
}

type listReposOut struct {
	Repos []string `json:"repos"`
}

type searchIn struct {
	Query string `json:"query" jsonschema:"text to look for in recorded tasks"`
}

type searchResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type searchOut struct {
	Results []searchResult `json:"results"`
}

type fetchIn struct {
	ID string `json:"id" jsonschema:"id from a search result"`
}

type fetchOut struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Text     string            `json:"text"`
	URL      string            `json:"url"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func clientName(req *mcp.CallToolRequest) string {
	if req.Session == nil {
		return ""
	}
	if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
		return p.ClientInfo.Name + "/" + p.ClientInfo.Version
	}
	return ""
}

func newServer(st *store, repos []string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "dotshift-spike", Version: "0.1.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "start_task",
		Title:       "Start a coding task",
		Description: "Hand a coding task to dotshift, which runs it locally with a coding agent and returns a task id. Use this whenever the user asks for code to be written or changed in one of their repositories.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Start a coding task",
			ReadOnlyHint:    false,
			DestructiveHint: ptr(false),
			IdempotentHint:  false,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in startTaskIn) (*mcp.CallToolResult, startTaskOut, error) {
		t := &task{
			ID:        newID("task_"),
			Repo:      in.Repo,
			Prompt:    in.Prompt,
			Model:     in.Model,
			Status:    "queued",
			CreatedAt: time.Now().UTC(),
			Client:    clientName(req),
		}
		if err := st.add(t); err != nil {
			return nil, startTaskOut{}, err
		}
		log.Printf("\n%s\n*** start_task CALLED: write tools work ***\n  id=%s repo=%q model=%q client=%q\n  prompt=%q\n%s",
			strings.Repeat("=", 60), t.ID, t.Repo, t.Model, t.Client, t.Prompt, strings.Repeat("=", 60))
		return nil, startTaskOut{
			TaskID:  t.ID,
			Status:  t.Status,
			Message: "Task recorded by the dotshift spike. No code will run; this only proves the hand-off works.",
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "task_status",
		Description: "Get the status of a task started with start_task.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in taskStatusIn) (*mcp.CallToolResult, *task, error) {
		log.Printf("task_status called: %s", in.TaskID)
		t, ok := st.get(in.TaskID)
		if !ok {
			return nil, nil, fmt.Errorf("no task %q", in.TaskID)
		}
		return nil, t, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_repos",
		Description: "List the repositories dotshift is allowed to work in.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listReposOut, error) {
		log.Printf("list_repos called (read tool works) client=%q", clientName(req))
		return nil, listReposOut{Repos: repos}, nil
	})

	// search and fetch follow the shape ChatGPT expects from read/fetch-only
	// connectors, so they show whether a limited plan exposes anything at all.
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "Search dotshift tasks by text. Returns ids usable with fetch.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
		log.Printf("search called: %q (read tool works) client=%q", in.Query, clientName(req))
		out := searchOut{Results: []searchResult{}}
		q := strings.ToLower(in.Query)
		for _, t := range st.list() {
			if q == "" || strings.Contains(strings.ToLower(t.Prompt+" "+t.Repo), q) {
				out.Results = append(out.Results, searchResult{ID: t.ID, Title: t.Repo + ": " + t.Prompt, URL: "dotshift://tasks/" + t.ID})
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "fetch",
		Description: "Fetch a dotshift task by id.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in fetchIn) (*mcp.CallToolResult, fetchOut, error) {
		log.Printf("fetch called: %q (read tool works)", in.ID)
		t, ok := st.get(in.ID)
		if !ok {
			return nil, fetchOut{}, fmt.Errorf("no task %q", in.ID)
		}
		return nil, fetchOut{
			ID: t.ID, Title: t.Repo + ": " + t.Prompt, Text: t.Prompt, URL: "dotshift://tasks/" + t.ID,
			Metadata: map[string]string{"status": t.Status, "repo": t.Repo, "created_at": t.CreatedAt.Format(time.RFC3339)},
		}, nil
	})

	return s
}

// logRequests prints every HTTP request so you can see whether ChatGPT is
// reaching the server at all, and which JSON-RPC methods it sends.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s ua=%q session=%q", r.Method, r.URL.Path, r.UserAgent(), r.Header.Get("Mcp-Session-Id"))
		next.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address (the tunnel points here)")
	secret := flag.String("secret", "", "path secret; the MCP endpoint is served at /<secret>/mcp (generated if empty)")
	tasksFile := flag.String("tasks", "spike-tasks.jsonl", "file that records every start_task call")
	reposFlag := flag.String("repos", "gregwhitaker/dotshift", "comma-separated repos reported by list_repos")
	flag.Parse()

	if *secret == "" {
		*secret = newID("")
	}
	f, err := os.OpenFile(*tasksFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	st := &store{tasks: map[string]*task{}, file: f}
	srv := newServer(st, strings.Split(*reposFlag, ","))

	mux := http.NewServeMux()
	mux.Handle("/"+*secret+"/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })

	log.Printf("dotshift MCP spike listening on http://%s", *addr)
	log.Printf("MCP endpoint path: /%s/mcp", *secret)
	log.Printf("In ChatGPT, use https://<your-tunnel-host>/%s/mcp with no authentication", *secret)
	log.Fatal(http.ListenAndServe(*addr, logRequests(mux)))
}
