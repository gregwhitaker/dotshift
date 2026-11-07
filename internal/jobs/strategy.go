package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
)

var errModelsUnavailable = errors.New("all strategy models need restored access")
var errAdaptiveStalled = errors.New("adaptive strategy exhausted its model ladder")

// AdaptiveProgress survives service restarts; ordinary resume retains the tier.
type AdaptiveProgress struct {
	Level            int    `json:"level"`
	EscalateAfter    int    `json:"escalate_after"`
	StalledCycles    int    `json:"stalled_cycles"`
	ReviewRejections int    `json:"review_rejections"`
	Escalations      int    `json:"escalations"`
	LastTree         string `json:"last_tree,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

// ModelSelection is persisted with the goal at every change and wait. The
// branch and cumulative GoalReport are the handoff between model invocations.
type ModelSelection struct {
	Strategy     string     `json:"strategy"`
	ActiveModel  string     `json:"active_model,omitempty"`
	ActiveRoute  string     `json:"active_route,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	Switches     int        `json:"switches"`
	SelectedAt   *time.Time `json:"selected_at,omitempty"`
	WaitingUntil *time.Time `json:"waiting_until,omitempty"`
	LastFailure  string     `json:"last_failure,omitempty"`
}

type StrategyCatalog struct {
	ExecutionMode        string         `json:"execution_mode"`
	SupportsSpendingCaps bool           `json:"supports_spending_caps"`
	DefaultStrategy      string         `json:"default_strategy"`
	DefaultReviewer      string         `json:"default_reviewer_model,omitempty"`
	DefaultEscalateAfter int            `json:"default_escalate_after"`
	DefaultMaxCostUSD    float64        `json:"default_max_cost_usd"`
	Strategies           []StrategyInfo `json:"strategies"`
}

type StrategyInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Aliases     []StrategyAlias `json:"aliases"`
}

type StrategyAlias struct {
	Alias  string         `json:"alias"`
	Models []Availability `json:"models"`
}

// Strategies lets dot discover supported policies, eligible alias pools and
// observed availability without making a paid provider request.
func (r *Runner) Strategies(ctx context.Context) (*StrategyCatalog, error) {
	defaultStrategy := r.Config.Jobs.Strategy
	if defaultStrategy == "" {
		defaultStrategy = config.StrategyAlias
	}
	catalog := &StrategyCatalog{DefaultStrategy: defaultStrategy, DefaultReviewer: r.Config.Jobs.ReviewerModel, DefaultEscalateAfter: r.Config.Jobs.EscalateAfter, DefaultMaxCostUSD: r.Config.Jobs.MaxCostUSD, Strategies: []StrategyInfo{
		{Name: config.StrategyAlias, Description: "Use alias order. Router-managed fallbacks by default; a separate reviewer or spending cap enables explicit deployment selection and tracked fallback attempts.", Aliases: []StrategyAlias{}},
		{Name: config.StrategyPreferOpenAI, Description: "Prefer configured OpenAI models, then Anthropic. Persist API quota failures and cooldowns, and switch between fresh agent attempts.", Aliases: []StrategyAlias{}},
		{Name: config.StrategyAdaptive, Description: "Use the alias's configured model order, from baseline to strongest. Escalate after repeated failed tests, unchanged work, or rejected reviews; preserve the tier across restarts.", Aliases: []StrategyAlias{}},
	}}
	catalog.ExecutionMode = r.Config.ExecutionMode()
	catalog.SupportsSpendingCaps = !r.Config.UsesSubscriptions()
	if r.Config.UsesSubscriptions() {
		catalog.Strategies[0].Description = "Use alias order through the official subscription CLIs. Switch after observed usage limits; wait for a known reset or block until retry_unavailable."
		catalog.Strategies[1].Description = "Prefer Codex with ChatGPT sign-in, then Claude Code with Claude sign-in. Persist subscription limit events and preserve the worktree between attempts. No API fallback."
	}
	now := time.Now()
	for _, alias := range r.Config.AliasNames() {
		for i := range catalog.Strategies {
			routes, err := r.Config.StrategyModels(alias, catalog.Strategies[i].Name)
			if err != nil {
				continue
			}
			pool := StrategyAlias{Alias: alias, Models: []Availability{}}
			for _, route := range routes {
				availability, err := r.modelAvailability(ctx, route, now)
				if err != nil {
					return nil, err
				}
				pool.Models = append(pool.Models, availability)
			}
			catalog.Strategies[i].Aliases = append(catalog.Strategies[i].Aliases, pool)
		}
	}
	return catalog, nil
}

