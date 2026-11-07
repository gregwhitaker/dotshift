package mcpintake

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestProjectCreationAndGoalOverOAuth(t *testing.T) {
	const script = `cat > /dev/null
if [ "$READ_ONLY" != true ]; then
  if [ -f hello.txt ]; then echo fixed > hello.txt; else echo broken > hello.txt; fi
fi
printf '%s' '{"status":"complete","summary":"Implemented the project goal.","next_steps":""}' > "$SUMMARY"`
	runner := newRunner(t, script)
	runner.Config.Repos = nil
	runner.Config.Projects = config.Projects{Root: filepath.Join(t.TempDir(), "workspace"), TestProfiles: map[string]string{"hello": `test "$(cat hello.txt)" = fixed`}}
	t.Cleanup(func() { runner.Shutdown(context.Background(), "test finished") })
	srv, _ := newTestServer(t, runner, "")
	cs := connect(t, srv, "none")
	catalog, msg := call(t, cs, "list_repos", nil)
	if msg != "" {
		t.Fatal(msg)
	}
	options := catalog["projects"].(map[string]any)
	if options["enabled"] != true || !strings.Contains(fmt.Sprint(options["test_profiles"]), "hello") || len(catalog["repos"].([]any)) != 0 {
		t.Fatalf("discovery = %+v", catalog)
	}
	if _, msg := call(t, cs, "create_project", map[string]any{"name": "../escape", "test_profile": "hello"}); msg == "" {
		t.Fatal("accepted a path instead of a name")
	}
	created, msg := call(t, cs, "create_project", map[string]any{"name": "new-app", "description": "A useful application", "test_profile": "hello"})
	if msg != "" || created["project"].(map[string]any)["name"] != "new-app" || !strings.Contains(fmt.Sprint(created["next"]), "start_goal") {
		t.Fatalf("create_project = %+v, %s", created, msg)
	}
	// Registration is also exposed and can safely be retried.
	if _, msg := call(t, cs, "register_project", map[string]any{"name": "new-app", "test_profile": "hello"}); msg != "" {
		t.Fatal(msg)
	}
	catalog, msg = call(t, cs, "list_repos", nil)
	if msg != "" || len(catalog["repos"].([]any)) != 1 {
		t.Fatalf("project not immediately visible: %+v, %s", catalog, msg)
	}
	// Auto-selection works with a managed repo, even with an empty static allowlist.
	started, msg := call(t, cs, "start_goal", map[string]any{"goal": "Build the application so hello.txt says fixed", "max_iterations": 3})
	if msg != "" {
		t.Fatal(msg)
	}
	id := started["task_id"].(string)
	status := waitFinished(t, cs, id)
	if status["status"] != "done" {
		t.Fatalf("goal = %+v", status)
	}
	result, msg := call(t, cs, "task_result", map[string]any{"task_id": id, "include_diff": true})
	if msg != "" || result["tests"] != "passed" || result["commits"] != 2.0 || !strings.Contains(fmt.Sprint(result["diff"]), "+fixed") {
		t.Fatalf("goal result = %+v, %s", result, msg)
	}
}

func TestProjectToolsExplainDisabledConfiguration(t *testing.T) {
	srv, _ := newTestServer(t, newRunner(t, editScript), "")
	cs := connect(t, srv, "none")
	catalog, msg := call(t, cs, "list_repos", nil)
	if msg != "" || catalog["projects"].(map[string]any)["enabled"] != false {
		t.Fatalf("catalog = %+v, %s", catalog, msg)
	}
	for _, tool := range []string{"create_project", "register_project"} {
		if _, msg := call(t, cs, tool, map[string]any{"name": "new-app", "test_profile": "go"}); !strings.Contains(msg, "projects.root") {
			t.Fatalf("%s: %s", tool, msg)
		}
	}
}
