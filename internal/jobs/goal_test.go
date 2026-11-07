package jobs

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fakeGoalAgent(mode, prompt, summary string) int {
	if strings.HasPrefix(mode, "goal_api") {
		return fakeAPIGoal(mode, prompt, summary)
	}
	review := os.Getenv("FAKE_REVIEW") == "true"
	report := GoalReport{Status: "complete", Summary: "Goal implemented and verified."}
	content, _ := os.ReadFile("hello.txt")
	if review {
		if mode == "goal_review" && string(content) != "fixed\n" {
			report = GoalReport{Status: "continue", Summary: "Acceptance criterion is missing.", NextSteps: "Implement the remaining acceptance criterion."}
		}
	} else {
		switch mode {
		case "goal_fix", "goal_review":
			value := "broken\n"
			if len(content) > 0 {
				if mode == "goal_fix" && !strings.Contains(prompt, "Latest validation result: failed") {
					return 12
				}
				if mode == "goal_review" && !strings.Contains(prompt, "remaining acceptance criterion") {
					return 13
				}
				value = "fixed\n"
			}
			os.WriteFile("hello.txt", []byte(value), 0o644)
		case "goal_continue":
			os.WriteFile("hello.txt", append(content, []byte("increment\n")...), 0o644)
			report = GoalReport{Status: "continue", Summary: "Made an increment.", NextSteps: "Finish the next requirement."}
		case "goal_block":
			os.WriteFile("hello.txt", []byte("partial\n"), 0o644)
			if !strings.Contains(prompt, "Use the approved design") {
				report = GoalReport{Status: "blocked", Summary: "Need a design decision.", NextSteps: "Which design should I implement?"}
			}
		case "goal_sleep":
			os.WriteFile("hello.txt", []byte("preserved partial work\n"), 0o644)
			time.Sleep(time.Minute)
		case "goal_invalid":
			os.WriteFile(summary, []byte("I think it is complete."), 0o644)
			return 0
		case "goal_retry":
			if len(content) == 0 {
				os.WriteFile("hello.txt", []byte("recoverable partial work\n"), 0o644)
				return 17
			}
			if !strings.Contains(prompt, "exit status 17") {
				return 18
			}
		case "goal_finish":
			if len(content) == 0 {
				os.WriteFile("hello.txt", []byte("finished\n"), 0o644)
			}
		}
	}
	data, _ := json.Marshal(report)
	os.WriteFile(summary, data, 0o644)
	return 0
}

func waitGoal(t *testing.T, r *Runner, id string) *Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	j, err := r.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func waitGoalPhase(t *testing.T, r *Runner, id, phase string) *Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := r.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Goal.Phase == phase && j.AgentPID > 0 {
			return j
		}
		if j.Status.Terminal() {
			t.Fatalf("goal stopped before %s: %+v", phase, j)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goal never reached %s", phase)
	return nil
}

func reopenGoalRunner(t *testing.T, r *Runner, mode string) *Runner {
	t.Helper()
	store, err := OpenStore(filepath.Join(r.StateDir, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	next := &Runner{Config: r.Config, Store: store, Agent: fake{mode}, BaseURL: r.BaseURL, APIKey: r.APIKey, StateDir: r.StateDir}
	t.Cleanup(func() {
		next.Shutdown(context.Background(), "test finished")
		store.Close()
	})
	return next
}

func TestGoalFixesFailuresAndRequiresReview(t *testing.T) {
	for _, tc := range []struct{ mode, test string }{
		{"goal_fix", `test "$(cat hello.txt)" = fixed`},
		{"goal_review", "test -f hello.txt"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r, repo := setup(t, tc.mode, tc.test)
			j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Implement all acceptance criteria", Goal: true, Model: "fast"})
			if j.Status != Done || j.TestResult != "passed" || j.Goal.Iterations != 2 || j.Commits != 2 || j.Model != "fast" {
				t.Fatalf("goal = %+v, progress = %+v", j, j.Goal)
			}
			if got := run(t, repo, "git", "show", j.Branch+":hello.txt"); got != "fixed" {
				t.Fatalf("branch contents = %q", got)
			}
			if got := run(t, repo, "git", "status", "--porcelain"); got != "" || j.Worktree != "" {
				t.Fatalf("working copy = %q, worktree = %q", got, j.Worktree)
			}
			log, _ := os.ReadFile(j.LogPath)
			if !strings.Contains(string(log), "reviewing") {
				t.Fatal("completion was not reviewed")
			}
		})
	}
}

func TestGoalLimitAndResumePreserveBranch(t *testing.T) {
	r, _ := setup(t, "goal_fix", `test "$(cat hello.txt)" = fixed`)
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Make it work", Goal: true, MaxIterations: 1})
	if j.Status != Blocked || j.TestResult != "failed" || j.Goal.Iterations != 1 || j.Worktree == "" || !strings.Contains(j.Error, "max_iterations") {
		t.Fatalf("goal = %+v, progress = %+v", j, j.Goal)
	}
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID}); err == nil {
		t.Fatal("exhausted limit was silently reset")
	}
	limit := 0
	resumed, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, Model: "fast", MaxIterations: &limit})
	if err != nil {
		t.Fatal(err)
	}
	result := waitGoal(t, r, resumed.ID)
	if result.ID != j.ID || result.Branch != j.Branch || result.Model != "fast" || result.Status != Done || result.Commits != 2 {
		t.Fatalf("resumed = %+v", result)
	}
}

