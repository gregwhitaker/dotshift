package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
)

// ProjectOptions describes the local policy clients discover in list_repos.
type ProjectOptions struct {
	Enabled      bool          `json:"enabled"`
	Root         string        `json:"root,omitempty"`
	TestProfiles []TestProfile `json:"test_profiles"`
}

type TestProfile struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

type ProjectRequest struct {
	Name        string
	Description string
	TestProfile string
}

// Project is a repository registered in the durable project catalog.
type Project struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	TestProfile string `json:"test_profile"`
	TestCommand string `json:"test_command"`
	root        string
}

var projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func (r *Runner) ProjectOptions() ProjectOptions {
	out := ProjectOptions{TestProfiles: []TestProfile{}}
	if r.Config.Projects.Root == "" {
		return out
	}
	out.Enabled = true
	out.Root = realPath(expandHome(r.Config.Projects.Root))
	for name, command := range r.Config.Projects.TestProfiles {
		out.TestProfiles = append(out.TestProfiles, TestProfile{Name: name, Command: command})
	}
	sort.Slice(out.TestProfiles, func(i, j int) bool { return out.TestProfiles[i].Name < out.TestProfiles[j].Name })
	return out
}

// allowedRepos combines the static allowlist with projects still permitted by
// the current root and test profiles. Config stays immutable while jobs run.
func (r *Runner) allowedRepos(ctx context.Context) (map[string]config.Repo, error) {
	out := make(map[string]config.Repo, len(r.Config.Repos))
	for name, repo := range r.Config.Repos {
		out[name] = repo
	}
	options := r.ProjectOptions()
	if !options.Enabled {
		return out, nil
	}
	projects, err := r.Store.projects(ctx, options.Root)
	if err != nil {
		return nil, fmt.Errorf("read project catalog: %w", err)
	}
	for _, p := range projects {
		command := r.Config.Projects.TestProfiles[p.TestProfile]
		if _, exists := out[p.Name]; exists || strings.TrimSpace(command) == "" {
			continue
		}
		// Removing/changing the root or a profile revokes the managed entry.
		// Replacing either the checkout or .git with a symlink does too.
		if err := managedProjectCheckout(ctx, p); err != nil {
			continue
		}
		out[p.Name] = config.Repo{Path: p.Path, Test: command}
	}
	return out, nil
}

func managedProjectCheckout(ctx context.Context, p Project) error {
	if err := managedProjectPath(p); err != nil {
		return err
	}
	top, err := git(ctx, p.Path, "rev-parse", "--show-toplevel")
	if err != nil || realPath(top) != p.Path {
		return fmt.Errorf("%s must be the top-level directory of its git repository", p.Path)
	}
	return nil
}

func managedProjectPath(p Project) error {
	if !projectNamePattern.MatchString(p.Name) || p.Path != filepath.Join(p.root, p.Name) || realPath(p.root) != p.root {
		return errors.New("project is outside the configured root")
	}
	for _, dir := range []string{p.Path, filepath.Join(p.Path, ".git")} {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must be a directory, not a symlink or linked worktree", dir)
		}
	}
	return nil
}

