package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"
	"time"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/jobs"
	"github.com/gregwhitaker/dotshift/internal/litellm"
	"github.com/gregwhitaker/dotshift/internal/router"
	usagelog "github.com/gregwhitaker/dotshift/internal/usage"
)

// startUsage runs the endpoint the router posts usage records to and tells
// the router where it is. If it can't, it warns, turns the router's usage
// callback off and carries on: usage is useful but not worth failing serve
// over. The returned function shuts the endpoint down.
func startUsage(ctx context.Context, path string, cfg *config.Config, r *router.Router) func() {
	noop := func() {}
	if !cfg.Router.UsageEnabled() {
		return noop
	}
	stop, err := listenUsage(ctx, path, cfg, r)
	if err == nil {
		return stop
	}
	fmt.Fprintf(os.Stderr, "warning: usage recording is off: %v\n", err)
	fmt.Fprintln(os.Stderr, "  set router.usage_listen to an address the container can reach, or to off to hide this")
	cfg.Router.UsageListen = config.UsageOff
	if err := writeLiteLLMConfig(path, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: rewriting the router config: %v\n", err)
	}
	return noop
}

func listenUsage(ctx context.Context, path string, cfg *config.Config, r *router.Router) (func(), error) {
	host := cfg.Router.UsageListen
	if host == "" {
		var err error
		if host, err = usageHost(ctx, r); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, err
	}
	dir, err := stateDir(path)
	if err != nil {
		ln.Close()
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		ln.Close()
		return nil, err
	}
	store, err := jobs.OpenStore(filepath.Join(dir, "jobs.db"))
	if err != nil {
		ln.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/usage", &usagelog.Receiver{Path: filepath.Join(dir, usagelog.File), Token: token, OnRecords: store.RecordUsage})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	port := ln.Addr().(*net.TCPAddr).Port
	r.SendUsage(fmt.Sprintf("http://%s:%d/usage", router.HostAlias, port), token)
	return func() {
		// Give the router's last batch a moment to land before closing.
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		_ = store.Close()
	}, nil
}

// usageHost picks the host address the router container can reach. Docker
// Desktop (macOS, Windows) forwards host.docker.internal to this machine's
// loopback; on Linux it is the Docker bridge's gateway address.
func usageHost(ctx context.Context, r *router.Router) (string, error) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return "127.0.0.1", nil
	}
	return r.BridgeGateway(ctx)
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeLiteLLMConfig(path string, cfg *config.Config) error {
	dir, err := stateDir(path)
	if err != nil {
		return err
	}
	out, err := litellm.Marshal(litellm.Build(cfg))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "litellm.yaml"), out, 0o644)
}

// runUsage prints calls, tokens and cost per alias and model, over the given
// period (e.g. "24h", "7d") or all time.
func runUsage(path string, args []string) error {
	if len(args) > 1 {
		return errors.New("usage takes at most one period, e.g. `dotshift usage 7d`")
	}
	var since time.Time
	label := "all time"
	if len(args) == 1 {
		d, err := usagelog.ParsePeriod(args[0])
		if err != nil {
			return err
		}
		since = time.Now().Add(-d)
		label = "last " + args[0]
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if cfg.UsesSubscriptions() {
		fmt.Println("Subscription mode uses CLI plan allowances. USD costs and remaining allowance are not metered by dotshift; check the providers' own usage screens. Use `dotshift jobs` for progress and observed limit failures.")
		return nil
	}
	dir, err := stateDir(path)
	if err != nil {
		return err
	}
	records, err := usagelog.Load(filepath.Join(dir, usagelog.File))
	if err != nil {
		return err
	}
	rows := usagelog.Summarize(records, since)
	if len(rows) == 0 {
		fmt.Printf("No usage recorded (%s).\n", label)
		if !cfg.Router.UsageEnabled() {
			fmt.Println("Usage recording is off (router.usage_listen: off).")
		}
		return nil
	}
	fmt.Printf("Usage, %s:\n\n", label)
	printUsage(os.Stdout, rows)
	return nil
}

func printUsage(w io.Writer, rows []usagelog.Row) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tMODEL\tREQUESTS\tFAILED\tINPUT TOKENS\tOUTPUT TOKENS\tCOST (USD)")
	var total usagelog.Row
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%s\n", r.Alias, r.Model, r.Requests, r.Failures, r.InputTokens, r.OutputTokens, dollars(r.Cost))
		total.Requests += r.Requests
		total.Failures += r.Failures
		total.InputTokens += r.InputTokens
		total.OutputTokens += r.OutputTokens
		total.Cost += r.Cost
	}
	fmt.Fprintf(tw, "total\t\t%d\t%d\t%d\t%d\t%s\n", total.Requests, total.Failures, total.InputTokens, total.OutputTokens, dollars(total.Cost))
	tw.Flush()
}

func dollars(c float64) string {
	switch {
	case c > 0 && c < 0.0001:
		return "<0.0001"
	case c > 0 && c < 0.01:
		return fmt.Sprintf("%.4f", c)
	}
	return fmt.Sprintf("%.2f", c)
}
