package jobs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttentionDismissalPersistsWithoutChangingWork(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r := &Runner{Store: store}
	for _, status := range []Status{Blocked, Failed, WaitingInput, Done} {
		t.Run(string(status), func(t *testing.T) {
			worktree := t.TempDir()
			file := filepath.Join(worktree, "partial.txt")
			if err := os.WriteFile(file, []byte("keep my work"), 0o600); err != nil {
				t.Fatal(err)
			}
			j := &Job{ID: string(status), Status: status, RunToken: "run-1", Error: "original issue", TestResult: "failed", TestOutput: "test evidence", Worktree: worktree, Branch: "dotshift/saved", Summary: "saved progress", FinishedAt: fromUnix(123456)}
			if status == Blocked {
				j.Goal = &GoalProgress{Phase: goalWorking}
			}
			if err := store.Save(ctx, j); err != nil {
				t.Fatal(err)
			}
			version := j.AttentionVersion()
			updated, err := r.SetAttentionDismissed(ctx, j.ID, version, true)
			if err != nil || !updated.AttentionDismissed() {
				t.Fatalf("dismiss: %+v %v", updated, err)
			}
			// A worker that read before the user's action must not erase it.
			if err := store.Save(ctx, j); err != nil {
				t.Fatal(err)
			}
			if j.Goal != nil {
				if err := store.saveGoal(ctx, j); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := OpenStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			saved, err := reopened.Get(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !saved.AttentionDismissed() || saved.Status != status || saved.Error != j.Error || saved.TestOutput != j.TestOutput || saved.Branch != j.Branch || saved.Worktree != worktree || saved.Summary != j.Summary {
				t.Fatalf("dismissal changed or lost work: %+v", saved)
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "keep my work" {
				t.Fatalf("lost work: %s %v", data, err)
			}
			list, err := reopened.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, listed := range list {
				if listed.ID == j.ID {
					found = listed.AttentionDismissed()
				}
			}
			if !found {
				t.Fatal("dismissed job disappeared from history or lost its acknowledgment")
			}
			updated, err = r.SetAttentionDismissed(ctx, j.ID, version, false)
			if err != nil || updated.AttentionDismissed() {
				t.Fatalf("restore: %+v %v", updated, err)
			}
		})
	}
}

func TestAttentionDismissalRejectsChangedIssue(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r := &Runner{Store: store}
	for _, change := range []struct {
		name  string
		apply func(*Job)
	}{
		{"resumed", func(j *Job) { j.RunToken = "new-run" }},
		{"running", func(j *Job) { j.Status = Running }},
		{"new error", func(j *Job) { j.Error = "another blocker" }},
		{"test result", func(j *Job) { j.TestResult = "failed" }},
		{"finished again", func(j *Job) { j.FinishedAt = fromUnix(456) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			before := &Job{ID: change.name, Status: Blocked, RunToken: "old-run", Error: "blocked", FinishedAt: fromUnix(123)}
			if err := store.Save(ctx, before); err != nil {
				t.Fatal(err)
			}
			if _, err := r.SetAttentionDismissed(ctx, before.ID, before.AttentionVersion(), true); err != nil {
				t.Fatal(err)
			}
			after := *before
			change.apply(&after)
			if err := store.Save(ctx, &after); err != nil {
				t.Fatal(err)
			}
			// This is the interleaving between the action's read and SQL update.
			for _, dismissed := range []bool{true, false} {
				if err := store.setAttentionDismissed(ctx, before, dismissed); !errors.Is(err, ErrAttentionChanged) {
					t.Fatalf("stale update: %v", err)
				}
			}
			if _, err := r.SetAttentionDismissed(ctx, before.ID, before.AttentionVersion(), true); !errors.Is(err, ErrAttentionChanged) {
				t.Fatalf("stale browser: %v", err)
			}
			saved, err := store.Get(ctx, after.ID)
			if err != nil || saved.AttentionDismissed() {
				t.Fatalf("new issue was hidden: %+v %v", saved, err)
			}
		})
	}
	for _, status := range []Status{Queued, Running, Cancelled, Done} {
		j := &Job{ID: "quiet-" + string(status), Status: status}
		if err := store.Save(ctx, j); err != nil {
			t.Fatal(err)
		}
		if _, err := r.SetAttentionDismissed(ctx, j.ID, "", true); !errors.Is(err, ErrAttentionChanged) {
			t.Fatalf("dismissed %s: %v", status, err)
		}
	}
}

func TestResumedGoalNeedsAttentionAgain(t *testing.T) {
	r, _ := setup(t, "goal_block", "test -f hello.txt")
	ctx := context.Background()
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Build the project"})
	if j.Status != Blocked {
		t.Fatalf("expected blocker: %+v", j)
	}
	version := j.AttentionVersion()
	if _, err := r.SetAttentionDismissed(ctx, j.ID, version, true); err != nil {
		t.Fatal(err)
	}
	// Resuming without answering the question produces the same blocker in a
	// new run. Its matching status/error must still require fresh attention.
	if _, err := r.ResumeGoal(ctx, ResumeGoalRequest{ID: j.ID}); err != nil {
		t.Fatal(err)
	}
	next := waitGoal(t, r, j.ID)
	if next.Status != Blocked || next.AttentionDismissed() || next.AttentionVersion() == version || next.Worktree != j.Worktree {
		t.Fatalf("new blocker did not reappear: %+v", next)
	}
	if _, err := r.SetAttentionDismissed(ctx, j.ID, version, true); !errors.Is(err, ErrAttentionChanged) {
		t.Fatalf("old action hid a new blocker: %v", err)
	}
}

func TestAttentionMigrationKeepsExistingJobs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldSchema := strings.Replace(schema, ",\n\tattention_dismissed TEXT NOT NULL DEFAULT ''", "", 1)
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	legacy := &Store{db: db}
	if err := legacy.Save(ctx, &Job{ID: "old-task", Status: Failed, Summary: "existing history"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	j, err := store.Get(ctx, "old-task")
	if err != nil || j.AttentionDismissed() || j.Summary != "existing history" {
		t.Fatalf("migration: %+v %v", j, err)
	}
	r := &Runner{Store: store}
	if updated, err := r.SetAttentionDismissed(ctx, j.ID, j.AttentionVersion(), true); err != nil || !updated.AttentionDismissed() {
		t.Fatalf("legacy dismissal: %+v %v", updated, err)
	}
}