func (r *Runner) projectRequest(ctx context.Context, req ProjectRequest) (*Project, error) {
	if r.stopping {
		return nil, errors.New("dotshift is stopping")
	}
	options := r.ProjectOptions()
	if !options.Enabled {
		return nil, errors.New("project creation and registration are disabled; configure projects.root and projects.test_profiles in dotshift.yaml")
	}
	if !projectNamePattern.MatchString(req.Name) {
		return nil, errors.New("project name must be 1-64 lowercase letters, digits, - or _, starting with a letter or digit; paths are not accepted")
	}
	command, ok := r.Config.Projects.TestProfiles[req.TestProfile]
	if !ok || strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("unknown test_profile %q; choose one from list_repos.projects.test_profiles", req.TestProfile)
	}
	p := &Project{Name: req.Name, root: options.Root, Path: filepath.Join(options.Root, req.Name), TestProfile: req.TestProfile, TestCommand: command}
	for name, repo := range r.Config.Repos {
		if name == req.Name || realPath(expandHome(repo.Path)) == p.Path {
			return nil, fmt.Errorf("project is already configured as repos.%s; use that repo", name)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

// projectRoot refuses to place independent repositories inside another repo.
// The root is set locally; clients cannot supply an arbitrary destination.
func projectRoot(ctx context.Context, root string, create bool) error {
	if create {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || realPath(root) != root {
		return errors.New("projects.root must resolve to a directory")
	}
	if _, err := git(ctx, root, "rev-parse", "--absolute-git-dir"); err == nil {
		return errors.New("projects.root is inside a git repository; choose a workspace folder outside repositories")
	}
	return ctx.Err()
}

// CreateProject bootstraps an empty repository, then durably registers it.
// It makes no model calls and never overwrites an existing directory.
func (r *Runner) CreateProject(ctx context.Context, req ProjectRequest) (*Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := r.projectRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(req.Description) > 16000 {
		return nil, errors.New("project description must be at most 16000 bytes; put the full objective in start_goal")
	}
	if exists, err := r.Store.projectExists(ctx, p.Name); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("project %q is already registered; use list_repos", p.Name)
	}
	if err := projectRoot(ctx, p.root, true); err != nil {
		return nil, err
	}
	// Mkdir is the cross-process collision check. Never remove or reuse an
	// existing path, including an empty directory or dangling symlink.
	if err := os.Mkdir(p.Path, 0o755); err != nil {
		return nil, fmt.Errorf("create %s (existing folders are never overwritten; use register_project for an existing git repo): %w", p.Path, err)
	}
	if err := bootstrapProject(ctx, p, req.Description); err != nil {
		// Keep partial setup for inspection/recovery; never delete user data on
		// a failed request or a concurrent change to the new directory.
		return nil, fmt.Errorf("created %s but initialization failed; directory kept: %w", p.Path, err)
	}
	if err := r.Store.registerProject(ctx, *p); err != nil {
		return nil, fmt.Errorf("created %s but registration failed; retry with register_project: %w", p.Path, err)
	}
	return p, nil
}

func bootstrapProject(ctx context.Context, p *Project, description string) error {
	// Avoid inherited templates, hooks and signing during the seed commit.
	if _, err := git(ctx, p.Path, "init", "--quiet", "--template=", "-b", "main"); err != nil {
		return err
	}
	readme := "# " + p.Name + "\n"
	if description = strings.TrimSpace(description); description != "" {
		readme += "\n" + description + "\n"
	}
	readme += "\nCreated with dotshift. The project goal will add the implementation and tests.\n"
	f, err := os.OpenFile(filepath.Join(p.Path, "README.md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(readme)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	files := []string{"README.md"}
	if p.TestProfile == "python" {
		if err := os.WriteFile(filepath.Join(p.Path, ".gitignore"), []byte("__pycache__/\n*.py[cod]\n.pytest_cache/\n.venv/\n"), 0o644); err != nil {
			return err
		}
		files = append(files, ".gitignore")
	}
	if _, err := git(ctx, p.Path, append([]string{"add", "--"}, files...)...); err != nil {
		return err
	}
	_, err = git(ctx, p.Path, "-c", "core.hooksPath="+os.DevNull, "-c", "commit.gpgSign=false", "-c", "user.name=dotshift", "-c", "user.email=dotshift@localhost", "commit", "--quiet", "-m", "Initialize project")
	return err
}

// RegisterProject accepts an existing committed repo directly under the root.
// It does not stage, commit, or change any of the user's existing files.
func (r *Runner) RegisterProject(ctx context.Context, req ProjectRequest) (*Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := r.projectRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := projectRoot(ctx, p.root, false); err != nil {
		return nil, err
	}
	if err := managedProjectCheckout(ctx, *p); err != nil {
		return nil, fmt.Errorf("cannot register %s: %w", p.Path, err)
	}
	if _, err := git(ctx, p.Path, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return nil, errors.New("commit the project's initial files before registering it; jobs start from committed code")
	}
	if err := r.Store.registerProject(ctx, *p); err != nil {
		return nil, err
	}
	return p, nil
}