func (r *Runner) goalStrategy(j *Job) string {
	if j.Goal.ModelSelection != nil {
		return j.Goal.ModelSelection.Strategy
	}
	return config.StrategyAlias
}

func (r *Runner) selectGoalModel(ctx context.Context, j *Job, review bool, out io.Writer) (config.ModelRoute, error) {
	routes, err := r.Config.StrategyModels(j.Model, r.goalStrategy(j))
	selection := j.Goal.ModelSelection
	if review && j.Goal.ReviewerModel != "" {
		routes, err = r.Config.ReviewerRoutes(j.Model, j.Goal.ReviewerModel, selection.ActiveModel)
		selection = j.Goal.ReviewerSelection
	} else if adaptive := j.Goal.Adaptive; adaptive != nil && err == nil {
		if adaptive.Level < 0 || adaptive.Level >= len(routes) {
			return config.ModelRoute{}, errors.New("adaptive model ladder changed; resume_goal with a replacement model alias")
		}
		routes = routes[adaptive.Level:]
	}
	if err != nil {
		return config.ModelRoute{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return config.ModelRoute{}, err
		}
		now := time.Now()
		available := make([]Availability, 0, len(routes))
		for _, route := range routes {
			state, err := r.modelAvailability(ctx, route, now)
			if err != nil {
				return config.ModelRoute{}, err
			}
			available = append(available, state)
		}
		// Keep the implementation's model through completion review when it
		// is still available. Fail back at the next coding phase boundary.
		if review && j.Goal.ReviewerModel == "" && selection.ActiveModel != "" {
			for i, state := range available {
				if state.Model == selection.ActiveModel && state.Available {
					available[0], available[i] = available[i], available[0]
					break
				}
			}
		}
		var waitUntil time.Time
		var reasons []string
		chosen := -1
		for i, state := range available {
			if state.Available {
				chosen = i
				break
			}
			reasons = append(reasons, state.Model+": "+state.Reason)
			if state.RetryAt != nil && (waitUntil.IsZero() || state.RetryAt.Before(waitUntil)) {
				waitUntil = *state.RetryAt
			}
		}
		// Wait out a short throttle on the preferred model. Longer throttles
		// use an available fallback immediately.
		briefWait := chosen > 0 && !waitUntil.IsZero() && waitUntil.Sub(now) <= 5*time.Second
		if chosen >= 0 && !briefWait {
			route := available[chosen].ModelRoute
			if a := j.Goal.Adaptive; a != nil && !review {
				ladder, _ := r.Config.StrategyModels(j.Model, config.StrategyAdaptive)
				for i, candidate := range ladder {
					if candidate.Model == route.Model && i > a.Level {
						a.Level, a.StalledCycles, a.ReviewRejections = i, 0, 0
						j.Goal.Failures = 0
						a.Escalations++
						a.Reason = "lower tiers unavailable; advanced to " + route.Model
					}
				}
			}
			if selection.ActiveModel != route.Model || selection.ActiveRoute != route.Route {
				if selection.ActiveModel != "" && selection.ActiveModel != route.Model {
					selection.Switches++
				}
				selection.ActiveModel, selection.ActiveRoute = route.Model, route.Route
				selection.SelectedAt = &now
				selection.Reason = "selected the first available model in " + selection.Strategy + " order"
				if review && j.Goal.ReviewerModel != "" {
					selection.Reason = "selected a reviewer different from implementer " + j.Goal.ModelSelection.ActiveModel
				}
				if len(reasons) > 0 {
					selection.Reason += "; skipped " + strings.Join(reasons, "; ")
				}
				fmt.Fprintf(out, "--- dotshift: selected %s: %s\n", route.Model, selection.Reason)
			}
			selection.WaitingUntil = nil
			if err := r.Store.saveGoal(ctx, j); err != nil {
				return config.ModelRoute{}, err
			}
			return route, nil
		}
		if waitUntil.IsZero() {
			selection.WaitingUntil = nil
			selection.Reason = "all strategy models require restored credits or access: " + strings.Join(reasons, "; ")
			return config.ModelRoute{}, fmt.Errorf("%w: %s; wait for the allowance to reset or restore access, then resume_goal with retry_unavailable=true", errModelsUnavailable, strings.Join(reasons, "; "))
		}
		if selection.WaitingUntil == nil || !selection.WaitingUntil.Equal(waitUntil) {
			selection.WaitingUntil = &waitUntil
			selection.Reason = "waiting for model availability: " + strings.Join(reasons, "; ")
			fmt.Fprintf(out, "--- dotshift: %s; retry at %s\n", selection.Reason, waitUntil.UTC().Format(time.RFC3339))
		}
		// Check the database every second so cancellation from a separate
		// process also interrupts a long provider cooldown.
		if err := r.Store.saveGoal(ctx, j); err != nil {
			return config.ModelRoute{}, err
		}
		timer := time.NewTimer(min(time.Second, max(time.Millisecond, time.Until(waitUntil))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return config.ModelRoute{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Runner) phaseSelection(j *Job) *ModelSelection {
	if j.Goal.Phase == goalReviewing && j.Goal.ReviewerModel != "" {
		return j.Goal.ReviewerSelection
	}
	return j.Goal.ModelSelection
}

func (r *Runner) adaptiveTestResult(ctx context.Context, j *Job, out io.Writer) error {
	a := j.Goal.Adaptive
	if a == nil {
		return nil
	}
	tree, err := git(ctx, j.Worktree, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	previous := a.LastTree
	if previous == "" {
		previous, err = git(ctx, j.Worktree, "rev-parse", j.BaseCommit+"^{tree}")
		if err != nil {
			return err
		}
	}
	a.LastTree = tree
	reason := ""
	if j.TestResult == "failed" {
		reason = "repeated failed test cycles"
	} else if j.Goal.Report.Status == "continue" && tree == previous {
		reason = "repeated coding cycles without file changes"
	}
	if reason == "" {
		a.StalledCycles = 0
		return nil
	}
	a.StalledCycles++
	if a.StalledCycles < a.EscalateAfter {
		return nil
	}
	return r.escalateGoal(j, reason, out)
}

func (r *Runner) adaptiveReviewRejected(j *Job, out io.Writer) error {
	a := j.Goal.Adaptive
	if a == nil {
		return nil
	}
	a.ReviewRejections++
	if a.ReviewRejections < a.EscalateAfter {
		return nil
	}
	return r.escalateGoal(j, "repeated completion review rejections", out)
}

func (r *Runner) escalateGoal(j *Job, reason string, out io.Writer) error {
	a := j.Goal.Adaptive
	routes, err := r.Config.StrategyModels(j.Model, config.StrategyAdaptive)
	if err != nil {
		return err
	}
	level := a.Level
	for i, route := range routes {
		if route.Model == j.Goal.ModelSelection.ActiveModel {
			level = max(level, i)
		}
	}
	if level+1 >= len(routes) {
		a.Reason = reason + "; the strongest configured model also stalled"
		return fmt.Errorf("%w: %s; inspect the tests or review findings and resume with instructions or a revised model ladder", errAdaptiveStalled, a.Reason)
	}
	a.Level, a.StalledCycles, a.ReviewRejections = level+1, 0, 0
	j.Goal.Failures = 0
	a.Escalations++
	a.Reason = reason + "; escalating to " + routes[a.Level].Model
	fmt.Fprintf(out, "--- dotshift: %s\n", a.Reason)
	return nil
}
