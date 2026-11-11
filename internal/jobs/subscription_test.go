package jobs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
)

const subscriptionFakeHeader = `#!/bin/sh
set -eu
case "$*" in
  *--help*) echo '--ignore-user-config --json --restricted --safe-mode --json-schema'; exit 0;;
  *login\ status*) echo 'Logged in using ChatGPT'; exit 0;;
  *auth\ status*) echo '{"loggedIn":true,"authMethod":"claude.ai"}'; exit 0;;
esac
test -z "${OPENAI_API_KEY-}"
test -z "${ANTHROPIC_API_KEY-}"
test -z "${DOTSHIFT_API_KEY-}"
task_prompt=$(cat)
`

const subscriptionCodexSuccess = `
last=''
readonly=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message) last="$2"; shift;;
    --sandbox) if [ "$2" = read-only ]; then readonly=true; fi; shift;;
  esac
  shift
done
if [ "$readonly" = false ]; then printf 'fixed\n' > hello.txt; fi
printf '%s\n' '{"status":"complete","summary":"Implemented","next_steps":""}' > "$last"
echo '{"type":"turn.completed"}'
`

const subscriptionClaudeSuccess = `
case "$*" in
  *,Bash*) printf 'fixed\n' > hello.txt;;
  *) test "$(cat hello.txt)" = fixed;;
esac
echo '{"type":"result","subtype":"success","is_error":false,"structured_output":{"status":"complete","summary":"Checked","next_steps":""},"result":"Checked"}'
`

func subscriptionRunner(t *testing.T, codexBody, claudeBody string) (*Runner, string) {
	t.Helper()
	r, repo := setup(t, "unused", `test "$(cat hello.txt)" = fixed`)
	cfg := r.Config
	cfg.Jobs.Mode, cfg.Jobs.Strategy, cfg.Jobs.ReviewerModel = config.ModeSubscription, config.StrategyPreferOpenAI, "auto"
	cfg.Providers["anthropic"] = config.Provider{}
	cfg.Aliases["smart"] = []string{"openai/default", "anthropic/default"}
	cfg.Jobs.Agent.Command, cfg.Jobs.ClaudeCommand = filepath.Join(t.TempDir(), "codex"), filepath.Join(t.TempDir(), "claude")
	writeSubscriptionCLI(t, cfg.Jobs.Agent.Command, codexBody)
	writeSubscriptionCLI(t, cfg.Jobs.ClaudeCommand, claudeBody)
	r.Agent = &agent.Subscriptions{CodexPath: cfg.Jobs.Agent.Command, ClaudePath: cfg.Jobs.ClaudeCommand}
	// The router is deliberately unusable: any attempt to launch a gateway or
	// call the API path will make these end-to-end goal tests fail.
	r.BaseURL, r.APIKey = "not-a-router", "should-not-be-passed"
	return r, repo
}

