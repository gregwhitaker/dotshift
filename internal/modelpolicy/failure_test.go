package modelpolicy

import (
	"net/http"
	"testing"
	"time"
)

func TestAPIFailureClassification(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, body, kind string
		status           int
		permanent        bool
	}{
		{"OpenAI credits", `{"error":{"code":"credit_balance_exhausted"}}`, Quota, 429, true},
		{"OpenAI legacy quota", `{"error":{"code":"insufficient_quota"}}`, Quota, 429, true},
		{"Anthropic credits", `{"error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`, Quota, 400, true},
		{"Anthropic tier cap", `{"error":{"type":"rate_limit_error","details":{"error_code":"enforced_spend_limit_reached"}}}`, Quota, 429, true},
		{"Anthropic workspace cap", `{"error":{"type":"invalid_request_error","message":"You have reached your specified workspace API usage limits"}}`, Quota, 400, true},
		{"LiteLLM wrapped quota", `{"error":{"message":"RateLimitError: OpenAIException - {'code': 'insufficient_quota'}"}}`, Quota, 429, true},
		{"billing", `{"error":{"type":"billing_error"}}`, Quota, 402, true},
		{"rate limit", `{"error":{"code":"rate_limit_exceeded"}}`, RateLimit, 429, false},
		{"flat SSE limit", `{"type":"error","code":"rate_limit_exceeded"}`, RateLimit, 200, false},
		{"flat SSE slowdown", `{"type":"error","code":"slow_down"}`, RateLimit, 200, false},
		{"flat SSE overload", `{"type":"error","code":"server_is_overloaded"}`, Unavailable, 200, false},
		{"overloaded", `{"error":{"type":"overloaded_error"}}`, Unavailable, 529, false},
		{"context full", `{"error":{"code":"context_length_exceeded","message":"Too many tokens"}}`, "", 400, false},
		{"bad parameter", `{"error":{"message":"Unknown parameter: max_tokens"}}`, "", 400, false},
		{"auth", `{"error":{"type":"authentication_error"}}`, "", 401, false},
		{"goal budget", `{"error":{"type":"BudgetExceededError","message":"Project budget exceeded"}}`, "", 400, false},
		{"ordinary output", `{"output_text":"We ran out of tokens"}`, "", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A normal rate-reset header must never re-enable depleted credits.
			headers := http.Header{"Retry-After": {"20"}}
			got := Classify(tc.status, headers, []byte(tc.body), now)
			if tc.kind == "" {
				if got != nil {
					t.Fatalf("misclassified: %+v", got)
				}
				return
			}
			if got == nil || got.Kind != tc.kind || got.ProviderWide != (tc.kind == Quota) || got.RetryAt.IsZero() != tc.permanent {
				t.Fatalf("failure = %+v", got)
			}
		})
	}
}

func TestRetryTimesAndExplicitCreditReset(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		headers http.Header
		delay   time.Duration
	}{
		{http.Header{"Retry-After": {"17"}}, 17 * time.Second},
		{http.Header{"Retry-After": {now.Add(time.Minute).Format(http.TimeFormat)}}, time.Minute},
		{http.Header{"Retry-After": {"NaN"}}, time.Minute},
		{http.Header{"Retry-After": {"1e99"}}, time.Minute},
		{http.Header{"Retry-After": {"0"}}, time.Second},
		{http.Header{"X-Ratelimit-Remaining-Tokens": {"0"}, "X-Ratelimit-Reset-Tokens": {"2m3s"}}, 123 * time.Second},
		{http.Header{"Anthropic-Ratelimit-Requests-Remaining": {"0"}, "Anthropic-Ratelimit-Requests-Reset": {now.Add(45 * time.Second).Format(time.RFC3339)}}, 45 * time.Second},
		{http.Header{"X-Ratelimit-Remaining-Tokens": {"100"}, "X-Ratelimit-Reset-Tokens": {"1h"}}, time.Minute},
	} {
		got := Classify(429, tc.headers, nil, now)
		if got.RetryAt.Sub(now) != tc.delay {
			t.Errorf("%v: delay = %v, want %v", tc.headers, got.RetryAt.Sub(now), tc.delay)
		}
	}
	body := `{"error":{"type":"rate_limit_error","message":"You will regain access on 2026-12-01 at 00:00 UTC.","details":{"error_code":"enforced_spend_limit_reached"}}}`
	got := Classify(429, nil, []byte(body), now)
	if got.Kind != Quota || got.RetryAt.Format(time.RFC3339) != "2026-12-01T00:00:00Z" {
		t.Fatalf("explicit reset = %+v", got)
	}
}
