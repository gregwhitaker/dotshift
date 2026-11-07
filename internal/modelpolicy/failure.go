// Package modelpolicy observes API failures without interpreting agent prose.
package modelpolicy

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	SubscriptionLimit = "subscription_limit"
	Quota             = "quota_exhausted"
	RateLimit         = "rate_limited"
	Unavailable       = "unavailable"
)

// Failure contains only normalized information, never a provider's raw error
// body (which can contain request content). A zero RetryAt requires an explicit
// retry after credits or access have been restored.
type Failure struct {
	Kind         string    `json:"kind"`
	Status       int       `json:"http_status"`
	RetryAt      time.Time `json:"retry_at"`
	ProviderWide bool      `json:"provider_wide"`
}

func (f *Failure) Error() string {
	if f.Kind == SubscriptionLimit {
		return "subscription usage limit reached"
	}
	return fmt.Sprintf("model API %s (HTTP %d)", f.Kind, f.Status)
}

// Classify handles both native and LiteLLM-wrapped OpenAI/Anthropic errors.
// Authentication, context length, invalid parameters and ordinary model output
// are deliberately not treated as an exhausted allowance.
func Classify(status int, headers http.Header, body []byte, now time.Time) *Failure {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	if len(envelope.Error) == 0 {
		envelope.Error = body // Responses API's flat SSE error event.
	}
	var detail struct {
		Code    string `json:"code"`
		Type    string `json:"type"`
		Message string `json:"message"`
		Details struct {
			Code string `json:"error_code"`
		} `json:"details"`
	}
	_ = json.Unmarshal(envelope.Error, &detail)
	message := strings.ToLower(detail.Message)
	codes := []string{detail.Code, detail.Type, detail.Details.Code}
	quotaCodes := []string{
		"insufficient_quota", "credit_balance_exhausted", "billing_hard_limit_reached",
		"organization_spend_limit_exceeded", "project_spend_limit_exceeded",
		"organization_usage_limit_exceeded", "enforced_spend_limit_reached", "billing_error",
	}
	quota := status == http.StatusPaymentRequired
	for _, code := range quotaCodes {
		for _, actual := range codes {
			quota = quota || actual == code
		}
		// LiteLLM sometimes embeds the provider's JSON error in its message.
		quota = quota || strings.Contains(message, `"`+code+`"`) || strings.Contains(message, "'"+code+"'")
	}
	for _, text := range []string{
		"your credit balance is too low", "credit balance too low",
		"you have reached your specified api usage limits",
		"you have reached your specified workspace api usage limits",
	} {
		quota = quota || strings.Contains(message, text)
	}
	if quota && (status == 400 || status == 402 || status == 429 || status == 200) {
		// Do not use a normal token-rate reset or Retry-After for an exhausted
		// credit balance. Only an explicit restoration date is meaningful.
		return &Failure{Kind: Quota, Status: status, ProviderWide: true, RetryAt: accessReset(message, now)}
	}
	if status == 429 || detail.Code == "rate_limit_exceeded" || detail.Code == "rate_limit_error" || detail.Code == "slow_down" || detail.Type == "rate_limit_error" {
		return &Failure{Kind: RateLimit, Status: status, RetryAt: retryTime(headers, now, time.Minute)}
	}
	if status >= 500 || detail.Code == "server_error" || detail.Code == "server_is_overloaded" || detail.Type == "overloaded_error" || detail.Type == "service_unavailable_error" || detail.Type == "api_error" {
		return &Failure{Kind: Unavailable, Status: status, RetryAt: retryTime(headers, now, 30*time.Second)}
	}
	return nil
}

var accessResetPattern = regexp.MustCompile(`(?:regain access on|access (?:resumes|will resume) on) (\d{4}-\d{2}-\d{2}) at (\d{2}:\d{2}) utc`)

func accessReset(message string, now time.Time) time.Time {
	m := accessResetPattern.FindStringSubmatch(message)
	if len(m) == 3 {
		if reset, err := time.Parse("2006-01-02 15:04", m[1]+" "+m[2]); err == nil && reset.After(now) {
			return reset
		}
	}
	return time.Time{}
}

func retryTime(headers http.Header, now time.Time, fallback time.Duration) time.Time {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0 && seconds <= float64((365*24*time.Hour)/time.Second) {
		return now.Add(max(time.Second, time.Duration(seconds*float64(time.Second))))
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at
	}
	// Use reset headers only for an exhausted bucket. An unrelated bucket's
	// reset must not postpone an otherwise usable model.
	var reset time.Time
	for _, prefix := range []string{
		"x-ratelimit-", "anthropic-ratelimit-",
	} {
		for _, bucket := range []string{"requests", "tokens", "input-tokens", "output-tokens"} {
			remainingKey, resetKey := prefix+"remaining-"+bucket, prefix+"reset-"+bucket
			if prefix == "anthropic-ratelimit-" {
				remainingKey, resetKey = prefix+bucket+"-remaining", prefix+bucket+"-reset"
			}
			if headers.Get(remainingKey) != "0" {
				continue
			}
			value := headers.Get(resetKey)
			at, err := time.Parse(time.RFC3339, value)
			if err != nil {
				if duration, err := time.ParseDuration(value); err == nil {
					at = now.Add(duration)
				}
			}
			if at.After(reset) && at.After(now) {
				reset = at
			}
		}
	}
	if !reset.IsZero() {
		return reset
	}
	return now.Add(fallback)
}
