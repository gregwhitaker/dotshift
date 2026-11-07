package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gregwhitaker/dotshift/internal/jobs"
)

// `dotshift run` is the CLI intake: it starts a job and follows it until it
// finishes. Ctrl-C cancels the job.
func init() {
	var repo, model, branch, base *string
	commands["run"] = command("run",
		func(fs *flag.FlagSet) {
			repo = fs.String("repo", "", "allowlisted repo name or path (optional when only one is allowlisted)")
			model = fs.String("model", "", "router alias to use (default: jobs.model)")
			branch = fs.String("branch", "", "new branch for the job (default: dotshift/<job id>)")
			base = fs.String("base", "", "commit or branch to start from (default: the repo's HEAD)")
		},
		func(ctx context.Context, configPath string, fs *flag.FlagSet) error {
			prompt, err := readPrompt(fs.Args(), os.Stdin)
			if err != nil {
				return err
			}
			runner, r, closeStore, err := openRunner(configPath)
			if err != nil {
				return err
			}
			defer closeStore()
			if *repo == "" {
				repos, err := runner.Repos(ctx)
				if err != nil {
					return err
				}
				var names []string
				for _, item := range repos {
					names = append(names, item.Name)
				}
				if len(names) != 1 {
					return fmt.Errorf("say which repo with -repo (allowlisted: %s)", listOrNone(names))
				}
				*repo = names[0]
			}
			if r != nil && !r.Healthy(ctx) {
				return errors.New("the router is not running; start it with `dotshift serve` in another terminal")
			}
			runner.Echo = os.Stdout

			j, err := runner.Submit(ctx, jobs.Request{Repo: *repo, Prompt: prompt, Model: *model, Branch: *branch, Base: *base})
			if err != nil {
				return err
			}
			fmt.Printf("Job %s: %s on branch %s with %s\nWorktree: %s\n\n", j.ID, j.Repo, j.Branch, j.Model, j.Worktree)

			id := j.ID
			j, err = runner.Wait(ctx, id)
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "\nCancelling job...")
				if err := runner.Cancel(context.Background(), id); err != nil {
					return err
				}
				j, err = runner.Wait(context.Background(), id)
			}
			if err != nil {
				return err
			}
			fmt.Println()
			printJob(os.Stdout, j)
			if j.Status != jobs.Done {
				return fmt.Errorf("job %s %s", j.ID, j.Status)
			}
			return nil
		})
}

// readPrompt takes the instructions from the arguments, or from stdin when
// they are "-" or absent.
func readPrompt(args []string, stdin io.Reader) (string, error) {
	prompt := strings.Join(args, " ")
	if prompt == "" || prompt == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		prompt = string(data)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("no instructions given: dotshift run [flags] \"what to do\"")
	}
	return strings.TrimSpace(prompt), nil
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none yet; use create_project/register_project, or add one under repos: in dotshift.yaml"
	}
	return strings.Join(names, ", ")
}
