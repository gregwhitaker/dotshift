package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
)

// The test binary doubles as the fake agent: when DOTSHIFT_FAKE_AGENT is set
// it behaves like a coding agent instead of running tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("DOTSHIFT_FAKE_AGENT"); mode != "" {
		os.Exit(fakeAgent(mode))
	}
	os.Exit(m.Run())
}

func fakeAgent(mode string) int {
	if os.Getenv(APIKeyEnv) != "sk-test" {
		fmt.Println("fake agent: missing router key")
		return 9
	}
	prompt, _ := readAll(os.Stdin)
	fmt.Println("fake agent got:", strings.SplitN(prompt, "\n", 2)[0])
	summary := os.Getenv("FAKE_SUMMARY")
	switch mode {
	case "edit":
		os.WriteFile("hello.txt", []byte("hello from the agent\n"), 0o644)
		os.WriteFile(summary, []byte("Added hello.txt.\n"), 0o644)
	case "noop":
		os.WriteFile(summary, []byte("Nothing to change.\n"), 0o644)
	case "fail":
		os.WriteFile("partial.txt", []byte("half done\n"), 0o644)
		return 3
	case "sleep":
		time.Sleep(time.Minute)
	}
	return 0
}

func readAll(f *os.File) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String(), nil
		}
	}
}

type fake struct{ mode string }

func (f fake) Name() string { return "fake" }

func (f fake) Command(ctx context.Context, t agent.Task) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Dir = t.Dir
	cmd.Env = append(os.Environ(), "DOTSHIFT_FAKE_AGENT="+f.mode, "FAKE_SUMMARY="+t.LastMessageFile)
	return cmd
}

