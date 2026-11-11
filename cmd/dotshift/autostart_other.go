//go:build !darwin

package main

import (
	"context"
	"errors"
)

func configureAutostart(context.Context, string, bool) error {
	return errors.New("automatic login startup currently supports macOS; run dotshift serve under your service manager on other platforms")
}
func startInstalledService(context.Context, string, bool) (bool, error) { return false, nil }
func unloadInstalledService(context.Context, string) (bool, error)      { return false, nil }
