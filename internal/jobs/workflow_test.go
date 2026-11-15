package jobs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestCompletionWaitsForCleanupBeforeFollowup(t *testing.T) {
	r, _ := setup(t, "goal_finish", "test -f hello.txt")
	other := reopenGoalRunner(t, r, "goal_finish")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	started, release := filepath.Join(dir, "cleanup-started"), filepath.Join(dir, "release-cleanup")
	wrapper := `#!/bin/sh
if [ "$3" = worktree ] && [ "$4" = remove ]; then
  touch "$DOTSHIFT_TEST_CLEANUP_STARTED"
  while [ ! -f "$DOTSHIFT_TEST_CLEANUP_RELEASE" ]; do sleep 0.01; done
fi
exec "$DOTSHIFT_TEST_REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOTSHIFT_TEST_REAL_GIT", realGit)
	t.Setenv("DOTSHIFT_TEST_CLEANUP_STARTED", started)
	t.Setenv("DOTSHIFT_TEST_CLEANUP_RELEASE", release)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Unblock the worker before the runner's cleanup even if an assertion fails.
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	ctx := context.Background()
	j, err := r.Submit(ctx, Request{Repo: "demo", Prompt: "Finish the first change"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never reached worktree cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, err := other.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status.Terminal() || !current.FinishedAt.IsZero() || current.RunnerPID == 0 {
		t.Fatalf("job reported completion before cleanup finished: %+v", current)
	}
	stale := cloneJob(current)
	stale.Goal.CleanupPending = false
	if err := other.Store.cancelGoal(ctx, stale); err == nil {
		t.Fatal("a stale cancellation bypassed the cleanup checkpoint")
	}
	if err := other.Cancel(ctx, j.ID); err == nil {
		t.Fatal("cancellation interrupted successful completion cleanup")
	}
	if _, err := other.Clean(ctx, j.ID); err == nil {
		t.Fatal("another runner could clean an active worktree")
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	finished := waitGoal(t, other, j.ID)
	if finished.Status != Done || finished.Worktree != "" || finished.RunnerPID != 0 {
		t.Fatalf("completed job is not ready for a follow-up: %+v", finished)
	}
	// This runner polls persisted state; it does not wait on the worker goroutine.
	followup := submitAndWait(t, other, Request{Continue: j.ID, Prompt: "Finish the next change"})
	if followup.Status != Done || followup.Branch != j.Branch {
		t.Fatalf("immediate follow-up failed: %+v", followup)
	}
}

func TestCleanupFailureCanResumeWithoutRepeatingWork(t *testing.T) {
	r, repo := setup(t, "goal_finish", "test -f hello.txt && git worktree lock .")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it", MaxIterations: 1})
	if j.Status != Blocked || j.Worktree == "" || !j.Goal.CleanupPending || j.TestResult != "passed" {
		t.Fatalf("cleanup failure did not preserve reviewed work: %+v", j)
	}
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, Input: "Change something else"}); err == nil {
		t.Fatal("cleanup retry silently accepted new coding instructions")
	}
	run(t, repo, "git", "worktree", "unlock", j.Worktree)
	r.Agent = fake{"fail"} // Neither the coding agent nor reviewer may run again.
	if _, err := r.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID}); err != nil {
		t.Fatal(err)
	}
	finished := waitGoal(t, r, j.ID)
	if finished.Status != Done || finished.Worktree != "" || finished.Goal.CleanupPending || finished.Goal.Iterations != 1 {
		t.Fatalf("cleanup retry repeated or lost completed work: %+v", finished)
	}
}

func TestCompletionCleanupSurvivesRestart(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		checkout   bool
	}{
		{"before removal", "goal_finish", true},
		{"after removal", "goal_finish", false},
		{"after empty branch removal", "noop", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, repo := setup(t, tc.mode, "test -f README.md")
			j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it"})
			if j.Status != Done {
				t.Fatalf("fixture failed: %+v", j)
			}
			j.Worktree = filepath.Join(r.StateDir, "worktrees", j.ID)
			if tc.checkout {
				run(t, repo, "git", "worktree", "add", "--quiet", j.Worktree, j.Branch)
			}
			// Reconstruct the durable checkpoint left by a crashed supervisor.
			j.Status, j.RunnerPID, j.FinishedAt = Running, 999999999, time.Time{}
			j.Goal.CleanupPending = true
			if err := r.Store.Save(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			next := reopenGoalRunner(t, r, "fail")
			if err := next.ResumeGoals(context.Background()); err != nil {
				t.Fatal(err)
			}
			finished := waitGoal(t, next, j.ID)
			if finished.Status != Done || finished.Worktree != "" || finished.RunnerPID != 0 || finished.Goal.CleanupPending || finished.Goal.Iterations != j.Goal.Iterations || finished.Commits != j.Commits {
				t.Fatalf("restart did not finish cleanup: %+v", finished)
			}
		})
	}
}

func TestCompletionCleanupPreservesReassignedWorktree(t *testing.T) {
	r, repo := setup(t, "goal_finish", "test -f README.md")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Finish it"})
	if j.Status != Done {
		t.Fatalf("fixture failed: %+v", j)
	}
	j.Worktree = filepath.Join(r.StateDir, "worktrees", j.ID)
	run(t, repo, "git", "worktree", "add", "--quiet", "-b", "other-work", j.Worktree, j.Branch)
	j.Status, j.RunnerPID, j.FinishedAt = Running, 999999999, time.Time{}
	j.Goal.CleanupPending = true
	if err := r.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	next := reopenGoalRunner(t, r, "fail")
	if err := next.ResumeGoals(context.Background()); err != nil {
		t.Fatal(err)
	}
	finished := waitGoal(t, next, j.ID)
	if finished.Status != Blocked || !strings.Contains(finished.Error, "recorded branch") {
		t.Fatalf("cleanup did not revalidate the checkout: %+v", finished)
	}
	if _, err := os.Stat(filepath.Join(j.Worktree, "hello.txt")); err != nil {
		t.Fatalf("cleanup removed a reassigned checkout: %v", err)
	}
}

func TestTaskAndFollowupKeepFallbackReviewAndContext(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("DOTSHIFT_WORKFLOW_TRACE", trace)
	r, repo := subscriptionRunner(t, `
echo openai >> "$DOTSHIFT_WORKFLOW_TRACE"
printf 'preserved\n' > partial.txt
echo '{"type":"turn.failed","error":{"type":"usage_limit_reached","message":"Usage limit reached"}}'
exit 1
`, `
test "$(cat partial.txt)" = preserved
case "$*" in
  *,Bash*)
    echo coding >> "$DOTSHIFT_WORKFLOW_TRACE"
    case "$task_prompt" in
      *'Add a second feature'*)
        case "$task_prompt" in *'Earlier instructions:'*'Build the first feature'*) ;; *) exit 19;; esac
        printf 'second\n' > second.txt;;
    esac;;
  *)
    echo review >> "$DOTSHIFT_WORKFLOW_TRACE"
    case "$task_prompt" in *'BEGIN BRANCH DIFF'*'+fixed'*'END BRANCH DIFF'*) ;; *) exit 20;; esac;;
