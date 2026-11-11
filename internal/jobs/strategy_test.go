package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
)

func fakeAPIGoal(mode, prompt, summary string) int {
	review := os.Getenv("FAKE_REVIEW") == "true"
	if !review && mode != "goal_api_noop" {
		// Model failures can arrive after a tool has already changed files.
		if _, err := os.Stat("partial.txt"); os.IsNotExist(err) {
			os.WriteFile("partial.txt", []byte("partial work from "+os.Getenv("FAKE_MODEL")), 0o644)
		}
		os.WriteFile("handoff.txt", []byte(prompt), 0o644)
	}
	calls := 1
	if mode == "goal_api_twice" {
		calls = 2
	}
	for range calls {
		req, err := http.NewRequest(http.MethodPost, os.Getenv("FAKE_API_BASE")+"/responses", strings.NewReader(`{"model":"must-be-overridden","stream":true,"input":"test"}`))
		if err != nil {
			return 30
		}
		req.Header.Set("Authorization", "Bearer "+os.Getenv(APIKeyEnv))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 31
		}
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			return 32
		}
	}
	if !review && mode != "goal_api_noop" {
		value := "fixed\n"
		if mode == "goal_api_stuck" || ((mode == "goal_api_adaptive" || mode == "goal_api_review") && os.Getenv("FAKE_MODEL") == "smart") {
			value = "broken\n"
		}
		os.WriteFile("hello.txt", []byte(value), 0o644)
	}
	report := GoalReport{Status: "complete", Summary: "Implemented the goal and retained partial changes."}
	if (mode == "goal_api_continue" || mode == "goal_api_noop") && !review {
		report.Status, report.NextSteps = "continue", "Finish the next increment."
	}
	if mode == "goal_api_review" && review {
		data, _ := os.ReadFile("hello.txt")
		if string(data) != "fixed\n" {
			report = GoalReport{Status: "continue", Summary: "Acceptance criterion missing.", NextSteps: "Fix the unmet acceptance criterion."}
		}
	}
	data, _ := json.Marshal(report)
	os.WriteFile(summary, data, 0o644)
	return 0
}

func setupStrategy(t *testing.T, mode string) (*Runner, string) {
	t.Helper()
	r, repo := setup(t, mode, `test "$(cat hello.txt)" = fixed`)
	r.Config.Providers["anthropic"] = config.Provider{}
	r.Config.Aliases["smart"] = []string{"openai/gpt-a", "openai/gpt-b", "anthropic/claude"}
	r.Config.Aliases["other"] = []string{"anthropic/claude", "openai/gpt-a"}
	r.Config.Jobs.Strategy = config.StrategyPreferOpenAI
	return r, repo
}

