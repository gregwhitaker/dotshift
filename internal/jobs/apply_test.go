package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyReviewedResultAndFollowup(t *testing.T) {
	r, repo := setup(t, "goal_finish", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Complete the feature"})
	ctx := context.Background()
	d, err := r.Delivery(ctx, j.ID)
	if err != nil || !d.Ready || d.Applied {
		t.Fatalf("delivery: %+v, %v", d, err)
	}
	request := ApplyRequest{ID: j.ID, Commit: d.Commit, TargetBranch: d.TargetBranch, ExpectedHead: d.TargetHead}
	for range 2 {
		applied, err := r.Apply(ctx, request)
		if err != nil || !applied.Applied {
			t.Fatalf("apply/retry: %+v, %v", applied, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(repo, "hello.txt")); err != nil || string(got) != "finished\n" {
		t.Fatalf("working files: %q, %v", got, err)
	}
	if got := run(t, repo, "git", "rev-parse", "HEAD"); got != d.Commit {
		t.Fatal("applied an untested commit")
	}
	// The same branch still supports a follow-up after delivering its first result.
	next := submitAndWait(t, r, Request{Continue: j.ID, Prompt: "Check the feature again"})
	if next.Status != Done {
		t.Fatalf("follow-up: %+v", next)
	}
}

func TestApplyRefusesChangedOrUnreviewedWork(t *testing.T) {
	for _, scenario := range []string{"dirty", "untracked", "diverged", "result_changed", "wrong_head", "wrong_branch", "wrong_commit", "unreviewed", "revoked", "merge_in_progress"} {
		t.Run(scenario, func(t *testing.T) {
			r, repo := setup(t, "goal_finish", "test -f hello.txt")
			j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Complete the feature"})
			ctx := context.Background()
			d, err := r.Delivery(ctx, j.ID)
			if err != nil || !d.Ready {
				t.Fatalf("fixture: %+v, %v", d, err)
			}
			req := ApplyRequest{ID: j.ID, Commit: d.Commit, TargetBranch: d.TargetBranch, ExpectedHead: d.TargetHead}
			switch scenario {
			case "dirty":
				os.WriteFile(filepath.Join(repo, "README.md"), []byte("user edits\n"), 0o644)
			case "untracked":
				os.WriteFile(filepath.Join(repo, "personal.txt"), []byte("user notes\n"), 0o644)
			case "diverged":
				os.WriteFile(filepath.Join(repo, "personal.txt"), []byte("user notes\n"), 0o644)
				run(t, repo, "git", "add", ".")
				run(t, repo, "git", "commit", "--quiet", "-m", "Other work")
			case "result_changed":
				run(t, repo, "git", "branch", "-f", j.Branch, j.BaseCommit)
			case "wrong_head":
				req.ExpectedHead = strings.Repeat("0", 40)
			case "wrong_branch":
				req.TargetBranch = "another-branch"
			case "wrong_commit":
				req.Commit = j.BaseCommit
			case "unreviewed":
				j.Goal.Report.Status = "continue"
				if err := r.Store.Save(ctx, j); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				delete(r.Config.Repos, "demo")
			case "merge_in_progress":
				os.WriteFile(filepath.Join(repo, ".git", "MERGE_HEAD"), []byte(j.BaseCommit+"\n"), 0o644)
			}
			before := run(t, repo, "git", "status", "--porcelain")
			head := run(t, repo, "git", "rev-parse", "HEAD")
			if _, err := r.Apply(ctx, req); err == nil {
				t.Fatal("unsafe apply succeeded")
			}
			if got := run(t, repo, "git", "status", "--porcelain"); got != before {
				t.Fatalf("apply changed local edits: %s", got)
			}
			if got := run(t, repo, "git", "rev-parse", "HEAD"); got != head {
				t.Fatal("apply moved the original branch")
			}
		})
	}
}
