package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/jobs"
)

type fakeService struct {
	job       *jobs.Job
	resumed   *jobs.ResumeGoalRequest
	cancelled string
}

func (f *fakeService) Strategies(context.Context) (*jobs.StrategyCatalog, error) {
	return &jobs.StrategyCatalog{ExecutionMode: "subscription", DefaultStrategy: "alias", Strategies: []jobs.StrategyInfo{}}, nil
}

func (f *fakeService) List(context.Context) ([]*jobs.Job, error) { return []*jobs.Job{f.job}, nil }
func (f *fakeService) Get(_ context.Context, id string) (*jobs.Job, error) {
	if id != f.job.ID {
		return nil, jobs.ErrNotFound
	}
	return f.job, nil
}
func (f *fakeService) Cancel(_ context.Context, id string) error { f.cancelled = id; return nil }
func (f *fakeService) ResumeGoal(_ context.Context, r jobs.ResumeGoalRequest) (*jobs.Job, error) {
	f.resumed = &r
	return f.job, nil
}

func testHandler(t *testing.T) (http.Handler, *fakeService, Record) {
	t.Helper()
	f := &fakeService{job: &jobs.Job{ID: "goal-1", Repo: "demo", Prompt: "Build the search", Status: jobs.Blocked, RunToken: "private-run-token", RunnerPID: 123, AgentPID: 456, CreatedAt: time.Now(), Goal: &jobs.GoalProgress{Phase: "working", Iterations: 2, MaxIterations: 2}}}
	r := Record{URL: "http://127.0.0.1:8123", Token: strings.Repeat("a", 64), Instance: "abc", ConfigPath: "/tmp/config.yaml"}
	return newHandler(Options{Jobs: f, ExecutionMode: "subscription"}, r), f, r
}

func request(handler http.Handler, record Record, method, path, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, record.URL+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+record.Token)
	r.Header.Set("Content-Type", "application/json")
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestLocalBrowserBoundary(t *testing.T) {
	h, _, r := testHandler(t)
	for _, test := range []struct {
		name   string
		change func(*http.Request)
		code   int
	}{
		{"authenticated", func(*http.Request) {}, 200},
		{"no token", func(r *http.Request) { r.Header.Del("Authorization") }, 401},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, 401},
		{"rebinding host", func(r *http.Request) { r.Host = "attacker.example:8123" }, 403},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"foreign site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"forwarded tunnel", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "1.2.3.4") }, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := request(h, r, "GET", "/api/jobs", "", test.change)
			if w.Code != test.code {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
		})
	}
	w := request(h, r, "POST", "/api/session", "{}", nil)
	cookies := w.Result().Cookies()
	if w.Code != 200 || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("invalid browser session: %d", w.Code)
	}
	w = request(h, r, "GET", "/api/jobs", "", func(r *http.Request) { r.Header.Del("Authorization"); r.AddCookie(cookies[0]) })
	if w.Code != 200 {
		t.Fatalf("cookie session: %d", w.Code)
	}
	w = request(h, r, "POST", "/api/jobs/goal-1/resume", "{}", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") })
	if w.Code != 415 {
		t.Fatalf("accepted form-like request: %d", w.Code)
	}
}

func TestJobViewsAndActions(t *testing.T) {
	h, f, r := testHandler(t)
	w := request(h, r, "GET", "/api/jobs/goal-1", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-run-token") || strings.Contains(w.Body.String(), "RunnerPID") {
		t.Fatalf("invalid job projection: %s", w.Body.String())
	}
	w = request(h, r, "GET", "/api/jobs/missing", "", nil)
	if w.Code != 404 {
		t.Fatalf("missing job: %d", w.Code)
	}
	w = request(h, r, "POST", "/api/jobs/goal-1/resume", `{"input":"Fixed access","max_iterations":5,"retry_unavailable":true}`, nil)
	if w.Code != 202 || f.resumed == nil || f.resumed.Input != "Fixed access" || *f.resumed.MaxIterations != 5 || !f.resumed.RetryUnavailable {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"max_cost_usd":100}`, `{} {}`, `{"max_iterations":1.5}`} {
		w = request(h, r, "POST", "/api/jobs/goal-1/resume", body, nil)
		if w.Code != 400 {
			t.Fatalf("accepted invalid resume %s: %d", body, w.Code)
		}
	}
	f.job.Status = jobs.Done
	w = request(h, r, "POST", "/api/jobs/goal-1/resume", `{}`, nil)
	if w.Code != 409 {
		t.Fatalf("resumed completed work: %d", w.Code)
	}
	w = request(h, r, "POST", "/api/jobs/goal-1/cancel", `{}`, nil)
	if w.Code != 409 {
		t.Fatalf("cancelled completed work: %d", w.Code)
	}
	f.job.Status = jobs.Running
	w = request(h, r, "POST", "/api/jobs/goal-1/cancel", `{}`, nil)
	if w.Code != 202 || f.cancelled != "goal-1" {
		t.Fatalf("cancel: %d", w.Code)
	}
}

func TestLogTailAndEmbeddedRoutes(t *testing.T) {
	h, f, r := testHandler(t)
	f.job.LogPath = filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(f.job.LogPath, []byte(strings.Repeat("x", 70*1024)+"latest output"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := request(h, r, "GET", "/api/jobs/goal-1/logs", "", nil)
	var out struct {
		Output    string `json:"output"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || len(out.Output) > 64*1024 || !strings.HasSuffix(out.Output, "latest output") {
		t.Fatal("log tail lost newest output or exceeded its limit")
	}
	for _, path := range []string{"/", "/models", "/goals/goal-1"} {
		w = request(h, r, "GET", path, "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `id="root"`) {
			t.Fatalf("embedded route %s: %d", path, w.Code)
		}
	}
	w = request(h, r, "GET", "/missing.js", "", nil)
	if w.Code != 404 {
		t.Fatalf("missing asset: %d", w.Code)
	}
}

func TestDiscoveryVerifiesIdentityAndPrivateReceipt(t *testing.T) {
	_, f, _ := testHandler(t)
	srv, err := Start(Options{Jobs: f, ConfigPath: "/tmp/config.yaml", ExecutionMode: "subscription"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())
	dir := t.TempDir()
	if err := srv.Record.Save(dir); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(filepath.Join(dir, "service.json"))
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("receipt permissions: %v", stat.Mode())
	}
	record, err := Discover(context.Background(), dir, "/tmp/config.yaml")
	if err != nil || record == nil || record.Instance != srv.Record.Instance {
		t.Fatalf("discovery: %v", err)
	}
	if _, err := Discover(context.Background(), dir, "/tmp/other.yaml"); err == nil {
		t.Fatal("accepted another configuration")
	}
	res, err := record.Request(context.Background(), "GET", "/api/jobs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "Build the search") {
		t.Fatal("could not read live jobs")
	}
	bad := *record
	bad.Token = strings.Repeat("b", 64)
	if err := bad.Save(dir); err != nil {
		t.Fatal(err)
	}
	if stale, err := Discover(context.Background(), dir, "/tmp/config.yaml"); err != nil || stale != nil {
		t.Fatalf("accepted stale identity: %v", err)
	}
}
