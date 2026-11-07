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
	if err := cfg.ValidateReviewer("smart", StrategyAlias, "auto"); err == nil {
		t.Fatal("default model identity cannot establish an independent same-provider reviewer")
	}
}
