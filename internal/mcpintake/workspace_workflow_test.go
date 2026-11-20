package mcpintake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/jobs"
)

// Exercise the exact tools Dot receives through authenticated MCP. The local
// agent intentionally fails validation once, then fixes it; review is separate.
// No dashboard, per-repo configuration, model choices or iteration controls are
// used by the client during the workflow.
func TestDotWorkspaceWorkflowOverOAuth(t *testing.T) {
	const script = `task_prompt=$(cat)
if [ "$READ_ONLY" = true ]; then
  test "$(cat hello.txt)" = fixed || exit 21
  case "$task_prompt" in *'BEGIN BRANCH DIFF'*) ;; *) exit 22;; esac
else
  printf 'test:\n\ttest "$$(cat hello.txt)" = fixed\n' > Makefile
  if [ -f hello.txt ]; then echo fixed > hello.txt; else echo broken > hello.txt; fi
  case "$task_prompt" in
    *'Add a follow-up feature'*)
      case "$task_prompt" in *'Earlier instructions:'*) ;; *) exit 23;; esac
      echo followup > second.txt
      printf '\ttest -f second.txt\n' >> Makefile;;
  esac
fi
printf '%s' '{"status":"complete","summary":"Implemented and checked the requested work.","next_steps":""}' > "$SUMMARY"`
	runner := newRunner(t, script)
	repo := runner.Config.Repos["demo"].Path
	runner.Config.Repos = nil
	runner.Config.Projects = config.Projects{Root: filepath.Dir(repo), Discover: true, TestProfiles: map[string]string{"auto": "auto"}}
	srv, _ := newTestServer(t, runner, "")
	cs := connect(t, srv, "none")
	catalog, msg := call(t, cs, "list_repos", nil)
	if msg != "" || len(catalog["repos"].([]any)) != 1 || catalog["projects"].(map[string]any)["discover"] != true {
		t.Fatalf("discover workspace: %+v, %s", catalog, msg)
	}
	request := map[string]any{"request_id": "dot-change", "title": "Build the local app", "repo": filepath.Base(repo), "instructions": "Implement the local app"}
	started, msg := call(t, cs, "start_task", request)
	if msg != "" {
		t.Fatal(msg)
	}
	id := started["task_id"].(string)
	// A lost client connection does not lose the accepted task or duplicate it.
	cs.Close()
	cs = connect(t, srv, "none")
	replayed, msg := call(t, cs, "start_task", request)
	if msg != "" || replayed["task_id"] != id || replayed["title"] != request["title"] {
		t.Fatalf("reconnect replay: %+v, %s", replayed, msg)
	}
	status := waitFinished(t, cs, id)
	if status["status"] != "done" {
		t.Fatalf("first task: %+v", status)
	}
	result, msg := call(t, cs, "task_result", map[string]any{"task_id": id, "include_diff": true})
	if msg != "" || result["tests"] != "passed" || result["goal"].(map[string]any)["iterations"] != 2.0 || !strings.Contains(fmt.Sprint(result["diff"]), "+fixed") {
		t.Fatalf("test/fix/review result: %+v, %s", result, msg)
	}
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("coding changed the original checkout")
	}
	delivery := result["delivery"].(map[string]any)
	apply := map[string]any{"task_id": id, "commit": delivery["commit"], "target_branch": delivery["target_branch"], "expected_head": delivery["target_head"]}
	for range 2 {
		applied, msg := call(t, cs, "apply_task", apply)
		if msg != "" || applied["applied"] != true {
			t.Fatalf("apply/retry: %+v, %s", applied, msg)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "hello.txt")); string(got) != "fixed\n" {
		t.Fatal("result was not delivered locally")
	}
	// A new service instance can recover the recorded task and follow up without
	// the user reconstructing a project, model choice or branch.
	runner.Shutdown(context.Background(), "restart fixture")
	next := &jobs.Runner{Config: runner.Config, Store: runner.Store, Agent: runner.Agent, BaseURL: runner.BaseURL, APIKey: runner.APIKey, StateDir: runner.StateDir}
	t.Cleanup(func() { next.Shutdown(context.Background(), "finished") })
	nextServer, _ := newTestServer(t, next, "")
	cs = connect(t, nextServer, "none")
	recent, msg := call(t, cs, "list_tasks", nil)
	if msg != "" || !strings.Contains(fmt.Sprint(recent), id) {
		t.Fatalf("recover tasks: %+v, %s", recent, msg)
	}
	followup, msg := call(t, cs, "send_input", map[string]any{"task_id": id, "request_id": "dot-followup", "input": "Add a follow-up feature"})
	if msg != "" {
		t.Fatal(msg)
	}
	secondID := followup["task_id"].(string)
	if status := waitFinished(t, cs, secondID); status["status"] != "done" {
		t.Fatalf("follow-up: %+v", status)
	}
	second, msg := call(t, cs, "task_result", map[string]any{"task_id": secondID})
	if msg != "" || second["branch"] != result["branch"] {
		t.Fatalf("follow-up branch: %+v, %s", second, msg)
	}
	delivery = second["delivery"].(map[string]any)
	if applied, msg := call(t, cs, "apply_task", map[string]any{"task_id": secondID, "commit": delivery["commit"], "target_branch": delivery["target_branch"], "expected_head": delivery["target_head"]}); msg != "" || applied["applied"] != true {
		t.Fatalf("follow-up delivery: %+v, %s", applied, msg)
	}
	if _, err := os.Stat(filepath.Join(repo, "second.txt")); err != nil {
		t.Fatal("follow-up was not delivered")
	}
	// New work starts from a blank repository with automatic tests too.
	created, msg := call(t, cs, "create_project", map[string]any{"name": "new-app", "description": "A new local app"})
	if msg != "" || created["project"].(map[string]any)["test_profile"] != "auto" {
		t.Fatalf("create without setup: %+v, %s", created, msg)
	}
	newTask, msg := call(t, cs, "start_task", map[string]any{"repo": "new-app", "instructions": "Build the new application with tests"})
	if msg != "" {
		t.Fatal(msg)
	}
	if status := waitFinished(t, cs, newTask["task_id"].(string)); status["status"] != "done" {
		t.Fatalf("new application: %+v", status)
	}
}
