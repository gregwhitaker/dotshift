package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestAutomaticValidationUsesRepositoryBuildSystem(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"make covers a mixed repo", map[string]string{"Makefile": ".PHONY: test\ntest:\n\tgo test ./...\n", "package.json": `{}`}, "make test"},
		{"node", map[string]string{"package.json": `{"scripts":{"test":"node --test"}}`}, "npm test"},
		{"pnpm", map[string]string{"package.json": `{"scripts":{"test":"vitest"},"packageManager":"pnpm@10.0.0"}`}, "pnpm test"},
		{"yarn", map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`, "yarn.lock": "# lock"}, "yarn test"},
		{"go", map[string]string{"go.mod": "module example.org/app\n"}, "go test ./..."},
		{"rust", map[string]string{"Cargo.toml": "[package]\nname = 'app'"}, "cargo test"},
		{"pytest", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\n"}, "python3 -m pytest"},
		{"unittest", map[string]string{"tests/test_app.py": "import unittest\n"}, "python3 -m unittest discover -s tests -v"},
		{"no tests", nil, ""},
		{"no node script", map[string]string{"package.json": `{}`}, ""},
		{"phony is not a test target", map[string]string{"Makefile": ".PHONY: test\n"}, ""},
		{"invalid manifest", map[string]string{"package.json": "{"}, ""},
		{"manager is not a shell command", map[string]string{"package.json": `{"scripts":{"test":"node --test"},"packageManager":"npm; touch hacked"}`}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, text := range tc.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := validationCommand(dir, "auto")
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("command = %q, %v; want %q", got, err, tc.want)
			}
			if got, err := validationCommand(dir, "./custom-validation"); err != nil || got != "./custom-validation" {
				t.Fatal("automatic detection replaced an explicit validation command")
			}
		})
	}
}

func TestWorkspaceDiscoveryRunsWithoutRegistrationOrPerRepoConfig(t *testing.T) {
	r, repo := setup(t, "goal_fix", "")
	root := filepath.Dir(repo)
	r.Config.Repos = nil
	r.Config.Projects = config.Projects{Root: root, Discover: true, TestProfiles: map[string]string{"auto": "auto"}}
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("test:\n\ttest \"$$(cat hello.txt)\" = fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", "Makefile")
	run(t, repo, "git", "commit", "--quiet", "-m", "Add validation")
	ctx := context.Background()
	repos, err := r.Repos(ctx)
	if err != nil || len(repos) != 1 || repos[0].TestCommand != "auto" {
		t.Fatalf("discovery: %+v, %v", repos, err)
	}
	j := submitAndWait(t, r, Request{Prompt: "Fix the application", MaxIterations: 3})
	if j.Status != Done || j.Goal.Iterations != 2 || j.Goal.TestCommand != "make test" {
		t.Fatalf("automatic workflow: %+v, %+v", j, j.Goal)
	}
	if entries, err := r.Store.projects(ctx, realPath(root)); err != nil || len(entries) != 0 {
		t.Fatal("discovery required catalog mutation")
	}
	r.Config.Projects.Discover = false
	if _, err := r.Submit(ctx, Request{Repo: repos[0].Name, Prompt: "More work"}); err == nil {
		t.Fatal("discovery stayed enabled after revocation")
	}
}

func TestAutomaticValidationFailureReturnsToCoding(t *testing.T) {
	r, _ := setup(t, "goal_finish", "auto")
	j := submitAndWait(t, r, Request{Repo: "demo", Prompt: "Build without a test target", MaxIterations: 2})
	if j.Status != Blocked || j.TestResult != "failed" || j.Goal.Iterations != 2 || !strings.Contains(j.TestOutput, "no test command") {
		t.Fatalf("missing tests bypassed validation: %+v", j)
	}
}

func TestWorkspaceDiscoveryDoesNotFollowSymlinksOrReviveRevokedProfiles(t *testing.T) {
	r := projectRunner(t)
	r.Config.Projects.Discover = true
	p, err := r.CreateProject(context.Background(), ProjectRequest{Name: "app", TestProfile: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p.Path, filepath.Join(p.root, "linked")); err != nil {
		t.Fatal(err)
	}
	delete(r.Config.Projects.TestProfiles, "hello")
	r.Config.Projects.TestProfiles["auto"] = "auto"
	repos, err := r.Repos(context.Background())
	if err != nil || len(repos) != 0 {
		t.Fatalf("revoked or linked repo rediscovered: %+v, %v", repos, err)
	}
}