func TestGoalBlockerAcceptsInput(t *testing.T) {
	r, _ := setup(t, "goal_block", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Build the project", Goal: true})
	if j.Status != Blocked || !strings.Contains(j.Error, "Which design") {
		t.Fatalf("goal = %+v", j)
	}
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, Input: "Use the approved design"}); err != nil {
		t.Fatal(err)
	}
	if result := waitGoal(t, r, j.ID); result.Status != Done || !strings.Contains(result.Goal.Input, "approved design") {
		t.Fatalf("goal = %+v", result)
	}
}

func TestGoalRestartPreservesPartialWork(t *testing.T) {
	r, repo := setup(t, "goal_sleep", "test -f hello.txt")
	j, err := r.Submit(context.Background(), Request{Repo: "demo", Prompt: "Finish the project", Goal: true})
	if err != nil {
		t.Fatal(err)
	}
	waitGoalPhase(t, r, j.ID, goalWorking)
	// Wait for the agent's actual edit, then interrupt it mid-attempt.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(j.Worktree, "hello.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent never wrote partial work")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Shutdown(context.Background(), "restart")
	stopped, err := r.Get(context.Background(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != Queued || stopped.RunnerPID != 0 || stopped.Worktree != j.Worktree {
		t.Fatalf("stopped = %+v", stopped)
	}
	r.Store.Close()
	next := reopenGoalRunner(t, r, "goal_finish")
	if err := next.ResumeGoals(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := waitGoal(t, next, j.ID)
	if result.Status != Done || result.RunToken == j.RunToken {
		t.Fatalf("resumed = %+v", result)
	}
	if got := run(t, repo, "git", "show", result.Branch+":hello.txt"); got != "preserved partial work" {
		t.Fatalf("lost work: %q", got)
	}
}

func TestGoalRecoversDeadSupervisorAndOrphanProcess(t *testing.T) {
	r, _ := setup(t, "goal_continue", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it", Goal: true, MaxIterations: 1})
	orphan := exec.CommandContext(context.Background(), "sleep", "30")
	detach(orphan)
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killGroup(orphan.Process.Pid); orphan.Wait() })
	j.Status, j.RunnerPID, j.AgentPID = Running, 999999999, orphan.Process.Pid
	j.Goal.Phase, j.Goal.MaxIterations = goalReviewing, 0
	j.Goal.Report = GoalReport{Status: "complete", Summary: "Ready for review."}
	if err := r.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	next := reopenGoalRunner(t, r, "goal_finish")
	if err := next.ResumeGoals(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := waitGoal(t, next, j.ID)
	if result.Status != Done || result.Goal.Iterations != 1 {
		t.Fatalf("recovery = %+v, progress = %+v", result, result.Goal)
	}
	if err := orphan.Wait(); err == nil {
		t.Fatal("orphan process was not terminated")
	}
}

func TestGoalCancellationStopsTestsAndStaysCancelled(t *testing.T) {
	r, _ := setup(t, "goal_finish", "sleep 30")
	j, err := r.Submit(context.Background(), Request{Repo: "demo", Prompt: "Build it", Goal: true})
	if err != nil {
		t.Fatal(err)
	}
	waitGoalPhase(t, r, j.ID, goalTesting)
	other := reopenGoalRunner(t, r, "goal_finish")
	if err := other.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	result := waitGoal(t, r, j.ID)
	if result.Status != Cancelled || result.AgentPID != 0 || result.RunnerPID != 0 {
		t.Fatalf("cancelled = %+v", result)
	}
	if err := other.ResumeGoals(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := other.Get(context.Background(), j.ID)
	if current.Status != Cancelled {
		t.Fatal("cancelled goal restarted automatically")
	}
}

func TestGoalRetriesExecutionFailuresAndRejectsInvalidCompletion(t *testing.T) {
	for _, mode := range []string{"goal_retry", "goal_invalid"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := setup(t, mode, "test -f hello.txt")
			j, err := r.Submit(context.Background(), Request{Repo: "demo", Prompt: "Finish it", Goal: true})
			if err != nil {
				t.Fatal(err)
			}
			result := waitGoal(t, r, j.ID)
			if mode == "goal_retry" && result.Status != Done {
				t.Fatalf("recoverable failure = %+v", result)
			}
			if mode == "goal_invalid" && (result.Status != Blocked || result.Goal.Failures != 3 || result.Goal.Iterations != 0) {
				t.Fatalf("invalid completion = %+v, progress = %+v", result, result.Goal)
			}
		})
	}
}

func TestConcurrentGoalResumesHaveOneOwner(t *testing.T) {
	r, _ := setup(t, "goal_block", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it", Goal: true})
	first := reopenGoalRunner(t, r, "goal_sleep")
	second := reopenGoalRunner(t, r, "goal_sleep")
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, runner := range []*Runner{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runner.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID})
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	successes := 0
	for err := range errors {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("%d workers claimed the goal", successes)
	}
	// A stale worker cannot checkpoint over the new owner's progress.
	if err := r.Store.saveGoal(context.Background(), j); err == nil {
		t.Fatal("stale worker overwrote resumed goal")
	}
}

func TestGoalCleanupPreventsResume(t *testing.T) {
	r, _ := setup(t, "goal_block", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it", Goal: true})
	cleaned, err := r.Clean(context.Background(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.Worktree != "" || cleaned.Status != Blocked || cleaned.Error != j.Error || cleaned.RunnerPID != 0 {
		t.Fatalf("cleaned = %+v", cleaned)
	}
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID}); err == nil || !strings.Contains(err.Error(), "worktree was removed") {
		t.Fatalf("resuming cleaned goal = %v", err)
	}
}

func TestGoalsRequireTestsAndPreserveLegacyJobs(t *testing.T) {
	r, _ := setup(t, "edit", "")
	if _, err := r.Submit(context.Background(), Request{Repo: "demo", Prompt: "Build it", Goal: true}); err == nil || !strings.Contains(err.Error(), ".test must be configured") {
		t.Fatalf("missing test command = %v", err)
	}
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "One task"})
	if j.Status != Done || j.Goal != nil {
		t.Fatalf("ordinary task = %+v", j)
	}
}

func TestGoalRecoveryRevalidatesChangedCode(t *testing.T) {
	r, _ := setup(t, "goal_continue", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it", Goal: true, MaxIterations: 1})
	// Simulate a restart after validation, with additional untested edits.
	j.Status, j.RunnerPID = Running, 999999999
	j.Goal.Phase = goalReviewing
	j.Goal.Report = GoalReport{Status: "complete", Summary: "Ready for review."}
	if err := os.Remove(filepath.Join(j.Worktree, "hello.txt")); err != nil {
		t.Fatal(err)
	}
	if err := r.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	next := reopenGoalRunner(t, r, "goal_finish")
	if err := next.ResumeGoals(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := waitGoal(t, next, j.ID)
	if result.Status != Blocked || result.TestResult != "failed" {
		t.Fatalf("stale passing tests completed the goal: %+v", result)
	}
}

func TestGoalRecoveryHonorsChangedAllowlist(t *testing.T) {
	r, _ := setup(t, "goal_continue", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it", Goal: true, MaxIterations: 1})
	j.Status, j.RunnerPID = Running, 999999999
	if err := r.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	delete(r.Config.Repos, "demo")
	next := reopenGoalRunner(t, r, "goal_finish")
	if err := next.ResumeGoals(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := waitGoal(t, next, j.ID)
	if result.Status != Blocked || !strings.Contains(result.Error, "not allowlisted") || result.Goal.Iterations != 1 {
		t.Fatalf("allowlist change = %+v", result)
	}
}

func TestStoreUpgradesDatabaseBeforeGoals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(schema, ",\n\tgoal        TEXT NOT NULL DEFAULT ''", "", 1)
	old = strings.Replace(old, ",\n\trun_token   TEXT NOT NULL DEFAULT ''", "", 1)
	if _, err := s.db.Exec(`DROP TABLE jobs`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(old); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO jobs (id, repo, repo_path, prompt, model, branch, base_commit, worktree, status, created_at)
		VALUES ('legacy', 'demo', '/repo', 'old task', 'smart', 'branch', 'base', '', 'done', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	legacy, err := s.Get(context.Background(), "legacy")
	if err != nil || legacy.Goal != nil || legacy.Status != Done || legacy.Prompt != "old task" {
		t.Fatalf("legacy record = %+v, %v", legacy, err)
	}
	goal := &Job{ID: "goal", Goal: &GoalProgress{Phase: goalWorking, Iterations: 2}, RunToken: "owner"}
	if err := s.Save(context.Background(), goal); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Get(context.Background(), goal.ID)
	if err != nil || restored.Goal.Iterations != 2 || restored.RunToken != "owner" {
		t.Fatalf("goal record = %+v, %v", restored, err)
	}
}
