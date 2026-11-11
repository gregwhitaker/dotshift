//go:build darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
)

type launchAgent struct {
	config, executable, directory, state, label, domain, file, pathEnv string
	run                                                                func(context.Context, ...string) error
}

func launchAgentFor(path string) (*launchAgent, error) {
	path, err := canonicalConfig(path)
	if err != nil {
		return nil, err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	label := fmt.Sprintf("com.dotshift.service.%x", sha256.Sum256([]byte(path)))[:33]
	state, err := stateDir(path)
	if err != nil {
		return nil, err
	}
	return &launchAgent{
		config: path, executable: exe, directory: cwd, state: state, label: label,
		domain:  fmt.Sprintf("gui/%d", os.Getuid()),
		file:    filepath.Join(homeDir, "Library", "LaunchAgents", label+".plist"),
		pathEnv: os.Getenv("PATH"), run: runLaunchctl,
	}, nil
}

func runLaunchctl(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", args[0], err, bytes.TrimSpace(out))
	}
	return nil
}

func xmlString(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (a *launchAgent) plist() []byte {
	// Only PATH is persisted; subscription credentials remain with the CLIs.
	// SuccessfulExit=false restarts crashes, while an intentional stop stays
	// stopped until the next login or an explicit start.
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>serve</string><string>-config</string><string>%s</string></array>
<key>WorkingDirectory</key><string>%s</string>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string></dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>10</integer>
<key>ExitTimeOut</key><integer>45</integer>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlString(a.label), xmlString(a.executable), xmlString(a.config), xmlString(a.directory), xmlString(a.pathEnv), xmlString(filepath.Join(a.state, "service.log")), xmlString(filepath.Join(a.state, "service.log"))))
}

func (a *launchAgent) target() string { return a.domain + "/" + a.label }
func (a *launchAgent) installed() (bool, error) {
	_, err := os.Stat(a.file)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func (a *launchAgent) loaded(ctx context.Context) bool { return a.run(ctx, "print", a.target()) == nil }

func (a *launchAgent) start(ctx context.Context) error {
	if err := a.run(ctx, "enable", a.target()); err != nil {
		return err
	}
	if a.loaded(ctx) {
		return a.run(ctx, "kickstart", a.target())
	}
	if err := a.run(ctx, "bootstrap", a.domain, a.file); err != nil {
		// Concurrent starters can both observe an unloaded job.
		if !a.loaded(ctx) {
			return err
		}
	}
	return nil
}

func (a *launchAgent) unload(ctx context.Context) error {
	if !a.loaded(ctx) {
		return nil
	}
	if err := a.run(ctx, "bootout", a.target()); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if unlock, err := lockService(a.state); err == nil {
			unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for service shutdown: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func configureAutostart(ctx context.Context, path string, install bool) error {
	a, err := launchAgentFor(path)
	if err != nil {
		return err
	}
	if !install {
		if err := a.unload(ctx); err != nil {
			return err
		}
		if err := os.Remove(a.file); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("Removed automatic login startup. Saved work is retained.")
		return nil
	}
	cfg, err := config.Load(a.config)
	if err != nil {
		return err
	}
	if !cfg.UsesSubscriptions() {
		return errors.New("automatic login startup currently supports subscription mode")
	}
	if err := os.MkdirAll(a.state, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.file), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(a.state, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	log.Close()
	// Stop through the existing service API so in-flight work is checkpointed.
	if _, err := stopService(ctx, a.config); err != nil {
		return err
	}
	if err := a.unload(ctx); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.file), ".dotshift-*.plist")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(a.plist())
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), a.file); err != nil {
		return err
	}
	if err := a.start(ctx); err != nil {
		return err
	}
	if _, err := ensureUI(ctx, a.config, false); err != nil {
		return err
	}
	fmt.Println("Dotshift starts at login and restarts after a crash. Use dotshift stop to pause it, or dotshift service uninstall to remove automatic startup.")
	return nil
}

func startInstalledService(ctx context.Context, path string, localOnly bool) (bool, error) {
	a, err := launchAgentFor(path)
	if err != nil {
		return false, err
	}
	installed, err := a.installed()
	if !installed || err != nil {
		return false, err
	}
	if localOnly {
		return false, errors.New("the installed service uses the saved MCP configuration; uninstall automatic startup before using -local-only")
	}
	return true, a.start(ctx)
}

func unloadInstalledService(ctx context.Context, path string) (bool, error) {
	a, err := launchAgentFor(path)
	if err != nil {
		return false, err
	}
	installed, err := a.installed()
	if !installed || err != nil {
		return false, err
	}
	return true, a.unload(ctx)
}
