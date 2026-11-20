package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Delivery describes whether a reviewed result can be brought into the user's
// checkout. Inspection never changes Git or the working files.
type Delivery struct {
	TaskID       string `json:"task_id"`
	Commit       string `json:"commit"`
	TargetBranch string `json:"target_branch"`
	TargetHead   string `json:"target_head"`
	Ready        bool   `json:"ready"`
	Applied      bool   `json:"applied"`
	Blocker      string `json:"blocker,omitempty"`
	BlockerCode  string `json:"blocker_code,omitempty"`
}

type ApplyRequest struct {
	ID           string
	Commit       string
	TargetBranch string
	ExpectedHead string
}

func (r *Runner) Delivery(ctx context.Context, id string) (*Delivery, error) {
	j, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &Delivery{TaskID: id}
	if err := r.inspectDelivery(ctx, j, d); err != nil {
		d.Blocker = err.Error()
		if d.BlockerCode == "" {
			d.BlockerCode = "unavailable"
		}
	}
	return d, nil
}

func (r *Runner) inspectDelivery(ctx context.Context, j *Job, d *Delivery) error {
	if !Reviewed(j) {
		d.BlockerCode = "unreviewed"
		return errors.New("only a completed, tested and independently reviewed result can be applied")
	}
	d.Commit = j.Goal.TestedCommit
	repos, err := r.allowedReposNamed(ctx, j.Repo)
	if err != nil {
		return err
	}
	repo, ok := repos[j.Repo]
	if !ok {
		d.BlockerCode = "checkout_changed"
		return errors.New("the repository is no longer authorized")
	}
	top, err := git(ctx, expandHome(repo.Path), "rev-parse", "--show-toplevel")
	if err != nil || realPath(top) != realPath(j.RepoPath) {
		d.BlockerCode = "checkout_changed"
		return errors.New("the authorized repository path changed")
	}
	d.TargetBranch, err = git(ctx, j.RepoPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		d.BlockerCode = "detached"
		return errors.New("the original checkout has a detached HEAD; select the intended branch before applying")
	}
	d.TargetHead, err = git(ctx, j.RepoPath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	// This also makes a retry after a lost response idempotent, including when
	// more work has subsequently been committed on the destination branch.
	if _, err := git(ctx, j.RepoPath, "merge-base", "--is-ancestor", d.Commit, d.TargetHead); err == nil {
		d.Applied = true
		return nil
	}
	tip, err := git(ctx, j.RepoPath, "rev-parse", "--verify", "refs/heads/"+j.Branch+"^{commit}")
	if err != nil || tip != d.Commit {
		d.BlockerCode = "result_changed"
		return errors.New("the result branch changed after review; finish and retrieve the latest task before applying")
	}
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		path, err := git(ctx, j.RepoPath, "rev-parse", "--git-path", marker)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(j.RepoPath, path)
		}
		if _, err := os.Stat(path); err == nil {
			d.BlockerCode = "git_in_progress"
			return errors.New("the original checkout has a Git operation in progress; preserve it and resolve that operation first")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	status, err := git(ctx, j.RepoPath, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	if status != "" {
		d.BlockerCode = "local_changes"
		return errors.New("the original checkout has local changes; preserve them and ask the user how to proceed, without stashing, resetting, or committing their work automatically")
	}
	if _, err := git(ctx, j.RepoPath, "merge-base", "--is-ancestor", d.TargetHead, d.Commit); err != nil {
		d.BlockerCode = "diverged"
		return errors.New("the destination branch has diverged; use start_task with base set to the current destination commit and instructions to integrate the earlier result, then test and review that task before applying")
	}
	d.Ready = true
	return nil
}

// Apply explicitly delivers an already-reviewed result. It only fast-forwards
// the named clean checkout; it never switches branches, resets, stashes or pushes.
func (r *Runner) Apply(ctx context.Context, req ApplyRequest) (*Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if r.stopping {
		return nil, errors.New("dotshift is stopping")
	}
	d, err := r.Delivery(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if req.Commit == "" || req.Commit != d.Commit || req.TargetBranch == "" || req.TargetBranch != d.TargetBranch {
		return nil, errors.New("result or destination changed; retrieve task_result and use its delivery commit and target_branch")
	}
	if d.Applied {
		return d, nil
	}
	if !d.Ready {
		return nil, errors.New(d.Blocker)
	}
	if req.ExpectedHead == "" || req.ExpectedHead != d.TargetHead {
		return nil, errors.New("destination HEAD changed; retrieve task_result before applying")
	}
	j, err := r.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if _, err := git(ctx, j.RepoPath, "-c", "core.hooksPath="+os.DevNull, "-c", "merge.autoStash=false", "merge", "--ff-only", "--no-edit", "--", d.Commit); err != nil {
		return nil, fmt.Errorf("could not fast-forward reviewed work: %w", err)
	}
	return r.Delivery(ctx, req.ID)
}