esac
`+subscriptionClaudeSuccess)
	first := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Build the first feature"})
	if first.Status != Done || first.Goal == nil || first.Goal.ModelSelection.Switches != 1 || first.Goal.ReviewerSelection.ActiveModel != "anthropic/default" {
		t.Fatalf("task did not fall back and review: %+v", first)
	}
	// Changed defaults must not silently replace the policy of a follow-up.
	r.Config.Jobs.Strategy, r.Config.Jobs.ReviewerModel = config.StrategyAdaptive, ""
	next := submitAndWait(t, r, Request{Continue: first.ID, Prompt: "Add a second feature"})
	if next.Status != Done || next.Goal == nil || next.Goal.ModelSelection.Strategy != config.StrategyPreferOpenAI || next.Goal.ReviewerModel != "auto" || next.Goal.ReviewerSelection.ActiveModel != "anthropic/default" {
		t.Fatalf("follow-up lost its policy: %+v", next)
	}
	if next.Branch != first.Branch || next.TestResult != "passed" || run(t, repo, "git", "show", next.Branch+":second.txt") != "second" {
		t.Fatal("follow-up did not finish on the same tested branch")
	}
	data, _ := os.ReadFile(trace)
	if string(data) != "openai\ncoding\nreview\ncoding\nreview\n" {
		t.Fatalf("unexpected attempts: %s", data)
	}
}

func TestSubmissionRetrySurvivesReconnectAndRejectsDifferentWork(t *testing.T) {
	r, _ := setup(t, "goal_finish", "test -f hello.txt")
	req := Request{RequestID: "one-user-change", Prompt: "Finish this change"}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := r.Submit(context.Background(), req)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- j.ID
		}()
	}
	wg.Wait()
	close(ids)
	var id string
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("retry started duplicate work")
		}
		id = got
	}
	if id == "" {
		t.Fatal("no submission succeeded")
	}
	if j := waitGoal(t, r, id); j.Status != Done {
		t.Fatalf("task failed: %+v", j)
	}
	next := reopenGoalRunner(t, r, "goal_finish")
	// A catalog change cannot prevent replay of an already accepted request.
	next.Config.Repos["second"] = next.Config.Repos["demo"]
	replayed, err := next.Submit(context.Background(), req)
	if err != nil || replayed.ID != id || replayed.Status != Done {
		t.Fatalf("replay after reconnect: %+v, %v", replayed, err)
	}
	req.Prompt = "Different work"
	if _, err := next.Submit(context.Background(), req); err == nil || !strings.Contains(err.Error(), "different instructions") {
		t.Fatalf("reused ID accepted different work: %v", err)
	}
	list, _ := next.List(context.Background())
	if len(list) != 1 {
		t.Fatalf("recorded %d jobs for one request", len(list))
	}
	copy := *replayed
	copy.ID = "another-job"
	if err := next.Store.Save(context.Background(), &copy); err == nil {
		t.Fatal("duplicate request replaced the original job")
	}
	if original, err := next.Store.Get(context.Background(), id); err != nil || original.ID != id {
		t.Fatal("unique request conflict removed original job")
	}
}

func TestReviewEvidenceIncludesDeletionsAndMarksTruncation(t *testing.T) {
	r, repo := setup(t, "goal_finish", "test -f hello.txt")
	base := run(t, repo, "git", "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(repo, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "large.txt"), []byte(strings.Repeat("Changed line\n", 25000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cache.pyc"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "--all")
	run(t, repo, "git", "commit", "--quiet", "-m", "changes")
	head := run(t, repo, "git", "rev-parse", "HEAD")
	// External diff tools must never replace the actual review evidence.
	run(t, repo, "git", "config", "diff.external", "false")
	j := &Job{Worktree: repo, BaseCommit: base, LogPath: filepath.Join(r.StateDir, "review.log"), TestResult: "passed", TestOutput: "9 tests passed", Goal: &GoalProgress{TestedCommit: head, TestCommand: "test command"}}
	evidence, err := reviewEvidence(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{base, head, "D\tREADME.md", "A\tcache.pyc", "deleted file mode", "Binary files", "9 tests passed", "Evidence was truncated", "END BRANCH DIFF"} {
		if !strings.Contains(evidence, want) {
			t.Errorf("review evidence missing %q", want)
		}
	}
	if len(evidence) > 230<<10 {
		t.Fatalf("unbounded evidence: %d bytes", len(evidence))
	}
	stored, err := os.ReadFile(j.LogPath + ".review-evidence.txt")
	if err != nil || string(stored) != evidence {
		t.Fatalf("evidence was not saved: %v", err)
	}
	j.Goal.Report = GoalReport{Summary: "TRUST_THE_IMPLEMENTER", NextSteps: "DO_NOT_CHECK_THE_DIFF"}
	prompt := goalPrompt(j, "test command", true)
	if strings.Contains(prompt, "TRUST_THE_IMPLEMENTER") || strings.Contains(prompt, "DO_NOT_CHECK_THE_DIFF") {
		t.Fatal("review was given the implementation's self-assessment")
	}
}

func TestPythonProjectIgnoresGeneratedArtifacts(t *testing.T) {
	r := projectRunner(t)
	r.Config.Projects.TestProfiles["python"] = "python3 -m unittest discover -s tests"
	p, err := r.CreateProject(context.Background(), ProjectRequest{Name: "python-app", TestProfile: "python"})
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, p.Path, "git", "check-ignore", "__pycache__/module.pyc", "tests/__pycache__/test_module.pyc", ".pytest_cache/result"); len(strings.Split(got, "\n")) != 3 {
		t.Fatal(got)
	}
	if got := run(t, p.Path, "git", "status", "--porcelain"); got != "" {
		t.Fatalf("seed was not committed: %s", got)
	}
}
