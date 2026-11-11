//go:build !darwin

package main

import (
	"context"
	"errors"
)

func launchMenuBar(context.Context, string, string) error {
	return errors.New("the Dotshift menu bar app is available on macOS")
}
