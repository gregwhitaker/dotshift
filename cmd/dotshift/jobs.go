package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gregwhitaker/dotshift/internal/jobs"
)

func init() {
	commands["jobs"] = command("jobs", nil, func(ctx context.Context, configPath string, fs *flag.FlagSet) error {
		runner, _, closeStore, err := openRunner(configPath)
		if err != nil {
			return err
		}
		defer closeStore()
		if id := fs.Arg(0); id != "" {
			j, err := runner.Get(ctx, id)
			if err != nil {
				return err
			}
			printJob(os.Stdout, j)
			return nil
		}
		list, err := runner.List(ctx)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No jobs yet. Start one with `dotshift run`.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tREPO\tMODEL\tBRANCH\tCOMMITS\tTESTS\tSTARTED\tINSTRUCTIONS")
		for _, j := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", j.ID, j.Status, j.Repo, j.Model, j.Branch,
				j.Commits, orDash(j.TestResult), ago(j.CreatedAt), firstLine(j.Prompt, 50))
		}
		return w.Flush()
	})

	commands["logs"] = withJob("logs", func(ctx context.Context, runner *jobs.Runner, j *jobs.Job) error {
		f, err := os.Open(j.LogPath)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(os.Stdout, f)
		return err
	})

	commands["cancel"] = withJob("cancel", func(ctx context.Context, runner *jobs.Runner, j *jobs.Job) error {
		if err := runner.Cancel(ctx, j.ID); err != nil {
			return err
		}
		fmt.Printf("Cancelled job %s.\n", j.ID)
		return nil
	})

	commands["clean"] = withJob("clean", func(ctx context.Context, runner *jobs.Runner, j *jobs.Job) error {
		if j.Worktree == "" {
			fmt.Printf("Job %s has no worktree left.\n", j.ID)
			return nil
		}
		if _, err := runner.Clean(ctx, j.ID); err != nil {
			return err
		}
		fmt.Printf("Removed %s.\n", j.Worktree)
		return nil
	})
}

// withJob builds a command that takes one job ID.
func withJob(name string, body func(ctx context.Context, runner *jobs.Runner, j *jobs.Job) error) func([]string) int {
	return command(name, nil, func(ctx context.Context, configPath string, fs *flag.FlagSet) error {
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: dotshift %s JOB_ID", name)
		}
		runner, _, closeStore, err := openRunner(configPath)
		if err != nil {
			return err
		}
		defer closeStore()
		j, err := runner.Get(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		return body(ctx, runner, j)
	})
}

// printJob shows everything recorded about a job.
func printJob(w io.Writer, j *jobs.Job) {
	fmt.Fprintf(w, "Job %s: %s\n", j.ID, j.Status)
	if j.Error != "" && j.Status != jobs.Cancelled {
		fmt.Fprintf(w, "Error:    %s\n", j.Error)
	}
	fmt.Fprintf(w, "Repo:     %s (%s)\n", j.Repo, j.RepoPath)
	fmt.Fprintf(w, "Branch:   %s, %d commit(s) on %s\n", j.Branch, j.Commits, short(j.BaseCommit))
	fmt.Fprintf(w, "Model:    %s\n", j.Model)
	if j.Parent != "" {
		fmt.Fprintf(w, "Follows:  job %s\n", j.Parent)
	}
	if !j.StartedAt.IsZero() {
		end := j.FinishedAt
		if end.IsZero() {
			end = time.Now()
		}
		fmt.Fprintf(w, "Ran:      %s, started %s\n", end.Sub(j.StartedAt).Round(time.Second), j.StartedAt.Format(time.DateTime))
	}
	if j.Worktree != "" {
		fmt.Fprintf(w, "Worktree: %s\n", j.Worktree)
	}
	fmt.Fprintf(w, "Log:      %s\n", j.LogPath)
	fmt.Fprintf(w, "\nInstructions:\n%s\n", indent(j.Prompt))
	if j.Summary != "" {
		fmt.Fprintf(w, "\nAgent notes:\n%s\n", indent(j.Summary))
	}
	if j.DiffStat != "" {
		fmt.Fprintf(w, "\nChanges:\n%s\n", indent(j.DiffStat))
	}
	if j.TestResult != "" {
		fmt.Fprintf(w, "\nTests %s.\n", j.TestResult)
		if j.TestResult == "failed" {
			fmt.Fprintf(w, "%s\n", indent(lastLines(j.TestOutput, 20)))
		}
	}
	if j.Status == jobs.Done && j.Commits > 0 {
		fmt.Fprintf(w, "\nReview it with: git -C %s log -p %s..%s\n", j.RepoPath, short(j.BaseCommit), j.Branch)
	}
	if j.Status.Terminal() && j.Status != jobs.Done && j.Worktree != "" {
		fmt.Fprintf(w, "\nThe worktree is kept for a look; `dotshift clean %s` removes it.\n", j.ID)
	}
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func firstLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format(time.DateOnly)
	}
}
