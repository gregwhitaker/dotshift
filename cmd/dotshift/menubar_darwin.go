//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func launchMenuBar(ctx context.Context, path, bundle string) error {
	if bundle == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		home, _ := os.UserHomeDir()
		for _, candidate := range []string{filepath.Join(filepath.Dir(exe), "build", "Dotshift.app"), filepath.Join(filepath.Dir(exe), "Dotshift.app"), filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "..")), filepath.Join(home, "Applications", "Dotshift.app"), "/Applications/Dotshift.app"} {
			if filepath.Base(candidate) != "Dotshift.app" {
				continue
			}
			if _, err := os.Stat(filepath.Join(candidate, "Contents", "MacOS", "DotshiftMenuBar")); err == nil {
				bundle = candidate
				break
			}
		}
	}
	if bundle == "" {
		return errors.New("Dotshift.app was not found; run `make menubar`, or use -app /path/to/Dotshift.app")
	}
	bundle, err := filepath.Abs(bundle)
	if err != nil {
		return err
	}
	file, err := writeMenuLaunch(path)
	if err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "/usr/bin/open", "-a", bundle, file).CombinedOutput(); err != nil {
		return fmt.Errorf("open menu bar app: %w: %s", err, out)
	}
	fmt.Println("Opened the Dotshift menu bar app. Quitting it leaves the background service running.")
	return nil
}