// setup makes a repo with one commit and a runner allowlisting it.
func setup(t *testing.T, mode string, test string) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	run(t, "", "git", "init", "--quiet", "-b", "main", repo)
	run(t, repo, "git", "config", "user.email", "greg@example.com")
	run(t, repo, "git", "config", "user.name", "Greg")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# repo\n"), 0o644)
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "--quiet", "-m", "init")

	yaml := fmt.Sprintf("providers:\n  openai: {}\naliases:\n  smart: [openai/gpt-5]\n  fast: [openai/gpt-5-mini]\nrepos:\n  demo:\n    path: %s\n    test: %q\njobs:\n  timeout: 30s\n", repo, test)
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	os.MkdirAll(state, 0o700)
	store, err := OpenStore(filepath.Join(state, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &Runner{Config: cfg, Store: store, Agent: fake{mode}, BaseURL: "http://127.0.0.1:4000/v1", APIKey: "sk-test", StateDir: state}, repo
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func submitAndWait(t *testing.T, r *Runner, req Request) *Job {
	t.Helper()
	ctx := context.Background()
	j, err := r.Submit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	j, err = r.Wait(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestJobCommitsToBranchWithoutTouchingWorkingCopy(t *testing.T) {
	r, repo := setup(t, "edit", "test -f hello.txt")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Add a hello file\nwith a greeting"})

	if j.Status != Done {
		t.Fatalf("status = %s (%s)", j.Status, j.Error)
	}
	if j.Model != "smart" || j.Branch != "dotshift/"+j.ID || j.Commits != 1 {
		t.Errorf("model %q branch %q commits %d", j.Model, j.Branch, j.Commits)
	}
	if j.Summary != "Added hello.txt." || !strings.Contains(j.DiffStat, "hello.txt") {
		t.Errorf("summary %q diffstat %q", j.Summary, j.DiffStat)
	}
	if j.TestResult != "passed" {
		t.Errorf("test result %q: %s", j.TestResult, j.TestOutput)
	}
	if j.Worktree != "" {
		t.Errorf("worktree %q was kept", j.Worktree)
	}
	// The branch has the work, committed as the repo's own user...
	if got := run(t, repo, "git", "show", j.Branch+":hello.txt"); got != "hello from the agent" {
		t.Errorf("branch file = %q", got)
	}
	if got := run(t, repo, "git", "log", "-1", "--format=%an %s", j.Branch); got != "Greg dotshift: Add a hello file" {
		t.Errorf("commit = %q", got)
	}
	// ...and the main working copy is untouched.
	if got := run(t, repo, "git", "status", "--porcelain"); got != "" {
		t.Errorf("working copy changed:\n%s", got)
	}
	if got := run(t, repo, "git", "branch", "--show-current"); got != "main" {
		t.Errorf("current branch = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); err == nil {
		t.Error("hello.txt appeared in the working copy")
	}
	log, _ := os.ReadFile(j.LogPath)
	if !strings.Contains(string(log), "fake agent got: Add a hello file") {
		t.Errorf("log = %q", log)
	}
}

func TestJobByPathModelAndBranch(t *testing.T) {
	r, repo := setup(t, "edit", "")
	j := submitAndWait(t, r, Request{Repo: repo, Prompt: "x", Model: "fast", Branch: "feature/hello"})
	if j.Status != Done || j.Repo != "demo" || j.Model != "fast" || j.Branch != "feature/hello" || j.TestResult != "" {
		t.Fatalf("job = %+v", j)
	}
	run(t, repo, "git", "rev-parse", "--verify", "feature/hello")
}

func TestNoChangesDeletesBranch(t *testing.T) {
	r, repo := setup(t, "noop", "")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "look around"})
	if j.Status != Done || j.Commits != 0 || j.Summary != "Nothing to change." {
		t.Fatalf("job = %+v", j)
	}
	if out := run(t, repo, "git", "branch", "--list", j.Branch); out != "" {
		t.Errorf("empty branch kept: %q", out)
	}
}

func TestFailedJobKeepsWorktreeUntilCleaned(t *testing.T) {
	r, repo := setup(t, "fail", "")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "break"})
	if j.Status != Failed || j.ExitCode != 3 || !strings.Contains(j.Error, "exited") {
		t.Fatalf("job = %+v", j)
	}
	if _, err := os.Stat(filepath.Join(j.Worktree, "partial.txt")); err != nil {
		t.Fatalf("worktree not kept: %v", err)
	}
	j, err := r.Clean(context.Background(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.StateDir, "worktrees", j.ID)); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if out := run(t, repo, "git", "branch", "--list", j.Branch); out != "" {
		t.Errorf("empty branch kept: %q", out)
	}
}

func TestCancel(t *testing.T) {
	r, _ := setup(t, "sleep", "")
	ctx := context.Background()
	j, err := r.Submit(ctx, Request{Repo: "demo", Prompt: "take forever"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, _ := r.Get(ctx, j.ID)
		if cur.Status == Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never started: %+v", cur)
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	if err := r.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = r.Wait(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != Cancelled || time.Since(start) > 5*time.Second {
		t.Fatalf("status %s after %s", j.Status, time.Since(start))
	}
	if err := r.Cancel(ctx, j.ID); err == nil {
		t.Error("cancelling a finished job should fail")
	}
}

func TestCancelFromAnotherProcess(t *testing.T) {
	r, _ := setup(t, "sleep", "")
	ctx := context.Background()
	j, err := r.Submit(ctx, Request{Repo: "demo", Prompt: "take forever"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		cur, _ := r.Get(ctx, j.ID)
		if cur.Status == Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A second runner sharing the store, as `dotshift cancel` would have.
	other := &Runner{Config: r.Config, Store: r.Store, StateDir: r.StateDir}
	if err := other.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = r.Wait(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != Cancelled {
		t.Fatalf("status = %s (%s)", j.Status, j.Error)
	}
}

func TestSubmitRejects(t *testing.T) {
	r, repo := setup(t, "edit", "")
	run(t, repo, "git", "branch", "taken")
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"not allowlisted", Request{Repo: t.TempDir(), Prompt: "x"}, "not allowlisted"},
		{"unknown name", Request{Repo: "other", Prompt: "x"}, "allowed: demo"},
		{"no prompt", Request{Repo: "demo", Prompt: "  "}, "no instructions"},
		{"bad model", Request{Repo: "demo", Prompt: "x", Model: "huge"}, `model "huge" is not an alias`},
		{"branch exists", Request{Repo: "demo", Prompt: "x", Branch: "taken"}, "already exists"},
		{"bad branch", Request{Repo: "demo", Prompt: "x", Branch: "a..b"}, "not a valid branch"},
		{"bad base", Request{Repo: "demo", Prompt: "x", Base: "nope"}, "not a commit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Submit(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	if list, _ := r.List(context.Background()); len(list) != 0 {
		t.Errorf("rejected requests were recorded: %d", len(list))
	}
}

func TestWorktreeDirInsideRepoRejected(t *testing.T) {
	r, repo := setup(t, "edit", "")
	r.Config.Jobs.WorktreeDir = filepath.Join(repo, ".worktrees")
	_, err := r.Submit(context.Background(), Request{Repo: "demo", Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "inside demo's working copy") {
		t.Fatalf("err = %v", err)
	}
}

func TestReconcileDeadRunner(t *testing.T) {
	r, _ := setup(t, "edit", "")
	ctx := context.Background()
	j := &Job{ID: "dead", Repo: "demo", Status: Running, RunnerPID: 999999999, CreatedAt: time.Now()}
	if err := r.Store.Save(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "dead")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Failed || !strings.Contains(got.Error, "stopped") {
		t.Fatalf("job = %+v", got)
	}
}
