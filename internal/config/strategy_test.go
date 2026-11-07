package config

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestStrategyUsesOnlyAliasPoolInProviderOrder(t *testing.T) {
	cfg := &Config{
		Providers: map[string]Provider{"openai": {}, "anthropic": {}, "gemini": {}},
		Aliases: map[string][]string{
			"smart": {"anthropic/claude-a", "gemini/gemini", "openai/gpt-a", "openai/gpt-b", "openai/gpt-a", "anthropic/claude-b"},
			"other": {"openai/unselected"},
		},
	}
	got, err := cfg.StrategyModels("smart", StrategyPreferOpenAI)
	want := []ModelRoute{
		{"openai/gpt-a", "openai", "smart~2"}, {"openai/gpt-b", "openai", "smart~3"},
		{"anthropic/claude-a", "anthropic", "smart"}, {"anthropic/claude-b", "anthropic", "smart~5"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("routes = %+v, %v", got, err)
	}
	if _, err := cfg.StrategyModels("other", StrategyPreferOpenAI); err == nil {
		t.Fatal("policy without its fallback provider was accepted")
	}
	if _, err := cfg.StrategyModels("smart", "invented"); err == nil {
		t.Fatal("unknown strategy was accepted")
	}
}

func TestAdaptiveOrderAndIndependentReviewerPools(t *testing.T) {
	cfg := &Config{Providers: map[string]Provider{"openai": {}, "anthropic": {}}, Aliases: map[string][]string{
		"ladder": {"openai/fast", "openai/strong", "openai/fast", "anthropic/reasoner"},
		"review": {"openai/strong", "anthropic/reasoner"}, "singleton": {"openai/fast"},
	}}
	routes, err := cfg.StrategyModels("ladder", StrategyAdaptive)
	if err != nil || len(routes) != 3 || routes[0].Model != "openai/fast" || routes[2].Route != "ladder~3" {
		t.Fatalf("ladder %+v, %v", routes, err)
	}
	if _, err := cfg.StrategyModels("singleton", StrategyAdaptive); err == nil {
		t.Fatal("one-model ladder accepted")
	}
	for _, tc := range []struct{ reviewer, implementer, first string }{
		{"auto", "openai/fast", "anthropic/reasoner"},
		{"auto", "anthropic/reasoner", "openai/fast"},
		{"review", "openai/strong", "anthropic/reasoner"},
		{"review", "anthropic/reasoner", "openai/strong"},
	} {
		reviewers, err := cfg.ReviewerRoutes("ladder", tc.reviewer, tc.implementer)
		if err != nil || reviewers[0].Model != tc.first {
			t.Fatalf("reviewers %+v, %v", reviewers, err)
		}
		for _, reviewer := range reviewers {
			if reviewer.Model == tc.implementer {
				t.Fatal("self-review allowed")
			}
		}
	}
	if err := cfg.ValidateReviewer("ladder", StrategyAdaptive, "review"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateReviewer("ladder", StrategyAdaptive, "singleton"); err == nil {
		t.Fatal("review pool cannot cover every implementer")
	}
	if err := cfg.ValidateReviewer("singleton", StrategyAlias, "auto"); err == nil {
		t.Fatal("auto self-review allowed")
	}
}

func TestGoalPolicyDefaultsAndLimits(t *testing.T) {
	base := "providers:\n  openai: {}\n  anthropic: {}\naliases:\n  smart: [openai/fast, anthropic/strong]\n"
	cfg, err := Parse([]byte(base + "jobs:\n  strategy: adaptive\n  reviewer_model: auto\n  max_cost_usd: 5\n"))
	if err != nil || cfg.Jobs.EscalateAfter != 2 || cfg.Jobs.MaxCostUSD != 5 || cfg.Jobs.ReviewerModel != "auto" {
		t.Fatalf("config %+v, %v", cfg, err)
	}
	for _, extra := range []string{
		"jobs: {max_cost_usd: -1}", "jobs: {max_cost_usd: .nan}", "jobs: {max_cost_usd: .inf}",
		"jobs: {escalate_after: -1}", "jobs: {reviewer_model: missing}",
		"router: {usage_listen: off}\njobs: {max_cost_usd: 1}",
	} {
		if _, err := Parse([]byte(base + extra)); err == nil {
			t.Fatalf("accepted %s", extra)
		}
	}
	for _, n := range []float64{-1, math.NaN(), math.Inf(1)} {
		if err := ValidateMaxCost(n); err == nil || !strings.Contains(err.Error(), "finite USD") {
			t.Fatalf("invalid cap %v: %v", n, err)
		}
	}
}

func TestDefaultGoalStrategyValidation(t *testing.T) {
	base := "providers:\n  openai: {}\n  anthropic: {}\naliases:\n  smart: [openai/gpt, anthropic/claude]\n"
	cfg, err := Parse([]byte(base + "jobs:\n  strategy: prefer_openai\n"))
	if err != nil || cfg.Jobs.Strategy != StrategyPreferOpenAI {
		t.Fatalf("default = %+v, %v", cfg, err)
	}
	if _, err := Parse([]byte(base + "jobs:\n  strategy: imaginary\n")); err == nil {
		t.Fatal("invalid default strategy accepted")
	}
	if _, err := Parse([]byte("providers:\n  openai: {}\naliases:\n  smart: [openai/gpt]\njobs:\n  strategy: prefer_openai\n")); err == nil {
		t.Fatal("default policy missing fallback accepted")
	}
}
