package jobs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// git runs a git command in dir and returns its trimmed stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// commitAll commits everything the agent left uncommitted in the worktree.
// It reports whether there was anything to commit.
func commitAll(ctx context.Context, dir, message string) (bool, error) {
	status, err := git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if status == "" {
		return false, nil
	}
	if _, err := git(ctx, dir, "add", "--all"); err != nil {
		return false, err
	}
	args := []string{"commit", "--quiet", "-m", message}
	// Use the repo's own identity when it has one; otherwise git would refuse.
	if email, _ := git(ctx, dir, "config", "user.email"); email == "" {
		args = append([]string{"-c", "user.name=dotshift", "-c", "user.email=dotshift@localhost"}, args...)
	}
	if _, err := git(ctx, dir, args...); err != nil {
		return false, err
	}
	return true, nil
}
