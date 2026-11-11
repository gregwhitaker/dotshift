package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
)

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
