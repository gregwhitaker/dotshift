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
func SuperviseFunnel(ctx context.Context, port string, out io.Writer, logf func(string, ...any), stateDir string) {
	superviseFunnel(ctx, port, out, logf, time.Second, 30*time.Second, stateDir)
}

func superviseFunnel(ctx context.Context, port string, out io.Writer, logf func(string, ...any), minDelay, maxDelay time.Duration, stateDir ...string) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	delay := minDelay
	for ctx.Err() == nil {
		bin, err := FindTailscale()
		var publicURL string
		if err == nil {
			publicURL, err = PublicURL(ctx, bin)
		}
		var f *Funnel
		if err == nil {
			f, err = startFunnel(ctx, bin, port, out, stateDir...)
		}
		if err == nil {
			logf("Tailscale Funnel is serving MCP at %s/mcp", publicURL)
			started := time.Now()
			select {
			case <-ctx.Done():
				f.Stop()
				return
			case <-f.Done():
				// A stable connection starts a fresh retry sequence; rapidly
				// exiting processes retain the increasing delay.
				if time.Since(started) >= maxDelay {
					delay = minDelay
				}
				err = f.err
				if err == nil {
					err = errors.New("tunnel process exited")
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		logf("Tailscale Funnel unavailable: %v; retrying in %s", err, delay)
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
