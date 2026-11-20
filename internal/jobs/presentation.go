package jobs

import (
	"strings"
	"unicode"
)

// DisplayTitle keeps historical prompts readable without changing their saved
// instructions or making a model request just to open the monitor.
func DisplayTitle(j *Job) string {
	title := strings.Join(strings.Fields(j.Title), " ")
	if title == "" {
		title = strings.TrimSpace(strings.SplitN(j.Prompt, "\n", 2)[0])
		title = strings.TrimLeft(title, "# ")
		if sentence, _, ok := strings.Cut(title, ". "); ok {
			title = sentence
		}
		lower := strings.ToLower(title)
		repo := strings.NewReplacer("-", " ", "_", " ").Replace(j.Repo)
		if repo != "" {
			runes := []rune(repo)
			runes[0] = unicode.ToUpper(runes[0])
			repo = string(runes)
			switch {
			case strings.HasPrefix(lower, "implement the following"), strings.HasPrefix(lower, "build the following"):
				title = "Build " + repo
			case strings.HasPrefix(lower, "read-only"), strings.HasPrefix(lower, "narrow read-only"), strings.HasPrefix(lower, "perform a fresh independent"):
				title = "Review " + repo
			}
		}
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return "Local coding request"
	}
	runes := []rune(title)
	if len(runes) <= 80 {
		return title
	}
	short := string(runes[:77])
	if i := strings.LastIndex(short, " "); i > 45 {
		short = short[:i]
	}
	return short + "…"
}

// Reviewed reports the runner's completion evidence, not merely a terminal
// process status or a model's claim that the work is finished.
func Reviewed(j *Job) bool {
	return j.Status == Done && j.TestResult == "passed" && j.Goal != nil &&
		j.Goal.Phase == goalReviewing && j.Goal.Report.Status == "complete" &&
		j.Goal.TestedCommit != "" && j.Worktree == ""
}