func strategyAPI(t *testing.T, r *Runner, handle func(http.ResponseWriter, string)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Model            string
			DisableFallbacks bool `json:"disable_fallbacks"`
			NumRetries       int  `json:"num_retries"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if req.URL.Path != "/v1/responses" || req.Header.Get("Authorization") != "Bearer sk-test" || !body.DisableFallbacks || body.NumRetries != 0 {
			t.Errorf("unpinned or unauthenticated provider request: %+v", body)
		}
		handle(w, body.Model)
	}))
	t.Cleanup(server.Close)
	r.BaseURL = server.URL + "/v1"
	return server
}

func quotaReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	io.WriteString(w, `{"error":{"code":"insufficient_quota"}}`)
}

func successReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
}

func TestPreferOpenAIQuotaSwitchPreservesWorkAndSurvivesRestart(t *testing.T) {
	r, repo := setupStrategy(t, "goal_api")
	var mu sync.Mutex
	calls := map[string]int{}
	strategyAPI(t, r, func(w http.ResponseWriter, model string) {
		mu.Lock()
		calls[model]++
		mu.Unlock()
		if model == "smart" {
			quotaReply(w)
		} else {
			successReply(w)
		}
	})
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Complete all acceptance criteria", Goal: true, MaxIterations: 1})
	if j.Status != Done || j.TestResult != "passed" || j.Goal.Iterations != 1 || j.Goal.Failures != 0 || j.Goal.ModelSelection.ActiveModel != "anthropic/claude" || j.Goal.ModelSelection.Switches != 1 {
		t.Fatalf("goal = %+v, progress = %+v, model = %+v", j, j.Goal, j.Goal.ModelSelection)
	}
	if got := run(t, repo, "git", "show", j.Branch+":partial.txt"); got != "partial work from smart" {
		t.Fatalf("partial work lost: %q", got)
	}
	handoff := run(t, repo, "git", "show", j.Branch+":handoff.txt")
	if !strings.Contains(handoff, "Complete all acceptance criteria") || !strings.Contains(handoff, "quota_exhausted") {
		t.Fatal("model switch lost objective or failure context")
	}
	if got := run(t, repo, "git", "status", "--porcelain"); got != "" {
		t.Fatalf("changed main worktree: %s", got)
	}
	r.Shutdown(context.Background(), "restart")
	r.Store.Close()
	next := reopenGoalRunner(t, r, "goal_api")
	second := submitAndWait(t, next, Request{Repo: "demo", Prompt: "A second goal", Goal: true})
	if second.Status != Done || second.Goal.ModelSelection.ActiveModel != "anthropic/claude" || second.Goal.ModelSelection.Switches != 0 {
		t.Fatalf("restart lost provider availability: %+v", second)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["smart"] != 1 || calls["smart~1"] != 0 || calls["smart~2"] != 4 {
		t.Fatalf("calls = %v; exhausted provider should be skipped across models and goals", calls)
	}
	catalog, err := next.Strategies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, pool := range catalog.Strategies[1].Aliases {
		for _, model := range pool.Models {
			if model.Provider == "openai" && (model.Available || model.Reason != modelpolicy.Quota || model.RetryAt != nil) {
				t.Fatalf("availability not shared across aliases: %+v", model)
			}
		}
	}
}

func TestAllCreditsExhaustedBlocksUntilExplicitRetry(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	var restored atomic.Bool
	var calls atomic.Int32
	strategyAPI(t, r, func(w http.ResponseWriter, model string) {
		calls.Add(1)
		if restored.Load() {
			successReply(w)
		} else {
			quotaReply(w)
		}
	})
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true})
	if j.Status != Blocked || j.Worktree == "" || calls.Load() != 2 || !strings.Contains(j.Error, "retry_unavailable") {
		t.Fatalf("goal = %+v, calls = %d", j, calls.Load())
	}
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID}); err != nil {
		t.Fatal(err)
	}
	if again := waitGoal(t, r, j.ID); again.Status != Blocked || calls.Load() != 2 {
		t.Fatal("ordinary resume retried exhausted credits")
	}
	restored.Store(true)
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, RetryUnavailable: true}); err != nil {
		t.Fatal(err)
	}
	done := waitGoal(t, r, j.ID)
	if done.Status != Done || done.Branch != j.Branch || done.Goal.ModelSelection.ActiveModel != "openai/gpt-a" || calls.Load() != 4 {
		t.Fatalf("retry = %+v, calls = %d", done, calls.Load())
	}
}

func TestShortRateLimitWaitsAndRecoversWithoutSwitchingProvider(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	var calls atomic.Int32
	strategyAPI(t, r, func(w http.ResponseWriter, model string) {
		if model != "smart" {
			t.Errorf("short throttle switched to %s", model)
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
		} else {
			successReply(w)
		}
	})
	start := time.Now()
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true})
	if j.Status != Done || j.Goal.ModelSelection.ActiveModel != "openai/gpt-a" || j.Goal.ModelSelection.Switches != 0 || calls.Load() != 3 || time.Since(start) < time.Second {
		t.Fatalf("rate limit outcome = %+v, calls = %d", j, calls.Load())
	}
}

func TestCooldownWaitSurvivesRestartAndCanBeCancelledElsewhere(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	routes, _ := r.Config.StrategyModels("smart", config.StrategyPreferOpenAI)
	retry := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	for _, route := range routes {
		if err := r.recordModelFailure(context.Background(), route, &modelpolicy.Failure{Kind: modelpolicy.RateLimit, RetryAt: retry}); err != nil {
			t.Fatal(err)
		}
	}
	j, err := r.Submit(context.Background(), Request{Repo: "demo", Prompt: "Finish later", Goal: true})
	if err != nil {
		t.Fatal(err)
	}
	waitForModelCooldown(t, r, j.ID)
	r.Shutdown(context.Background(), "restart")
	r.Store.Close()
	next := reopenGoalRunner(t, r, "goal_api")
	if err := next.ResumeGoals(context.Background()); err != nil {
		t.Fatal(err)
	}
	waiting := waitForModelCooldown(t, next, j.ID)
	if !waiting.Goal.ModelSelection.WaitingUntil.Equal(retry) || waiting.RunToken == j.RunToken {
		t.Fatalf("lost cooldown checkpoint: %+v", waiting.Goal.ModelSelection)
	}
	other := reopenGoalRunner(t, next, "goal_api")
	if err := other.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	done := waitGoal(t, next, j.ID)
	if done.Status != Cancelled || done.RunnerPID != 0 || done.AgentPID != 0 {
		t.Fatalf("cancelled wait = %+v", done)
	}
}

func waitForModelCooldown(t *testing.T, r *Runner, id string) *Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := r.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == Running && j.Goal.ModelSelection.WaitingUntil != nil {
			return j
		}
		if j.Status.Terminal() {
			t.Fatalf("stopped before cooldown: %+v", j)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("never checkpointed cooldown")
	return nil
}

func TestReviewKeepsModelThenNextIterationPrefersRecoveredOpenAI(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api_continue")
	strategyAPI(t, r, func(w http.ResponseWriter, model string) { successReply(w) })
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, MaxIterations: 1})
	j.Goal.ModelSelection.ActiveModel, j.Goal.ModelSelection.ActiveRoute = "anthropic/claude", "smart~2"
	review, err := r.selectGoalModel(context.Background(), j, true, io.Discard)
	if err != nil || review.Provider != "anthropic" {
		t.Fatalf("review choice = %+v, %v", review, err)
	}
	work, err := r.selectGoalModel(context.Background(), j, false, io.Discard)
	if err != nil || work.Provider != "openai" || j.Goal.ModelSelection.Switches != 1 {
		t.Fatalf("recovery choice = %+v, %v", work, err)
	}
}

func TestStrategyValidationBeforeCreatingWorktree(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	for _, request := range []Request{
		{Repo: "demo", Prompt: "x", Goal: true, Strategy: "imaginary"},
		{Repo: "demo", Prompt: "x", Goal: true, Model: "fast", Strategy: config.StrategyPreferOpenAI},
		{Repo: "demo", Prompt: "x", Strategy: "imaginary"},
	} {
		if _, err := r.Submit(context.Background(), request); err == nil {
			t.Fatalf("accepted %+v", request)
		}
	}
	files, _ := filepath.Glob(filepath.Join(r.StateDir, "worktrees", "*"))
	if len(files) != 0 {
		t.Fatal(fmt.Sprint(files))
	}
}
