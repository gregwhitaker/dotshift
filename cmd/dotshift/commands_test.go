package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	tests := []struct {
		args       []string
		model      string
		positional []string
	}{
		{[]string{"-model", "fast", "fix", "it"}, "fast", []string{"fix", "it"}},
		{[]string{"fix", "-model", "fast", "it"}, "fast", []string{"fix", "it"}},
		{[]string{"fix it", "-model=fast"}, "fast", []string{"fix it"}},
		{[]string{"--", "-model", "fast"}, "", []string{"-model", "fast"}},
		{[]string{"-"}, "", []string{"-"}},
		{nil, "", []string{}},
	}
	for _, tt := range tests {
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		model := fs.String("model", "", "")
		parseInterspersed(fs, tt.args)
		if *model != tt.model || !reflect.DeepEqual(fs.Args(), tt.positional) {
			t.Errorf("%q: model %q args %q", tt.args, *model, fs.Args())
		}
	}
}
