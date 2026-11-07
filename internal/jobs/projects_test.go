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

func projectRunner(t *testing.T) *Runner {
	t.Helper()
	r, _ := setup(t, "goal_fix", "")
	r.Config.Repos = nil
	r.Config.Projects = config.Projects{Root: filepath.Join(t.TempDir(), "projects"), TestProfiles: map[string]string{"hello": `test "$(cat hello.txt)" = fixed`}}
	return r
}

func TestCreatedProjectPersistsAndCompletesGoal(t *testing.T) {
	r := projectRunner(t)
	ctx := context.Background()
	p, err := r.CreateProject(ctx, ProjectRequest{Name: "new-app", TestProfile: "hello", Description: "A new application."})
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, p.Path, "git", "branch", "--show-current"); got != "main" {
		t.Fatalf("branch = %s", got)
	}
	readme, err := os.ReadFile(filepath.Join(p.Path, "README.md"))
	if err != nil || !strings.Contains(string(readme), "A new application.") {
		t.Fatalf("README = %s, %v", readme, err)
	}
	// A fresh process uses the same durable catalog, without editing YAML.
	next := reopenGoalRunner(t, r, "goal_fix")
	repos, err := next.Repos(ctx)
	if err != nil || len(repos) != 1 || repos[0].Name != p.Name || !repos[0].HasTests || !repos[0].Managed {
		t.Fatalf("repos = %+v, %v", repos, err)
	}
	j, err := next.Submit(ctx, Request{Repo: p.Path, Prompt: "Build the application with tests", Goal: true, MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	result := waitGoal(t, next, j.ID)
	if result.Status != Done || result.TestResult != "passed" || result.Goal.Iterations != 2 || result.Goal.Phase != goalReviewing {
		t.Fatalf("result = %+v, goal = %+v", result, result.Goal)
	}
	if got := run(t, p.Path, "git", "show", j.Branch+":hello.txt"); got != "fixed" {
		t.Fatalf("branch result = %s", got)
	}
	if _, err := os.Stat(filepath.Join(p.Path, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("goal changed original working copy")
	}
}

func TestProjectCreationRefusesUnsafeOrExistingPaths(t *testing.T) {
	r := projectRunner(t)
	ctx := context.Background()
	for _, name := range []string{"", "..", "../escape", "/tmp/escape", "a/b", "-flags", "Upper", strings.Repeat("a", 65)} {
		if _, err := r.CreateProject(ctx, ProjectRequest{Name: name, TestProfile: "hello"}); err == nil {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
	if _, err := r.CreateProject(ctx, ProjectRequest{Name: "test", TestProfile: "rm -rf anything"}); err == nil {
		t.Fatal("accepted arbitrary test command")
	}
	root := r.Config.Projects.Root
	if err := os.MkdirAll(filepath.Join(root, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "existing", "keep.txt")
	os.WriteFile(marker, []byte("keep me"), 0o644)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"existing", "linked", "dangling"} {
		if _, err := r.CreateProject(ctx, ProjectRequest{Name: name, TestProfile: "hello"}); err == nil {
			t.Fatalf("overwrote %s", name)
		}
		if _, err := r.RegisterProject(ctx, ProjectRequest{Name: name, TestProfile: "hello"}); err == nil {
			t.Fatalf("registered non-repository/symlink %s", name)
		}
	}
	if contents, _ := os.ReadFile(marker); string(contents) != "keep me" {
		t.Fatal("existing data changed")
	}
	if contents, _ := os.ReadDir(outside); len(contents) != 0 {
		t.Fatal("wrote outside project root")
	}
	r.Config.Projects.Root = ""
	if _, err := r.CreateProject(ctx, ProjectRequest{Name: "disabled", TestProfile: "hello"}); err == nil {
		t.Fatal("creation enabled without a root")
	}
	if _, err := r.RegisterProject(ctx, ProjectRequest{Name: "disabled", TestProfile: "hello"}); err == nil {
		t.Fatal("registration enabled without a root")
	}
}

func TestProjectConcurrentCreationAcrossRunners(t *testing.T) {
	r := projectRunner(t)
	next := reopenGoalRunner(t, r, "goal_fix")
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, runner := range []*Runner{r, next} {
		wg.Add(1)
		go func(runner *Runner) {
			defer wg.Done()
			<-start
			_, err := runner.CreateProject(context.Background(), ProjectRequest{Name: "same", TestProfile: "hello"})
			errs <- err
		}(runner)
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful creations = %d", successes)
	}
	repos, err := r.Repos(context.Background())
	if err != nil || len(repos) != 1 {
		t.Fatalf("catalog = %+v, %v", repos, err)
	}
	run(t, repos[0].Path, "git", "rev-parse", "--verify", "HEAD")
}

func TestRegisterProjectPreservesChangesAndRejectsProfileReplacement(t *testing.T) {
	r := projectRunner(t)
	ctx := context.Background()
	repo := filepath.Join(r.Config.Projects.Root, "existing")
	run(t, "", "git", "init", "--quiet", "-b", "main", repo)
	req := ProjectRequest{Name: "existing", TestProfile: "hello"}
	if _, err := r.RegisterProject(ctx, req); err == nil || !strings.Contains(err.Error(), "initial files") {
		t.Fatalf("empty repo: %v", err)
	}
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("original\n"), 0o644)
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "init")
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("uncommitted\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("untracked\n"), 0o644)
	status := run(t, repo, "git", "status", "--porcelain")
	for i := 0; i < 2; i++ {
		if _, err := r.RegisterProject(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if got := run(t, repo, "git", "status", "--porcelain"); got != status {
		t.Fatalf("working copy changed: %s", got)
	}
	r.Config.Projects.TestProfiles["other"] = "go test ./..."
	if _, err := r.RegisterProject(ctx, ProjectRequest{Name: "existing", TestProfile: "other"}); err == nil {
		t.Fatal("silently replaced registered test profile")
	}
	if got := run(t, repo, "git", "show", "HEAD:hello.txt"); got != "original" {
		t.Fatalf("committed user changes: %s", got)
	}
}

func TestManagedProjectAccessRevalidated(t *testing.T) {
	for _, change := range []string{"root_disabled", "root_changed", "profile_removed", "project_symlink", "git_symlink", "git_redirect"} {
		t.Run(change, func(t *testing.T) {
			r := projectRunner(t)
			ctx := context.Background()
			p, err := r.CreateProject(ctx, ProjectRequest{Name: "app", TestProfile: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "root_disabled":
				r.Config.Projects.Root = ""
			case "root_changed":
				r.Config.Projects.Root = t.TempDir()
			case "profile_removed":
				delete(r.Config.Projects.TestProfiles, "hello")
			case "project_symlink", "git_symlink":
				source := p.Path
				if change == "git_symlink" {
					source = filepath.Join(source, ".git")
				}
				destination := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(source, destination); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, source); err != nil {
					t.Fatal(err)
				}
			case "git_redirect":
				run(t, p.Path, "git", "config", "core.worktree", t.TempDir())
			}
			if _, err := r.Submit(ctx, Request{Repo: "app", Prompt: "work", Goal: true}); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
				t.Fatalf("access was not revoked: %v", err)
			}
		})
	}
}

func TestProjectRootCannotNestRepositories(t *testing.T) {
	r, repo := setup(t, "edit", "")
	r.Config.Projects = config.Projects{Root: repo, TestProfiles: map[string]string{"go": "go test ./..."}}
	if _, err := r.CreateProject(context.Background(), ProjectRequest{Name: "nested", TestProfile: "go"}); err == nil || !strings.Contains(err.Error(), "inside a git repository") {
		t.Fatalf("nested repo: %v", err)
	}
}
