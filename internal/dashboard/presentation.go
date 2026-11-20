package dashboard

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gregwhitaker/dotshift/internal/jobs"
)

// workState is shared by the browser and menu bar. A process finishing, a
// reviewed result, and changes reaching the user's checkout are distinct.
type workState struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Detail    string `json:"detail"`
	Attention bool   `json:"attention"`
	Completed bool   `json:"completed"`
	Message   string `json:"message,omitempty"`
}

type deliveryEntry struct {
	commit string
	until  time.Time
	value  *jobs.Delivery
}

type presenter struct {
	service Service
	mu      sync.Mutex
	cache   map[string]deliveryEntry
}

func newPresenter(service Service) *presenter {
	return &presenter{service: service, cache: map[string]deliveryEntry{}}
}

func (p *presenter) view(ctx context.Context, j *jobs.Job) (*jobView, error) {
	list, err := p.service.List(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for i, item := range list {
		if item.ID == j.ID {
			list[i], found = j, true
			break
		}
	}
	if !found {
		list = append(list, j)
	}
	for _, view := range p.views(ctx, list, j.ID) {
		if view.ID == j.ID {
			return &view, nil
		}
	}
	return nil, jobs.ErrNotFound
}

// This cache is only for display. apply_task always rechecks the checkout.
// Sharing it keeps menu, list and detail labels consistent without repeatedly
// running Git as several monitor windows poll the same finished work.
func (p *presenter) delivery(ctx context.Context, j *jobs.Job) *jobs.Delivery {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.cache[j.ID]; ok && entry.commit == j.Goal.TestedCommit && time.Now().Before(entry.until) {
		return entry.value
	}
	d, err := p.service.Delivery(ctx, j.ID)
	if err != nil || d == nil {
		return &jobs.Delivery{BlockerCode: "unavailable", Blocker: "Could not inspect the local checkout."}
	}
	p.cache[j.ID] = deliveryEntry{commit: j.Goal.TestedCommit, until: time.Now().Add(5 * time.Second), value: d}
	return d
}

func relatedParent(j *jobs.Job, byID map[string]*jobs.Job) *jobs.Job {
	parent := byID[j.Parent]
	if parent == nil || parent.ID == j.ID || parent.Repo != j.Repo ||
		(parent.Branch != "" && j.Branch != "" && parent.Branch != j.Branch) ||
		(parent.RepoPath != "" && j.RepoPath != "" && parent.RepoPath != j.RepoPath) {
		return nil
	}
	return parent
}

func hasIssue(j *jobs.Job) bool {
	return j.Status == jobs.Blocked || j.Status == jobs.Failed || j.Status == jobs.WaitingInput ||
		(j.Status == jobs.Done && j.TestResult == "failed")
}

func later(a, b *jobs.Job) bool {
	return b == nil || a.CreatedAt.After(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID)
}

func (p *presenter) views(ctx context.Context, list []*jobs.Job, detailID string) []jobView {
	byID := make(map[string]*jobs.Job, len(list))
	for _, j := range list {
		byID[j.ID] = j
	}
	roots := make(map[string]*jobs.Job, len(list))
	resolved := make(map[string]*jobs.Job)
	for _, j := range list {
		root := j
		valid := true
		seen := map[string]bool{j.ID: true}
		for parent := relatedParent(root, byID); parent != nil; parent = relatedParent(root, byID) {
			if seen[parent.ID] { // Corrupt history must not hang or merge requests.
				root = j
				valid = false
				break
			}
			seen[parent.ID] = true
			root = parent
		}
		roots[j.ID] = root
		// Only a successful descendant on the explicitly linked branch can
		// resolve an older issue. Unrelated success, cancellation and mere age
		// never clear attention. Legacy jobs still have independently run tests.
		if !valid || j.Status != jobs.Done || j.TestResult != "passed" || (j.Goal != nil && !jobs.Reviewed(j)) {
			continue
		}
		seen = map[string]bool{j.ID: true}
		for parent := relatedParent(j, byID); parent != nil && !seen[parent.ID]; parent = relatedParent(parent, byID) {
			seen[parent.ID] = true
			if hasIssue(parent) && !parent.CreatedAt.After(j.CreatedAt) && later(j, resolved[parent.ID]) {
				resolved[parent.ID] = j
			}
		}
	}
	current := map[string]*jobs.Job{}
	rank := func(j *jobs.Job) int {
		if j.Status == jobs.Running || j.Status == jobs.Queued {
			return 0
		}
		if hasIssue(j) && resolved[j.ID] == nil && !j.AttentionDismissed() {
			return 1
		}
		return 2
	}
	for _, j := range list {
		root := roots[j.ID].ID
		prev := current[root]
		if prev == nil || rank(j) < rank(prev) || (rank(j) == rank(prev) && later(j, prev)) {
			current[root] = j
		}
	}
	out := make([]jobView, 0, len(list))
	for _, j := range list {
		v := describe(j, j.ID == detailID)
		root := roots[j.ID]
		v.WorkID, v.WorkTitle, v.CurrentID = root.ID, jobs.DisplayTitle(root), current[root.ID].ID
		if resolution := resolved[j.ID]; resolution != nil {
			v.ResolvedBy = resolution.ID
		}
		if jobs.Reviewed(j) && j.Commits > 0 && (j.ID == v.CurrentID || j.ID == detailID) {
			v.Delivery = p.delivery(ctx, j)
		}
		v.Display = presentWork(j, v.Delivery)
		if v.ResolvedBy != "" {
			v.Display.Detail = "A later linked result passed validation. No action is needed on this earlier result."
		}
		if j.AttentionDismissed() || v.ResolvedBy != "" {
			v.Display.Attention = false
		}
		if j.ID != v.CurrentID {
			v.Display.Message = "" // The handoff always uses the current work.
		}
		out = append(out, v)
	}
	return out
}

func presentWork(j *jobs.Job, delivery *jobs.Delivery) workState {
	ref := fmt.Sprintf("task %s in %s", j.ID, j.Repo)
	recover := "Use dotshift to inspect " + ref + ". Resolve routine coding and validation problems and continue the saved work. Preserve my local edits and existing limits. Ask me only for missing access or a consequential decision. Do not apply, push or publish changes unless I ask."
	if j.Status == jobs.Running || j.Status == jobs.Queued {
		if j.Status == jobs.Queued {
			return workState{Kind: "queued", Label: "Queued", Detail: "Dot will start this work automatically."}
		}
		phase := "working"
		var selection *jobs.ModelSelection
		if j.Goal != nil {
			phase, selection = j.Goal.Phase, j.Goal.ModelSelection
			if phase == "reviewing" && j.Goal.ReviewerSelection != nil {
				selection = j.Goal.ReviewerSelection
			}
		}
		if selection != nil && selection.WaitingUntil != nil && !selection.WaitingUntil.IsZero() {
			return workState{Kind: "waiting", Label: "Waiting to retry", Detail: "Coding access is temporarily unavailable. Dotshift will retry automatically; no action is needed."}
		}
		switch phase {
		case "testing":
			return workState{Kind: "testing", Label: "Checking changes", Detail: "Tests are running. Dot will handle any failures."}
		case "reviewing":
			return workState{Kind: "reviewing", Label: "Reviewing changes", Detail: "A separate review is checking the result before it is ready."}
		default:
			return workState{Kind: "working", Label: "Working", Detail: "Dot is making changes in a separate working copy."}
		}
	}
	if j.Status == jobs.Cancelled {
		return workState{Kind: "stopped", Label: "Stopped", Detail: "Work stays stopped. The saved progress is available if you choose to continue.", Message: "I want to continue " + ref + ". Use dotshift to resume its saved work, preserving my local edits and existing limits."}
	}
	if hasIssue(j) {
		s := workState{Kind: "needs_dot", Label: "Ask Dot to continue", Attention: true, Detail: "Work has stopped. Ask Dot to inspect the saved progress and handle the next step.", Message: recover}
		lower := strings.ToLower(j.Error)
		switch {
		case strings.Contains(lower, "budget") || strings.Contains(lower, "spending cap"):
			s.Kind, s.Label, s.Detail = "needs_input", "Spending limit reached", "Work stopped at its spending limit. Tell Dot whether to keep this limit or authorize a different one."
		case strings.Contains(lower, "max_iterations"):
			s.Kind, s.Label, s.Detail = "needs_input", "Work limit reached", "Work stopped at its configured limit. Ask Dot to review what remains before deciding whether to continue."
		case strings.Contains(lower, "sign in") || strings.Contains(lower, "login") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "not authenticated"):
			s.Kind, s.Label, s.Detail = "needs_input", "Coding access needed", "A coding agent needs access restored. Ask Dot which sign-in or access step is required; your progress is saved."
		case j.Goal != nil && j.Goal.Report.Status == "blocked" && strings.TrimSpace(j.Goal.Report.NextSteps) != "":
			s.Kind, s.Label, s.Detail = "needs_input", "Dot needs your input", strings.TrimSpace(j.Goal.Report.NextSteps)
		case j.Status == jobs.WaitingInput:
			s.Kind, s.Label = "needs_input", "Dot needs your input"
		case j.TestResult == "failed":
			s.Label, s.Detail = "Tests failed", "The checks did not pass. Ask Dot to inspect the failure and finish the work; this result is not ready to apply."
		}
		return s
	}
	if j.Status != jobs.Done {
		return workState{Kind: "saved", Label: "Saved", Detail: "Progress is saved locally."}
	}
	if !jobs.Reviewed(j) {
		return workState{Kind: "unverified", Label: "Review not recorded", Completed: j.TestResult == "passed", Detail: "This earlier result has no recorded completion review. Ask Dot to verify it before applying changes.", Message: "Use dotshift to verify the tests and perform an independent review of " + ref + " before considering it ready to apply. Preserve the existing work and my local edits."}
	}
	if j.Commits == 0 {
		return workState{Kind: "verified", Label: "Verified", Completed: true, Detail: "Tests and review passed. No code changes were needed."}
	}
	if delivery == nil {
		return workState{Kind: "reviewed", Label: "Tested and reviewed", Completed: true, Detail: "Tests and review passed. Open the current result to check its local application status."}
	}
	if delivery.Applied {
		return workState{Kind: "applied", Label: "Applied locally", Completed: true, Detail: "Your local checkout includes these tested and reviewed changes. Ask Dot for any follow-up work."}
	}
	if delivery.Ready {
		return workState{Kind: "ready", Label: "Ready to apply", Completed: true, Detail: "Tests and review passed. The changes are saved separately and have not been applied to your local checkout.", Message: "Use dotshift to apply the tested and reviewed result from " + ref + " to my local checkout. Retrieve its current delivery fields first and preserve local edits. Do not push or publish."}
	}
	detail := "The result is saved, but its local application status could not be checked. Ask Dot to inspect it."
	switch delivery.BlockerCode {
	case "local_changes":
		detail = "Your local checkout has edits. Ask Dot to help preserve them before applying this result."
	case "diverged":
		detail = "Your local app has changed since this work started. Dot needs to combine the changes and check them again before applying."
	case "result_changed":
		detail = "The saved changes have changed since review. Ask Dot to check the latest result before applying."
	case "git_in_progress":
		detail = "A Git operation is already in progress in your checkout. Ask Dot to inspect it before applying this result."
	case "detached", "checkout_changed":
		detail = "The destination checkout needs to be confirmed before these changes can be applied. Ask Dot to inspect it."
	}
	// A saved, reviewed result does not demand intervention merely because the
	// user has local edits or asked to keep the work on a separate branch.
	return workState{Kind: "delivery_blocked", Label: "Not applied", Completed: true, Detail: detail, Message: "Use dotshift to inspect what prevents applying " + ref + ". Help me bring the result into my local app while preserving my edits. Ask before resolving a consequential choice; do not stash, reset, push or publish implicitly."}
}
