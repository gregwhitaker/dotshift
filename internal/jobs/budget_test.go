package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	"github.com/gregwhitaker/dotshift/internal/usage"
)

func costReply(w http.ResponseWriter, cost float64) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Litellm-Response-Cost", "0") // A preliminary streaming header.
	fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":20,\"cost\":%g}}}\n\n", cost)
}

func TestProjectCapIncludesReviewerAndAllowsFinalRequestOvershoot(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	var calls atomic.Int32
	strategyAPI(t, r, func(w http.ResponseWriter, _ string) { calls.Add(1); costReply(w, 0.3) })
	cap := 0.5
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, ReviewerModel: "auto", MaxCostUSD: &cap})
	b := j.Goal.Budget
	if j.Status != Done || calls.Load() != 2 || b.Requests != 2 || b.SpentUSD != 0.6 || b.RemainingUSD != 0 || b.PendingRequests != 0 || len(b.Models) != 2 {
		t.Fatalf("goal %+v, budget %+v, calls %d", j, b, calls.Load())
	}
	if b.Models[0].Role != goalReviewing || b.Models[1].Role != goalWorking {
		t.Fatalf("roles %+v", b.Models)
	}
}

func TestCapStopsNextRequestWithinAgentAndResumeKeepsSpend(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api_twice")
	var calls atomic.Int32
	strategyAPI(t, r, func(w http.ResponseWriter, _ string) { calls.Add(1); costReply(w, 0.6) })
	cap := 0.5
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, Strategy: config.StrategyAlias, MaxCostUSD: &cap})
	if j.Status != Blocked || calls.Load() != 1 || j.Goal.Failures != 0 || !strings.Contains(j.Error, "spending cap reached") || j.Goal.Budget.SpentUSD != 0.6 {
		t.Fatalf("goal %+v, budget %+v, calls %d", j, j.Goal.Budget, calls.Load())
	}
	if _, err := os.Stat(filepath.Join(j.Worktree, "partial.txt")); err != nil {
		t.Fatal("cap lost partial work:", err)
	}
	r.Shutdown(context.Background(), "restart")
	r.Store.Close()
	next := reopenGoalRunner(t, r, "goal_api")
	if _, err := next.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID}); err != nil {
		t.Fatal(err)
	}
	blocked := waitGoal(t, next, j.ID)
	if blocked.Status != Blocked || calls.Load() != 1 || blocked.Goal.Budget.SpentUSD != 0.6 {
		t.Fatalf("resume reset budget: %+v", blocked)
	}
	cap = 2
	if _, err := next.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, MaxCostUSD: &cap}); err != nil {
		t.Fatal(err)
	}
	done := waitGoal(t, next, j.ID)
	if done.Status != Done || done.Branch != j.Branch || done.Goal.Budget.SpentUSD != 1.8 || done.Goal.Budget.Requests != 3 || done.Goal.Budget.LimitUSD != 2 {
		t.Fatalf("resume %+v, budget %+v", done, done.Goal.Budget)
	}
}

func TestCapCountsFallbackAttemptsWithoutBlockingOnQuotaRejections(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	strategyAPI(t, r, func(w http.ResponseWriter, model string) {
		if model == "smart" {
			quotaReply(w)
		} else {
			costReply(w, 0.1)
		}
	})
	cap := 1.0
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, MaxCostUSD: &cap})
	if j.Status != Done || j.Goal.Budget.SpentUSD != 0.2 || j.Goal.Budget.Requests != 3 || j.Goal.Budget.PendingRequests != 0 {
		t.Fatalf("goal %+v, budget %+v", j, j.Goal.Budget)
	}
}

func budgetFixture(t *testing.T, limit float64) (*Store, *Job, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	j := &Job{ID: "budget-test", Status: Running, RunToken: "owner", CreatedAt: time.Now(), Goal: &GoalProgress{Phase: goalWorking, Budget: &BudgetProgress{LimitUSD: limit}}}
	if err := s.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	return s, j, path
}

