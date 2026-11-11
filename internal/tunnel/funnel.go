// Package tunnel gives the MCP intake a public https URL with Tailscale
// Funnel, so ChatGPT can reach it while the router stays on this machine.
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// macAppCLI is where the Tailscale app for macOS keeps its CLI when
// `tailscale` isn't on PATH.
const macAppCLI = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

// FindTailscale returns the tailscale CLI's path.
func FindTailscale() (string, error) {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p, nil
	}
	if _, err := os.Stat(macAppCLI); err == nil {
		return macAppCLI, nil
	}
	return "", errors.New("the tailscale CLI was not found; install Tailscale (https://tailscale.com/download) and log in")
}

// PublicURL returns the https URL Funnel serves this machine at, e.g.
// https://laptop.tail1234.ts.net. It fails when Tailscale isn't running or
// logged in.
func PublicURL(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	var st struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	if st.BackendState != "Running" {
		return "", fmt.Errorf("Tailscale is %s; run `tailscale up` and log in", strings.ToLower(st.BackendState))
	}
	name := strings.TrimSuffix(st.Self.DNSName, ".")
	if name == "" {
		return "", errors.New("Tailscale didn't report this machine's name; turn on MagicDNS for your tailnet")
	}
	return "https://" + name, nil
}

// Funnel is a running `tailscale funnel` process.
type Funnel struct {
	cmd  *exec.Cmd
	done chan struct{}
	out  *lockedBuffer
	err  error // read only after done is closed
}

// startupGrace is how long a funnel must stay up to count as started. When
// Funnel isn't allowed for the machine, tailscale exits within it.
var startupGrace = 3 * time.Second

// StartFunnel runs `tailscale funnel PORT` until Stop, publishing
// http://127.0.0.1:PORT at the machine's https URL. Its output also goes to
// log. Funnel needs to be turned on for the tailnet once; when it isn't,
// tailscale prints a link to do that and the error carries it.
func StartFunnel(bin, port string, log io.Writer) (*Funnel, error) {
	return startFunnel(context.Background(), bin, port, log)
}

func startFunnel(ctx context.Context, bin, port string, log io.Writer, stateDirs ...string) (*Funnel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var stateDir string
	if len(stateDirs) > 0 {
		stateDir = stateDirs[0]
	}
	if err := recoverFunnel(stateDir); err != nil {
		return nil, fmt.Errorf("recovering tunnel: %w", err)
	}
	f := &Funnel{done: make(chan struct{}), out: &lockedBuffer{}}
	f.cmd = exec.Command(bin, "funnel", port)
	prepareFunnelProcess(f.cmd)
	// The macOS CLI can leave a descendant holding its output pipe open.
	// Bound output draining after the CLI exits so shutdown can finish.
	f.cmd.WaitDelay = 2 * time.Second
	f.cmd.Stdout = io.MultiWriter(f.out, log)
	f.cmd.Stderr = f.cmd.Stdout
	if err := f.cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting tailscale funnel: %w", err)
	}
	if err := rememberFunnel(stateDir, f.cmd.Process.Pid); err != nil {
		killFunnelProcess(f.cmd)
		_ = f.cmd.Wait()
		return nil, fmt.Errorf("recording tunnel ownership: %w", err)
	}
	go func() {
		f.err = f.cmd.Wait()
		// A wrapper can exit without its child. Reap the process group even
		// on an unexpected exit, before another attempt can claim the port.
		killFunnelProcess(f.cmd)
		forgetFunnel(stateDir, f.cmd.Process.Pid)
		close(f.done)
	}()
	select {
	case <-ctx.Done():
		f.Stop()
		return nil, ctx.Err()
	case <-f.done:
		msg := strings.TrimSpace(f.out.String())
		if msg == "" && f.err != nil {
			msg = f.err.Error()
		}
		return nil, fmt.Errorf("tailscale funnel %s stopped: %s", port, msg)
	case <-time.After(startupGrace):
		return f, nil
	}
}

// Stop ends the funnel, which takes the public URL down.
func (f *Funnel) Stop() {
	select {
	case <-f.done:
		return
	default:
	}
	interruptFunnelProcess(f.cmd)
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		killFunnelProcess(f.cmd)
		<-f.done
	}
}

// Done is closed when the funnel process exits.
func (f *Funnel) Done() <-chan struct{} { return f.done }

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