func writeSubscriptionCLI(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(subscriptionFakeHeader+body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionGoalCodingTestsAndIndependentReview(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-be-used")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-be-used")
	r, repo := subscriptionRunner(t, subscriptionCodexSuccess, subscriptionClaudeSuccess)
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Create hello.txt with fixed", Goal: true, MaxIterations: 2})
	if j.Status != Done || j.TestResult != "passed" || j.Goal.Iterations != 1 || j.ExecutionMode != config.ModeSubscription {
		t.Fatalf("goal = %+v, progress = %+v", j, j.Goal)
	}
	if j.Goal.ModelSelection.ActiveModel != "openai/default" || j.Goal.ReviewerSelection.ActiveModel != "anthropic/default" {
		t.Fatalf("wrong roles: %+v", j.Goal)
	}
	if got := run(t, repo, "git", "show", j.Branch+":hello.txt"); got != "fixed" {
		t.Fatal(got)
	}
	catalog, err := r.Strategies(context.Background())
	if err != nil || catalog.SupportsSpendingCaps || catalog.ExecutionMode != config.ModeSubscription {
		t.Fatalf("catalog=%+v, err=%v", catalog, err)
	}
}

func TestSubscriptionExplicitReviewerWaitsAndResumes(t *testing.T) {
	r, repo := subscriptionRunner(t, `
printf 'preserved\n' > partial.txt
echo '{"type":"turn.failed","error":{"message":"You have hit your limit","type":"usage_limit_reached"}}'
exit 1
`, "test -f partial.txt\n"+subscriptionClaudeSuccess)
	r.Config.Aliases["review"] = []string{"openai/default", "anthropic/default"}
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Complete the project", Goal: true, ReviewerModel: "review", MaxIterations: 3})
	// An explicitly chosen reviewer pool retains its different-model rule.
	if j.Status != Blocked || j.TestResult != "passed" || j.Goal.Iterations != 1 || j.Goal.ModelSelection.ActiveModel != "anthropic/default" {
		t.Fatalf("goal=%+v, progress=%+v", j, j.Goal)
	}
	if got := run(t, repo, "git", "show", j.Branch+":partial.txt"); got != "preserved" {
		t.Fatal(got)
	}
	next := reopenGoalRunner(t, r, "unused")
	next.Agent = r.Agent
	availability, err := next.modelAvailability(context.Background(), config.ModelRoute{Model: "openai/default", Provider: "openai"}, time.Now())
	if err != nil || availability.Available || availability.Reason != modelpolicy.SubscriptionLimit || availability.RetryAt != nil {
		t.Fatalf("availability=%+v, err=%v", availability, err)
	}
	writeSubscriptionCLI(t, r.Config.Jobs.Agent.Command, subscriptionCodexSuccess)
	resumed, err := next.ResumeGoal(context.Background(), ResumeGoalRequest{ID: j.ID, RetryUnavailable: true})
	if err != nil {
		t.Fatal(err)
	}
	j = waitGoal(t, next, resumed.ID)
	if j.Status != Done || j.Goal.Iterations != 2 || j.ExecutionMode != config.ModeSubscription {
		t.Fatalf("resumed=%+v", j)
	}
}

func TestSubscriptionLimitFallbackCompletesWithFreshClaudeReview(t *testing.T) {
	for _, knownReset := range []bool{false, true} {
		t.Run(fmt.Sprintf("known_reset=%t", knownReset), func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			t.Setenv("DOTSHIFT_TEST_CALLS", calls)
			reset := int64(0)
			if knownReset {
				reset = time.Now().Add(time.Hour).Unix()
			}
			codex := fmt.Sprintf(`
echo "openai $$" >> "$DOTSHIFT_TEST_CALLS"
printf 'preserved\n' > partial.txt
echo '{"type":"turn.failed","error":{"type":"usage_limit_reached","resets_at":%d}}'
exit 1
`, reset)
			claude := `
test "$(cat partial.txt)" = preserved
case "$*" in
  *--resume*|*--continue*) exit 10;;
esac
case "$*" in
  *--no-session-persistence*) ;;
  *) exit 11;;
esac
case "$*" in
  *,Bash*) echo "claude-code $$" >> "$DOTSHIFT_TEST_CALLS";;
  *)
    echo "claude-review $$" >> "$DOTSHIFT_TEST_CALLS"
    case "$*" in
      *'--tools Read,Glob,Grep --allowedTools Read,Glob,Grep'*) ;;
      *) exit 12;;
    esac
    case "$task_prompt" in
      *'Independently review'*) ;;
      *) exit 13;;
    esac
    case "$task_prompt" in
      *'dotshift commits them'*) exit 14;;
    esac
    ;;
esac
` + subscriptionClaudeSuccess
			r, repo := subscriptionRunner(t, codex, claude)
			j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Complete the project", Goal: true, MaxIterations: 3})
			if j.Status != Done || j.TestResult != "passed" || j.Goal.Iterations != 1 ||
				j.Goal.ModelSelection.ActiveModel != "anthropic/default" || j.Goal.ReviewerSelection.ActiveModel != "anthropic/default" {
				t.Fatalf("goal=%+v, progress=%+v", j, j.Goal)
			}
			if got := run(t, repo, "git", "show", j.Branch+":partial.txt"); got != "preserved" {
				t.Fatalf("lost OpenAI's partial work: %q", got)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.Fields(string(data))
			if len(fields) != 6 || fields[0] != "openai" || fields[2] != "claude-code" || fields[4] != "claude-review" || fields[3] == fields[5] {
				t.Fatalf("expected OpenAI, Claude coding, and a separate Claude review process: %s", data)
			}
			next := reopenGoalRunner(t, r, "unused")
			availability, err := next.modelAvailability(context.Background(), config.ModelRoute{Model: "openai/default", Provider: "openai"}, time.Now())
			if err != nil || availability.Available || availability.Reason != modelpolicy.SubscriptionLimit || (availability.RetryAt != nil) != knownReset {
				t.Fatalf("OpenAI limit was not preserved: %+v, %v", availability, err)
			}
		})
	}
}

func TestSubscriptionAutoReviewDoesNotWaitForOpenAI(t *testing.T) {
	r, _ := subscriptionRunner(t, subscriptionCodexSuccess, subscriptionClaudeSuccess)
	reset := time.Now().Add(4 * time.Second)
	j := &Job{ID: "review-with-claude", Model: "smart", Status: Running, RunToken: "owner", CreatedAt: time.Now(), Goal: &GoalProgress{
		Phase: goalReviewing, ReviewerModel: "auto",
		ModelSelection:    &ModelSelection{Strategy: config.StrategyPreferOpenAI, ActiveModel: "anthropic/default"},
		ReviewerSelection: &ModelSelection{Strategy: config.StrategyAlias, ActiveModel: "openai/default", WaitingUntil: &reset},
	}}
	if err := r.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if err := r.recordModelFailure(context.Background(), config.ModelRoute{Model: "openai/default", Provider: "openai"}, &modelpolicy.Failure{
		Kind: modelpolicy.SubscriptionLimit, ProviderWide: true, RetryAt: reset,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	route, err := r.selectGoalModel(ctx, j, true, io.Discard)
	if err != nil || route.Model != "anthropic/default" {
		t.Fatalf("review waited for OpenAI despite available Claude: %+v, %v", route, err)
	}
	if j.Goal.ReviewerSelection.WaitingUntil != nil || j.Goal.ReviewerSelection.Switches != 1 {
		t.Fatalf("previous review wait was not cleared: %+v", j.Goal.ReviewerSelection)
	}
}

func TestSubscriptionFallbackReviewCanRequestFixes(t *testing.T) {
	r, repo := subscriptionRunner(t, `
echo '{"type":"turn.failed","error":{"type":"usage_limit_reached"}}'
exit 1
`, `
case "$*" in
  *,Bash*)
    if [ -f hello.txt ]; then
      case "$task_prompt" in
        *'Add the missing review fix'*) ;;
        *) exit 15;;
      esac
      echo addressed > review-fix.txt
    fi
    ;;
  *)
    if [ ! -f review-fix.txt ]; then
      echo '{"type":"result","subtype":"success","is_error":false,"structured_output":{"status":"continue","summary":"An acceptance criterion is missing","next_steps":"Add the missing review fix"}}'
      exit 0
    fi
    ;;
esac
`+subscriptionClaudeSuccess)
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Complete the project", MaxIterations: 3})
	if j.Status != Done || j.TestResult != "passed" || j.Goal.Iterations != 2 || j.Goal.ReviewerSelection.ActiveModel != "anthropic/default" {
		t.Fatalf("review feedback was not fixed and revalidated: %+v, %+v", j, j.Goal)
	}
	if got := run(t, repo, "git", "show", j.Branch+":review-fix.txt"); got != "addressed" {
		t.Fatalf("missing review fix: %q", got)
	}
}

func TestSubscriptionFallbackStillRequiresAvailableReviewer(t *testing.T) {
	r, _ := subscriptionRunner(t, `
echo '{"type":"turn.failed","error":{"type":"usage_limit_reached"}}'
exit 1
`, `
case "$*" in
  *,Bash*) ;;
  *) echo '{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}'; exit 1;;
esac
`+subscriptionClaudeSuccess)
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Complete the project", Goal: true, MaxIterations: 3})
	if j.Status != Blocked || j.TestResult != "passed" || j.Goal.Phase != goalReviewing || !strings.Contains(j.Error, "all strategy models need restored access") {
		t.Fatalf("goal must not finish when both providers cannot review: %+v, %+v", j, j.Goal)
	}
}

func TestSubscriptionModeCapsAndResumeGuard(t *testing.T) {
	r, _ := subscriptionRunner(t, subscriptionCodexSuccess, subscriptionClaudeSuccess)
	cap := 1.0
	if _, err := r.Submit(context.Background(), Request{Repo: "demo", Prompt: "Implement", Goal: true, MaxCostUSD: &cap}); err == nil || !strings.Contains(err.Error(), "subscription mode") {
		t.Fatalf("cap error = %v", err)
	}
	r.Config.Jobs.Mode = config.ModeAPI
	j := &Job{ExecutionMode: config.ModeSubscription}
	if _, err := r.goalRepo(context.Background(), j); err == nil || !strings.Contains(err.Error(), "restore its mode") {
		t.Fatalf("mode guard = %v", err)
	}
	r.Config.Jobs.Mode = config.ModeSubscription
	j.ExecutionMode = "" // a pre-migration API goal
	if _, err := r.goalRepo(context.Background(), j); err == nil || !strings.Contains(err.Error(), "uses api mode") {
		t.Fatalf("legacy guard = %v", err)
	}
	route := config.ModelRoute{Model: "openai/default", Provider: "openai"}
	subScope, _ := r.availabilityScopes(route)
	r.Config.Jobs.Mode = config.ModeAPI
	apiScope, _ := r.availabilityScopes(route)
	if subScope == apiScope {
		t.Fatal("subscription and API failures share a scope")
	}
}

func TestSubscriptionSingleTaskUsesCLIWithoutRouter(t *testing.T) {
	r, _ := subscriptionRunner(t, subscriptionCodexSuccess, subscriptionClaudeSuccess)
	for _, model := range []string{"codex", "claude"} {
		r.Config.Aliases[model] = []string{map[string]string{"codex": "openai/default", "claude": "anthropic/default"}[model]}
		j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Implement", Model: model})
		if j.Status != Done || j.TestResult != "passed" {
			t.Fatalf("%s job = %+v", model, j)
		}
	}
}
