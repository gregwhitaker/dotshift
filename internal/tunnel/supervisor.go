package tunnel

import (
	"context"
	"errors"
	"io"
	"time"
)

// SuperviseFunnel keeps the public MCP tunnel running until ctx is cancelled.
// It retries setup failures (including an offline Tailscale daemon) and
// unexpected process exits. It never resets unrelated Serve/Funnel mappings.
func SuperviseFunnel(ctx context.Context, port string, out io.Writer, logf func(string, ...any), stateDir string, onURL func(string)) {
	superviseFunnelWithURL(ctx, port, out, logf, time.Second, 5*time.Second, onURL, stateDir)
}

func superviseFunnel(ctx context.Context, port string, out io.Writer, logf func(string, ...any), minDelay, maxDelay time.Duration, stateDir ...string) {
	superviseFunnelWithURL(ctx, port, out, logf, minDelay, maxDelay, nil, stateDir...)
}

func superviseFunnelWithURL(ctx context.Context, port string, out io.Writer, logf func(string, ...any), minDelay, maxDelay time.Duration, onURL func(string), stateDir ...string) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	delay := minDelay
	var lastLog time.Time
	lastError := ""
	for ctx.Err() == nil {
		bin, err := FindTailscale()
		var publicURL string
		if err == nil {
			publicURL, err = PublicURL(ctx, bin)
			if err == nil && onURL != nil {
				onURL(publicURL)
			}
		}
		var f *Funnel
		if err == nil {
			f, err = startFunnel(ctx, bin, port, out, stateDir...)
		}
		if err == nil {
			logf("Tailscale Funnel is serving MCP at %s/mcp", publicURL)
			lastError = ""
			started := time.Now()
			err = watchFunnel(ctx, f, bin, publicURL, maxDelay)
			// A stable connection starts a fresh retry sequence; rapidly
			// exiting processes retain the increasing delay.
			if time.Since(started) >= maxDelay {
				delay = minDelay
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err.Error() != lastError || time.Since(lastLog) >= 30*time.Second {
			logf("Tailscale Funnel unavailable: %v; retrying in %s", err, delay)
			lastError, lastLog = err.Error(), time.Now()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, maxDelay)
	}
}

// A foreground Funnel process can stay alive across a stopped daemon without
// re-registering its listener when the daemon returns. Check the backend too.
func watchFunnel(ctx context.Context, f *Funnel, bin, publicURL string, interval time.Duration) error {
	defer f.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.Done():
			if f.err != nil {
				return f.err
			}
			return errors.New("tunnel process exited")
		case <-ticker.C:
			url, err := PublicURL(ctx, bin)
			if err != nil {
				return err
			}
			if url != publicURL {
				return errors.New("Tailscale address changed")
			}
		}
	}
}
