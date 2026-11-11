package config

import "fmt"

const (
	StrategyAlias        = "alias"
	StrategyPreferOpenAI = "prefer_openai"
	StrategyAdaptive     = "adaptive"
)

// ModelRoute identifies exactly one deployment behind an alias.
type ModelRoute struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Route    string `json:"route"`
}

// RouteName is also used when generating the LiteLLM configuration.
func RouteName(alias string, position int) string {
	if position == 0 {
		return alias
	}
	return fmt.Sprintf("%s~%d", alias, position)
}

// StrategyModels returns the policy's candidates in preference order. A goal
// can only select models explicitly listed in its chosen alias. In particular,
// prefer_openai never discovers or purchases access to an unconfigured model.
func (c *Config) StrategyModels(alias, strategy string) ([]ModelRoute, error) {
	models, ok := c.Aliases[alias]
	if !ok {
		return nil, fmt.Errorf("model alias %q is not configured", alias)
	}
	if strategy != "" && strategy != StrategyAlias && strategy != StrategyAdaptive && strategy != StrategyPreferOpenAI {
		return nil, fmt.Errorf("unknown strategy %q (use alias, prefer_openai, or adaptive)", strategy)
	}
	var routes []ModelRoute
	seen := map[string]bool{}
	counts := map[string]int{}
	providers := []string{""}
	if strategy == StrategyPreferOpenAI {
		providers = []string{"openai", "anthropic"}
	}
	for _, provider := range providers {
		for i, model := range models {
			p, _, _ := SplitModel(model)
			if (provider != "" && p != provider) || seen[model] {
				continue
			}
			if _, ok := c.Providers[p]; !ok {
				return nil, fmt.Errorf("provider %q is not configured", p)
			}
			seen[model] = true
			counts[p]++
			routes = append(routes, ModelRoute{Model: model, Provider: p, Route: RouteName(alias, i)})
		}
	}
	if strategy == StrategyPreferOpenAI && (counts["openai"] == 0 || counts["anthropic"] == 0) {
		return nil, fmt.Errorf("prefer_openai requires both OpenAI and Anthropic models in alias %q", alias)
	}
	if strategy == StrategyAdaptive && len(routes) < 2 {
		return nil, fmt.Errorf("adaptive requires at least two distinct models in alias %q, ordered from baseline to strongest", alias)
	}
	return routes, nil
}

// ReviewerRoutes prefers another provider, then another model for auto review.
// Subscription auto review can also use a fresh session of the coding model
// when the other candidates are unavailable. Explicit pools stay distinct.
func (c *Config) ReviewerRoutes(codingAlias, reviewer, implementer string) ([]ModelRoute, error) {
	alias := reviewer
	if reviewer == "auto" {
		alias = codingAlias
	}
	routes, err := c.StrategyModels(alias, StrategyAlias)
	if err != nil {
		return nil, err
	}
	var out []ModelRoute
	provider, workerModel, _ := SplitModel(implementer)
	for pass := 0; pass < 2; pass++ {
		for _, route := range routes {
			if route.Model == implementer {
				continue
			}
			_, reviewModel, _ := SplitModel(route.Model)
			if c.UsesSubscriptions() && route.Provider == provider && (workerModel == "default" || reviewModel == "default") {
				// CLI defaults may resolve to the same explicit model. A different
				// provider is required when model identity is not pinned.
				continue
			}
			otherProvider := reviewer == "auto" && route.Provider != provider
			if (pass == 0) == otherProvider {
				out = append(out, route)
			}
		}
	}
	if reviewer == "auto" && c.UsesSubscriptions() {
		for _, route := range routes {
			if route.Model == implementer {
				// Review always launches a fresh read-only CLI process. Keep
				// the working model last so an exhausted provider cannot
				// prevent the remaining provider from finishing the goal.
				out = append(out, route)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("reviewer_model %q has no model different from implementer %q", reviewer, implementer)
	}
	return out, nil
}

func (c *Config) ValidateReviewer(codingAlias, strategy, reviewer string) error {
	if reviewer == "" {
		return nil
	}
	workers, err := c.StrategyModels(codingAlias, strategy)
	if err != nil {
		return err
	}
	for _, worker := range workers {
		if _, err := c.ReviewerRoutes(codingAlias, reviewer, worker.Model); err != nil {
			return err
		}
	}
	return nil
}
