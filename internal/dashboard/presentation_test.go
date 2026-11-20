package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/jobs"
)

func reviewedWork(id, parent string, at int) *jobs.Job {
	return &jobs.Job{ID: id, Parent: parent, Repo: "app", Branch: "work", Prompt: "Build the local app", Title: "Add search", Status: jobs.Done, TestResult: "passed", Commits: 1,
		CreatedAt: time.Unix(int64(at), 0), Goal: &jobs.GoalProgress{Phase: "reviewing", TestedCommit: "commit-" + id, Report: jobs.GoalReport{Status: "complete"}}}
}

func TestCompletionPresentationRequiresValidationAndReview(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*jobs.Job)
		delivery  *jobs.Delivery
		kind      string
		attention bool
	}{
		{"failed tests override apparent delivery", func(j *jobs.Job) { j.TestResult = "failed" }, &jobs.Delivery{Ready: true}, "needs_dot", true},
		{"legacy result has no review", func(j *jobs.Job) { j.Goal = nil }, &jobs.Delivery{Applied: true}, "unverified", false},
		{"unfinished review", func(j *jobs.Job) { j.Goal.Report.Status = "continue" }, &jobs.Delivery{Ready: true}, "unverified", false},
		{"ready", func(*jobs.Job) {}, &jobs.Delivery{Ready: true}, "ready", false},
		{"applied", func(*jobs.Job) {}, &jobs.Delivery{Applied: true}, "applied", false},
		{"local edits are not an unsolicited demand to apply", func(*jobs.Job) {}, &jobs.Delivery{BlockerCode: "local_changes"}, "delivery_blocked", false},
		{"no changes needed", func(j *jobs.Job) { j.Commits = 0 }, nil, "verified", false},
		{"cancelled work stays stopped", func(j *jobs.Job) { j.Status = jobs.Cancelled }, nil, "stopped", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			j := reviewedWork("one", "", 1)
			test.change(j)
			state := presentWork(j, test.delivery)
			if state.Kind != test.kind || state.Attention != test.attention {
				t.Fatalf("unexpected outcome: %+v", state)
			}
			if test.kind == "needs_dot" && (state.Completed || strings.Contains(state.Message, "apply the tested")) {
				t.Fatalf("failed checks offered completed delivery: %+v", state)
			}
			if test.kind == "stopped" && !strings.Contains(state.Message, "I want to continue") {
				t.Fatal("resuming stopped work must require an explicit request")
			}
		})
	}
}

func TestAttentionExplainsHumanInputAndAutomaticWaiting(t *testing.T) {
	j := reviewedWork("question", "", 1)
	j.Status = jobs.Blocked
	j.Goal.Report = jobs.GoalReport{Status: "blocked", NextSteps: "Which account should receive the export?"}
	state := presentWork(j, nil)
	if state.Kind != "needs_input" || state.Detail != j.Goal.Report.NextSteps || !strings.Contains(state.Message, j.ID) {
		t.Fatalf("lost the question or handoff context: %+v", state)
	}
	j.Status = jobs.Running
	reset := time.Now().Add(time.Hour)
	j.Goal.ReviewerSelection = &jobs.ModelSelection{WaitingUntil: &reset}
	state = presentWork(j, nil)
	if state.Attention || state.Message != "" || !strings.Contains(state.Detail, "automatically") {
		t.Fatalf("automatic retry demanded intervention: %+v", state)
	}
}

func TestRelatedRequestsResolveOnlyConfirmedAncestorFailures(t *testing.T) {
	root := reviewedWork("root", "", 1)
	root.Goal = nil // Historical work still has an explicit parent chain.
	root.TestResult = "failed"
	root.Title = "Build the household notebook"
	middle := reviewedWork("middle", "root", 2)
	middle.Goal, middle.TestResult = nil, "failed"
	latest := reviewedWork("latest", "middle", 3)
	other := reviewedWork("other", "", 4)
	other.TestResult = "failed"
	p := newPresenter(&fakeService{})
	views := p.views(context.Background(), []*jobs.Job{latest, other, root, middle}, "root")
	current, failures := 0, 0
	for _, view := range views {
		if view.CurrentID == view.ID {
			current++
		}
		if view.Display.Attention {
			failures++
		}
		if view.ID == "root" || view.ID == "middle" {
			if view.ResolvedBy != "latest" || view.CurrentID != "latest" || view.Display.Message != "" || view.WorkTitle != root.Title {
				t.Fatalf("history lost its resolution or request: %+v", view)
			}
			if !strings.Contains(view.Display.Detail, "No action is needed") {
				t.Fatalf("resolved history still requests intervention: %+v", view.Display)
			}
		}
		if view.ID == "other" && (view.ResolvedBy != "" || !view.Display.Attention) {
			t.Fatal("unrelated success hid an unresolved request")
		}
	}
	if current != 2 || failures != 1 {
		t.Fatalf("current=%d failures=%d", current, failures)
	}
	if root.TestResult != "failed" || root.AttentionDismissed() {
		t.Fatal("presentation rewrote saved history")
	}
}

func TestFailedCancelledUnreviewedOrUnrelatedFollowupsCannotResolveIssues(t *testing.T) {
	for _, variant := range []string{"failed", "cancelled", "unreviewed", "different repo", "different branch", "cycle", "sibling"} {
		t.Run(variant, func(t *testing.T) {
			root := reviewedWork("root", "", 1)
			root.TestResult = "failed"
			next := reviewedWork("next", "root", 2)
			switch variant {
			case "failed":
				next.TestResult = "failed"
			case "cancelled":
				next.Status = jobs.Cancelled
			case "unreviewed":
				next.Goal.Report.Status = "continue"
			case "different repo":
				next.Repo = "other"
			case "different branch":
				next.Branch = "other"
			case "cycle":
				root.Parent = "next"
			case "sibling":
				root.Parent, next.Parent = "missing", "missing"
			}
			views := newPresenter(&fakeService{}).views(context.Background(), []*jobs.Job{root, next}, "")
			if views[0].ResolvedBy != "" || !views[0].Display.Attention {
				t.Fatalf("unconfirmed resolution: %+v", views[0])
			}
		})
	}
}

func TestListDetailAndDeliveryRefreshShareOneOutcome(t *testing.T) {
	h, service, record := testHandler(t)
	j := reviewedWork("goal-1", "", 1)
	service.job = j
	service.deliveries = map[string]*jobs.Delivery{j.ID: {Ready: true}}
	list := request(h, record, "GET", "/api/jobs", "", nil)
	detail := request(h, record, "GET", "/api/jobs/goal-1", "", nil)
	var views []jobView
	var view jobView
	if json.Unmarshal(list.Body.Bytes(), &views) != nil || json.Unmarshal(detail.Body.Bytes(), &view) != nil {
		t.Fatal("invalid views")
	}
	if len(views) != 1 || views[0].Display != view.Display || view.Display.Kind != "ready" || service.deliveryCalls != 1 {
		t.Fatalf("list and detail disagree: %+v %+v calls=%d", views, view, service.deliveryCalls)
	}
	p := newPresenter(service)
	p.views(context.Background(), []*jobs.Job{j}, "")
	service.deliveries[j.ID] = &jobs.Delivery{Applied: true}
	entry := p.cache[j.ID]
	entry.until = time.Time{}
	p.cache[j.ID] = entry
	if next := p.views(context.Background(), []*jobs.Job{j}, "")[0]; next.Display.Kind != "applied" {
		t.Fatalf("external checkout change never became visible: %+v", next)
	}
}
