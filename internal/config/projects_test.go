package config

import (
	"strings"
	"testing"
)

func TestProjectConfiguration(t *testing.T) {
	base := "aliases:\n  smart: [openai/default]\njobs:\n  mode: subscription\n"
	for _, tc := range []struct{ yaml, want string }{
		{"", ""},
		{"projects:\n  root: ~/workspace\n  test_profiles:\n    go: go test ./...\n", ""},
		{"projects:\n  root: relative\n  test_profiles:\n    go: go test ./...\n", "absolute path"},
		{"projects:\n  root: /tmp/projects\n", "at least one"},
		{"projects:\n  root: /tmp/projects\n  test_profiles:\n    go: ' '\n", "test command is required"},
		{"projects:\n  test_profiles:\n    Bad: npm test\n", "names must be"},
	} {
		_, err := Parse([]byte(base + tc.yaml))
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: got %v, want %q", tc.yaml, err, tc.want)
		}
	}
}
