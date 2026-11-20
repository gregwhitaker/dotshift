package mcpintake

import (
	"context"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/jobs"
)

func TestFinishedLegacyWorkDoesNotInviteUnverifiedApplication(t *testing.T) {
	runner := newRunner(t, editScript)
	srv, _ := newTestServer(t, runner, "")
	client := connect(t, srv, "none")
	for _, result := range []string{"failed", "passed"} {
		j := &jobs.Job{ID: "legacy-" + result, Repo: "demo", Prompt: "Earlier work", Status: jobs.Done, TestResult: result, Commits: 1}
		if err := runner.Store.Save(context.Background(), j); err != nil {
			t.Fatal(err)
		}
		out, msg := call(t, client, "task_result", map[string]any{"task_id": j.ID})
		if msg != "" {
			t.Fatal(msg)
		}
		next := out["next"].(string)
		if out["delivery"] != nil || strings.Contains(next, "call apply_task") || (result == "failed" && !strings.Contains(next, "not ready to apply")) || (result == "passed" && !strings.Contains(next, "no recorded completion review")) {
			t.Fatalf("misleading completion handoff: %+v", out)
		}
	}
}
