package config

import (
	"strings"
	"testing"
)

func TestSubscriptionConfiguration(t *testing.T) {
	cfg, err := Parse([]byte(SubscriptionSample))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsesSubscriptions() || len(cfg.KeyEnvs()) != 0 || len(cfg.Providers) != 2 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	for _, changed := range []string{
		strings.Replace(SubscriptionSample, "mode: subscription", "mode: subscription\n  max_cost_usd: 1", 1),
		SubscriptionSample + "\nproviders:\n  openai:\n    api_key_env: OPENAI_API_KEY\n",
		strings.ReplaceAll(SubscriptionSample, "anthropic/default", "gemini/default"),
		strings.Replace(SubscriptionSample, "mode: subscription", "mode: subscription\n  agent:\n    args: [--oss]", 1),
	} {
		if _, err := Parse([]byte(changed)); err == nil {
			t.Fatal("accepted incompatible subscription configuration")
		}
	}
	cfg.Aliases["smart"] = []string{"openai/default", "openai/gpt-test"}
	if err := cfg.ValidateReviewer("smart", StrategyAlias, "auto"); err != nil {
		t.Fatalf("subscription auto must allow a fresh review with the working model: %v", err)
	}
	if err := cfg.ValidateReviewer("smart", StrategyAlias, "smart"); err == nil {
		t.Fatal("an explicit reviewer alias must still require a distinct model")
	}
}

func TestSubscriptionAutoReviewerPrefersAlternativesThenFreshSession(t *testing.T) {
	cfg, err := Parse([]byte(SubscriptionSample))
	if err != nil {
		t.Fatal(err)
	}
	for _, implementer := range []string{"openai/default", "anthropic/default"} {
		routes, err := cfg.ReviewerRoutes("smart", "auto", implementer)
		if err != nil || len(routes) != 2 || routes[0].Model == implementer || routes[1].Model != implementer {
			t.Fatalf("review pool for %s: %+v, %v", implementer, routes, err)
		}
	}
	if err := cfg.ValidateReviewer("claude", StrategyAlias, "auto"); err != nil {
		t.Fatalf("Claude-only goals must allow a separate Claude review: %v", err)
	}
}
