package main

import (
	"fmt"
	"runtime/debug"
)

// Release builds set these with -ldflags. Source builds retain Go's VCS data.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func versionString() string {
	revision, date := commit, buildDate
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if revision == "unknown" {
					revision = setting.Value
				}
			case "vcs.time":
				if date == "unknown" {
					date = setting.Value
				}
			}
		}
	}
	return fmt.Sprintf("dotshift %s (commit %s, built %s)", version, revision, date)
}
