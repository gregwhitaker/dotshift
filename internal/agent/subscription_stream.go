package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/modelpolicy"
)

// Only machine error events affect routing. Model prose, command output, and
// files containing phrases like "usage limit" must never trigger a switch.
type subscriptionStream struct {
	provider   string
	task       Task
	out        io.Writer
	cancel     context.CancelFunc
	line       []byte
	oversized  bool
	err        error
	result     []byte
	seenResult bool
}

func (s *subscriptionStream) Write(p []byte) (int, error) {
	n, err := s.out.Write(p)
	if err != nil {
		return n, err
	}
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		if len(s.line)+len(part) > 2*1024*1024 {
			s.oversized = true
			s.line = nil
		}
		if !s.oversized {
			s.line = append(s.line, part...)
		}
		if i < 0 {
			break
		}
		if !s.oversized {
			s.event(s.line)
		}
		s.line, s.oversized = nil, false
		p = p[i+1:]
	}
	return n, nil
}

type cliError struct {
	Code                string `json:"code"`
	Type                string `json:"type"`
	Message             string `json:"message"`
	CodexErrorInfo      string `json:"codex_error_info"`
	CodexErrorInfoCamel string `json:"codexErrorInfo"`
	ResetsAt            int64  `json:"resets_at"`
}

func (s *subscriptionStream) event(line []byte) {
	var e struct {
		Type       string          `json:"type"`
		Message    string          `json:"message"`
		Error      json.RawMessage `json:"error"`
		IsError    bool            `json:"is_error"`
		Subtype    string          `json:"subtype"`
		Result     string          `json:"result"`
		Structured json.RawMessage `json:"structured_output"`
		RateLimit  struct {
			Status   string `json:"status"`
			ResetsAt int64  `json:"resetsAt"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(line, &e) != nil {
		return
	}
	var detail cliError
	engineError := false
	if s.provider == "openai" && (e.Type == "error" || e.Type == "turn.failed") {
		engineError = true
		if len(e.Error) > 0 {
			_ = json.Unmarshal(e.Error, &detail)
		} else {
			_ = json.Unmarshal(line, &detail)
		}
		if detail.Message == "" {
			detail.Message = e.Message
		}
	} else if s.provider == "anthropic" {
		if e.Type == "rate_limit_event" && e.RateLimit.Status == "rejected" {
			s.limit(e.RateLimit.ResetsAt)
			return
		}
		// Claude marks synthetic API-error assistant messages with an error
		// code outside the model-authored message body.
		if e.Type == "assistant" && len(e.Error) > 0 && string(e.Error) != "null" {
			engineError = true
			if json.Unmarshal(e.Error, &detail.Code) != nil {
				_ = json.Unmarshal(e.Error, &detail)
			}
		}
		if e.Type == "result" {
			s.seenResult = true
			if e.IsError || e.Subtype != "success" {
				if s.err == nil {
					s.err = errors.New("Claude Code did not complete successfully; see the job log")
				}
				return
			}
			if s.task.OutputSchemaFile != "" {
				if len(e.Structured) == 0 || string(e.Structured) == "null" {
					if s.err == nil {
						s.err = errors.New("Claude Code returned no structured goal report")
					}
				} else {
					s.result = append([]byte(nil), e.Structured...)
				}
			} else {
				s.result = []byte(e.Result)
			}
		}
	}
	if !engineError {
		return
	}
	code := strings.ToLower(detail.Code + " " + detail.Type + " " + detail.CodexErrorInfo + " " + detail.CodexErrorInfoCamel)
	message := strings.ToLower(detail.Message)
	limit := strings.Contains(code, "usage_limit") || strings.Contains(code, "usagelimit") || strings.Contains(code, "rate_limit")
	// Codex exec versions that omit the error code still emit typed engine
	// errors. Match only known allowance messages, never arbitrary output.
	limit = limit || strings.Contains(message, "you've hit your usage limit") || strings.Contains(message, "usage limit has been reached") || strings.Contains(message, "rate limit reached") || strings.Contains(message, "429 too many requests")
	if limit {
		s.limit(detail.ResetsAt)
		return
	}
	if s.err == nil && e.Type == "turn.failed" {
		s.err = errors.New("subscription CLI reported an execution error; see the job log")
	}
}

func (s *subscriptionStream) limit(reset int64) {
	failure := &modelpolicy.Failure{Kind: modelpolicy.SubscriptionLimit, ProviderWide: true}
	if at := time.Unix(reset, 0); at.After(time.Now()) && at.Before(time.Now().Add(30*24*time.Hour)) {
		failure.RetryAt = at
	}
	var previous *modelpolicy.Failure
	if errors.As(s.err, &previous) && previous.RetryAt.After(failure.RetryAt) {
		// A generic follow-up error must not discard a reset learned from
		// the preceding rate_limit_event for the same failed attempt.
		failure.RetryAt = previous.RetryAt
	}
	s.err = failure
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *subscriptionStream) finish() error {
	if len(s.line) > 0 && !s.oversized {
		s.event(s.line)
	}
	if s.err != nil {
		return s.err
	}
	if s.provider == "anthropic" {
		if !s.seenResult {
			return errors.New("Claude Code exited without a result event")
		}
		return os.WriteFile(s.task.LastMessageFile, s.result, 0o600)
	}
	return nil
}
