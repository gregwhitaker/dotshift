package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/check"
)

func TestCheckReportsCodingProtocolFailure(t *testing.T) {
	var out bytes.Buffer
	failed := printCheck(&out, []check.Result{{
		Target:     check.Target{Alias: "smart", Model: "openai/test"},
		Structured: check.Outcome{Err: errors.New("invalid goal JSON")},
	}})
	if failed != 1 || !strings.Contains(out.String(), "GOAL JSON") || !strings.Contains(out.String(), "openai/test goal JSON: invalid goal JSON") {
		t.Fatalf("failed = %d, output = %s", failed, out.String())
	}
}