func TestBudgetAdmissionAtomicReceiptsIdempotentAndOwnershipFenced(t *testing.T) {
	ctx := context.Background()
	s, j, path := budgetFixture(t, 0.1)
	other, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 0 {
				store = other
			}
			id, err := store.admitModelCall(ctx, j.ID, j.RunToken, "openai/test", goalWorking)
			if err != nil && !errors.Is(err, errCostPending) {
				t.Error(err)
			}
			if id != "" {
				ids <- id
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	var accepted []string
	for id := range ids {
		accepted = append(accepted, id)
	}
	if len(accepted) != 1 {
		t.Fatalf("admitted %d unsettled calls", len(accepted))
	}
	id := accepted[0]
	for _, cost := range []float64{0.1, 0.1, 0.05} {
		if err := s.recordCost(ctx, id, cost, 10, 20); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := other.admitModelCall(ctx, j.ID, j.RunToken, "anthropic/test", goalReviewing); err == nil || !strings.Contains(err.Error(), "cap reached") {
		t.Fatalf("equality did not stop requests: %v", err)
	}
	if err := s.recordCost(ctx, "unknown", 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	b, err := other.budget(ctx, j.ID)
	if err != nil || b.SpentUSD != 0.1 || b.Requests != 1 || b.PendingRequests != 0 {
		t.Fatalf("budget %+v, %v", b, err)
	}
	for _, cost := range []float64{-1, math.NaN(), math.Inf(1)} {
		if err := s.recordCost(ctx, id, cost, 0, 0); err == nil {
			t.Fatalf("invalid cost %v accepted", cost)
		}
	}
	stale := cloneJob(j)
	j.Goal.Budget.LimitUSD = 2
	if err := s.claimGoal(ctx, j); err != nil {
		t.Fatal(err)
	}
	stale.Goal.Budget.LimitUSD = 100
	if err := other.claimGoal(ctx, stale); !errors.Is(err, errGoalStopped) {
		t.Fatalf("stale cap update: %v", err)
	}
	if _, err := other.admitModelCall(ctx, j.ID, "owner", "openai/test", goalWorking); !errors.Is(err, errGoalStopped) {
		t.Fatalf("stale worker admitted call: %v", err)
	}
	if err := other.recordCost(ctx, id, 0.12, 10, 20); err != nil {
		t.Fatal(err)
	}
	b, err = s.budget(ctx, j.ID)
	if err != nil || b.SpentUSD != 0.12 || b.LimitUSD != 2 {
		t.Fatalf("late receipt or claim reset budget: %+v, %v", b, err)
	}
}

func TestMissingCostSurvivesRestartAndExplicitZeroSettles(t *testing.T) {
	ctx := context.Background()
	s, j, path := budgetFixture(t, 1)
	a := &goalAccountant{store: s, jobID: j.ID, token: j.RunToken, model: "openai/test", role: goalWorking, timeout: 20 * time.Millisecond}
	id, err := a.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Finish(ctx, id); err == nil {
		t.Fatal("missing cost treated as zero")
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.store = s
	if _, err := a.Begin(ctx); err == nil {
		t.Fatal("restart forgot pending request")
	}
	if err := s.RecordUsage(ctx, []usage.Record{{RequestID: id, Cost: 0, CostKnown: false}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Finish(ctx, id); err == nil {
		t.Fatal("unknown receipt settled")
	}
	if err := s.RecordUsage(ctx, []usage.Record{{RequestID: id, Cost: 0, CostKnown: true}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Finish(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Begin(ctx); err != nil {
		t.Fatal("reported zero must allow next request:", err)
	}
}

func TestDelayedAuthenticatedUsageCallbackSettlesGoalCosts(t *testing.T) {
	r, _ := setupStrategy(t, "goal_api")
	receiver := httptest.NewServer(&usage.Receiver{Path: filepath.Join(t.TempDir(), usage.File), Token: "callback-secret", OnRecords: r.Store.RecordUsage})
	defer receiver.Close()
	var wg sync.WaitGroup
	defer wg.Wait()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var metadata map[string]string
		if err := json.Unmarshal([]byte(req.Header.Get("X-Litellm-Spend-Logs-Metadata")), &metadata); err != nil || metadata["dotshift_request_id"] == "" {
			t.Error("missing receipt correlation")
			return
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if !strings.Contains(string(body["litellm_metadata"]), metadata["dotshift_request_id"]) {
			t.Error("missing Responses metadata")
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			time.Sleep(30 * time.Millisecond)
			payload := fmt.Sprintf(`{"model":"openai/test","model_group":"smart","response_cost":0.3,"cost_finalized":true,"metadata":{"spend_logs_metadata":{"dotshift_request_id":%q}}}`, id)
			for _, token := range []string{"wrong", "callback-secret", "callback-secret"} {
				request, _ := http.NewRequest(http.MethodPost, receiver.URL, strings.NewReader(payload))
				request.Header.Set("Authorization", "Bearer "+token)
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Error(err)
					return
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if (token == "wrong" && response.StatusCode != 401) || (token != "wrong" && response.StatusCode != 204) {
					t.Errorf("callback status %d", response.StatusCode)
				}
			}
		}(metadata["dotshift_request_id"])
		successReply(w) // Terminal event lacks cost; the callback must settle it.
	}))
	defer upstream.Close()
	r.BaseURL = upstream.URL + "/v1"
	cap := 0.5
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish", Goal: true, MaxCostUSD: &cap})
	if j.Status != Done || j.Goal.Budget.SpentUSD != 0.6 || j.Goal.Budget.Requests != 2 || j.Goal.Budget.PendingRequests != 0 {
		t.Fatalf("callback goal %+v, budget %+v", j, j.Goal.Budget)
	}
}
