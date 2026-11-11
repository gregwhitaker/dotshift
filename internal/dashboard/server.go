// Package dashboard serves the local browser UI and adapts the shared job runner.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/jobs"
	webassets "github.com/gregwhitaker/dotshift/web"
)

// Service is the runner functionality used by the local dashboard.
type Service interface {
	Strategies(context.Context) (*jobs.StrategyCatalog, error)
	List(context.Context) ([]*jobs.Job, error)
	Get(context.Context, string) (*jobs.Job, error)
	Cancel(context.Context, string) error
	ResumeGoal(context.Context, jobs.ResumeGoalRequest) (*jobs.Job, error)
}

// Options supplies the shared runner and service lifecycle to the dashboard.
type Options struct {
	Jobs          Service
	ConfigPath    string
	ExecutionMode string
	DefaultModel  string
	MCPEnabled    bool
	Stop          func()
}

// Server owns a loopback-only HTTP listener. It never uses the MCP tunnel.
type Server struct {
	Record Record
	http   *http.Server
}

// Start starts a dashboard on a free loopback port. The caller publishes Record
// only after the runner and other intakes are ready.
func Start(o Options) (*Server, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		ln.Close()
		return nil, err
	}
	token := hex.EncodeToString(key)
	record := Record{URL: "http://" + ln.Addr().String(), Token: token, Instance: token[:12], ConfigPath: o.ConfigPath, PID: os.Getpid(), MCPEnabled: o.MCPEnabled}
	srv := &Server{Record: record}
	srv.http = &http.Server{Handler: newHandler(o, record), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { _ = srv.http.Serve(ln) }()
	return srv, nil
}

// Close stops accepting requests and waits for in-flight requests to finish.
func (s *Server) Close(ctx context.Context) error { return s.http.Shutdown(ctx) }

type jobView struct {
	ID            string             `json:"id"`
	Repo          string             `json:"repo"`
	Prompt        string             `json:"prompt"`
	Model         string             `json:"model"`
	ExecutionMode string             `json:"execution_mode"`
	Branch        string             `json:"branch"`
	BaseCommit    string             `json:"base_commit"`
	Status        jobs.Status        `json:"status"`
	Error         string             `json:"error"`
	Summary       string             `json:"summary"`
	Commits       int                `json:"commits"`
	DiffStat      string             `json:"diffstat"`
	TestResult    string             `json:"test_result"`
	TestOutput    string             `json:"test_output,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	StartedAt     *time.Time         `json:"started_at"`
	FinishedAt    *time.Time         `json:"finished_at"`
	Goal          *jobs.GoalProgress `json:"goal"`
}

func describe(j *jobs.Job, detail bool) jobView {
	mode := j.ExecutionMode
	if mode == "" {
		mode = "api"
	}
	v := jobView{ID: j.ID, Repo: j.Repo, Prompt: j.Prompt, Model: j.Model, ExecutionMode: mode, Branch: j.Branch, BaseCommit: j.BaseCommit, Status: j.Status, Error: j.Error, Summary: j.Summary, Commits: j.Commits, DiffStat: j.DiffStat, TestResult: j.TestResult, CreatedAt: j.CreatedAt, Goal: j.Goal}
	if !j.StartedAt.IsZero() {
		v.StartedAt = &j.StartedAt
	}
	if !j.FinishedAt.IsZero() {
		v.FinishedAt = &j.FinishedAt
	}
	if detail {
		v.TestOutput = j.TestOutput
	}
	return v
}

func newHandler(o Options, record Record) http.Handler {
	started := time.Now().UTC()
	cookieName := "dotshift_" + record.Instance
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		catalog, err := o.Jobs.Strategies(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"default_model": o.DefaultModel, "strategies": catalog})
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"instance": record.Instance, "config_path": record.ConfigPath, "execution_mode": o.ExecutionMode, "mcp_enabled": o.MCPEnabled, "started_at": started})
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: record.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
		if o.Stop != nil {
			go o.Stop()
		}
	})
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		list, err := o.Jobs.List(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		out := make([]jobView, 0, len(list))
		for _, j := range list {
			out = append(out, describe(j, false))
		}
		writeJSON(w, http.StatusOK, out)
	})
	get := func(w http.ResponseWriter, r *http.Request) *jobs.Job {
		j, err := o.Jobs.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return nil
		}
		return j
	}
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j := get(w, r)
		if j == nil {
			return
		}
		writeJSON(w, http.StatusOK, describe(j, true))
	})
	mux.HandleFunc("GET /api/jobs/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		j := get(w, r)
		if j == nil {
			return
		}
		output, truncated, err := logTail(j.LogPath, 64*1024)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"output": output, "truncated": truncated})
	})
	mux.HandleFunc("GET /api/jobs/{id}/diff", func(w http.ResponseWriter, r *http.Request) {
		j := get(w, r)
		if j == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		patch, truncated, err := jobs.Diff(ctx, j, 256*1024)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"output": patch, "truncated": truncated})
	})
	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		j := get(w, r)
		if j == nil {
			return
		}
		if j.Status.Terminal() {
			writeError(w, http.StatusConflict, "This job has already stopped.")
			return
		}
		if err := o.Jobs.Cancel(r.Context(), j.ID); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/jobs/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		j := get(w, r)
		if j == nil {
			return
		}
		if j.Goal == nil || (j.Status != jobs.Blocked && j.Status != jobs.Cancelled && j.Status != jobs.Failed) {
			writeError(w, http.StatusConflict, "Only stopped, unfinished goals can be resumed.")
			return
		}
		var input struct {
			Input            string `json:"input"`
			MaxIterations    *int   `json:"max_iterations"`
			RetryUnavailable bool   `json:"retry_unavailable"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid resume request.")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeError(w, http.StatusBadRequest, "Expected one JSON object.")
			return
		}
		updated, err := o.Jobs.ResumeGoal(r.Context(), jobs.ResumeGoalRequest{ID: j.ID, Input: input.Input, MaxIterations: input.MaxIterations, RetryUnavailable: input.RetryUnavailable})
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, describe(updated, true))
	})
	assets := webassets.Files()
	// Old project-management links now lead to the work monitor.
	mux.HandleFunc("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" || name == "models" || strings.HasPrefix(name, "goals/") {
			name = "index.html"
		}
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasSuffix(name, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case strings.HasSuffix(name, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case strings.HasSuffix(name, ".html"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case strings.HasSuffix(name, ".svg"):
			w.Header().Set("Content-Type", "image/svg+xml")
		}
		_, _ = w.Write(data)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if "http://"+r.Host != record.URL || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-For") != "" {
			writeError(w, http.StatusForbidden, "The dashboard is only available on its local address.")
			return
		}
		if origin := r.Header.Get("Origin"); (origin != "" && origin != record.URL) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, http.StatusForbidden, "Cross-origin requests are not allowed.")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" && r.URL.Path != "/api/session" {
				if cookie, err := r.Cookie(cookieName); err == nil {
					token = cookie.Value
				}
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(record.Token)) != 1 {
				writeError(w, http.StatusUnauthorized, "Open this dashboard with dotshift ui to connect.")
				return
			}
			if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "Expected application/json.")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func logTail(path string, limit int64) (string, bool, error) {
	if path == "" {
		return "", false, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	truncated := info.Size() > limit
	if truncated {
		if _, err := f.Seek(-limit, io.SeekEnd); err != nil {
			return "", false, err
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	return strings.ToValidUTF8(string(data), "�"), truncated, err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "This job could not be found.")
		return
	}
	writeError(w, http.StatusInternalServerError, fmt.Sprintf("Could not load job data: %v", err))
}
