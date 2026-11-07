package jobs

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
)

func TestAdaptiveEscalatesTestsAndRetainsTierAfterRestart(t *testing.T) {
	r, repo := setupStrategy(t, "goal_api_adaptive")
	var mu sync.Mutex
	var calls []string
	strategyAPI(t, r, func(w http.ResponseWriter, model string) {
		mu.Lock()
		calls = append(calls, model)
		mu.Unlock()
		successReply(w)
	})
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish all requirements", Goal: true, Strategy: config.StrategyAdaptive, ReviewerModel: "auto", EscalateAfter: 2, MaxIterations: 2})
	if j.Status != Blocked || j.Goal.Adaptive.Level != 1 || j.Goal.Adaptive.Escalations != 1 || j.TestResult != "failed" {
		t.Fatalf("goal %+v, adaptive %+v", j, j.Goal.Adaptive)
	}
	r.Shutdown(context.Background(), "restart")
	r.Store.Close()
	next := reopenGoalRunner(t, r, "goal_api_adaptive")
	limit := 0
	if _, err := next.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, MaxIterations: &limit}); err != nil {
		t.Fatal(err)
	}
	done := waitGoal(t, next, j.ID)
	if done.Status != Done || done.Goal.Iterations != 3 || done.Goal.Adaptive.Level != 1 || done.Goal.ModelSelection.ActiveModel != "openai/gpt-b" || done.Goal.ReviewerSelection.ActiveModel != "anthropic/claude" {
		t.Fatalf("done %+v, progress %+v", done, done.Goal)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(calls, ","); got != "smart,smart,smart~1,smart~2" {
		t.Fatalf("calls %s", got)
	}
	handoff := run(t, repo, "git", "show", j.Branch+":handoff.txt")
	if !strings.Contains(handoff, "Latest validation result: failed") || !strings.Contains(handoff, "repeated failed test cycles") {
		t.Fatalf("missing escalation context: %s", handoff)
	}
}

func TestAdaptiveEscalatesRejectedReviews(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api_review")
	repo := r.Config.Repos["demo"]
	repo.Test = "test -f hello.txt"
	r.Config.Repos["demo"] = repo
	strategyAPI(t, r, func(w http.ResponseWriter, _ string) { successReply(w) })
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Meet every acceptance criterion", Goal: true, Strategy: config.StrategyAdaptive, ReviewerModel: "auto", EscalateAfter: 2, MaxIterations: 4})
	if j.Status != Done || j.Goal.Iterations != 3 || j.Goal.Adaptive.Level != 1 || !strings.Contains(j.Goal.Adaptive.Reason, "review rejections") {
		t.Fatalf("goal %+v, adaptive %+v", j, j.Goal.Adaptive)
	}
}

func TestAdaptiveBlocksAtStrongestModelAndOnUnchangedWork(t *testing.T) {
	for _, mode := range []string{"goal_api_stuck", "goal_api_noop"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := setupStrategy(t, mode)
			if mode == "goal_api_noop" {
				repo := r.Config.Repos["demo"]
				repo.Test = "true"
				r.Config.Repos["demo"] = repo
			}
			strategyAPI(t, r, func(w http.ResponseWriter, _ string) { successReply(w) })
			j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, Strategy: config.StrategyAdaptive, EscalateAfter: 1, MaxIterations: 5})
			if j.Status != Blocked || !strings.Contains(j.Error, "strongest configured model also stalled") || j.Goal.Iterations != 3 || j.Goal.Adaptive.Level != 2 {
				t.Fatalf("goal %+v, adaptive %+v", j, j.Goal.Adaptive)
			}
		})
	}
}

func TestAdaptiveAvailabilityAdvanceDoesNotDowngrade(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api_continue")
	routes, _ := r.Config.StrategyModels("smart", config.StrategyAdaptive)
	if err := r.recordModelFailure(context.Background(), routes[0], &modelpolicy.Failure{Kind: modelpolicy.RateLimit, RetryAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	strategyAPI(t, r, func(w http.ResponseWriter, model string) {
		if model == "smart" {
			t.Error("downgraded to baseline")
		}
		successReply(w)
	})
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, Strategy: config.StrategyAdaptive, MaxIterations: 1})
	if j.Goal.Adaptive.Level != 1 {
		t.Fatalf("tier = %+v", j.Goal.Adaptive)
	}
	limit := 2
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, MaxIterations: &limit, RetryUnavailable: true}); err != nil {
		t.Fatal(err)
	}
	j = waitGoal(t, r, j.ID)
	if j.Goal.ModelSelection.ActiveModel != "openai/gpt-b" || j.Goal.Adaptive.Level != 1 {
		t.Fatalf("tier lost: %+v", j.Goal)
	}
}

func TestNewAdaptiveTierClearsPreviousExecutionFailures(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	j := &Job{ID: "tier-test", Model: "smart", Status: Running, RunToken: "owner", CreatedAt: time.Now(), Goal: &GoalProgress{
		Phase: goalWorking, Failures: 1, Adaptive: &AdaptiveProgress{EscalateAfter: 2}, ModelSelection: &ModelSelection{Strategy: config.StrategyAdaptive},
	}}
	if err := r.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	routes, _ := r.Config.StrategyModels("smart", config.StrategyAdaptive)
	if err := r.recordModelFailure(context.Background(), routes[0], &modelpolicy.Failure{Kind: modelpolicy.RateLimit, RetryAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.selectGoalModel(context.Background(), j, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if j.Goal.Adaptive.Level != 1 || j.Goal.Failures != 0 {
		t.Fatalf("new tier inherited failures: %+v", j.Goal)
	}
	j.Goal.Failures = 2
	if err := r.escalateGoal(j, "repeated execution failures", io.Discard); err != nil {
		t.Fatal(err)
	}
	if j.Goal.Adaptive.Level != 2 || j.Goal.Failures != 0 {
		t.Fatalf("escalation inherited failures: %+v", j.Goal)
	}
}

func TestReviewerFallbackNeverUsesImplementer(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	var mu sync.Mutex
	var calls []string
	strategyAPI(t, r, func(w http.ResponseWriter, model string) {
		mu.Lock()
		calls = append(calls, model)
		mu.Unlock()
		if model == "smart~2" {
			quotaReply(w)
		} else {
			successReply(w)
		}
	})
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, ReviewerModel: "auto"})
	if j.Status != Done || j.Goal.ModelSelection.ActiveModel != "openai/gpt-a" || j.Goal.ReviewerSelection.ActiveModel != "openai/gpt-b" || j.Goal.ReviewerSelection.Switches != 1 {
		t.Fatalf("goal %+v, progress %+v", j, j.Goal)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(calls, ","); got != "smart,smart~2,smart~1" {
		t.Fatalf("review calls %s", got)
	}
	prompt := goalPrompt(j, "test command", true)
	if !strings.Contains(prompt, "Current model: openai/gpt-b") || !strings.Contains(prompt, "Implementation model: openai/gpt-a") {
		t.Fatalf("incorrect review context: %s", prompt)
	}
}
