package mcpintake

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gregwhitaker/dotshift/internal/jobs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configure the MCP intake's HTTP handler.
type Options struct {
	Jobs         jobs.Service
	DefaultModel string
	Models       []string
	Auth         *AuthStore
	// Passcode is what you type on the approval page to connect a client.
	Passcode string
	// PublicURL is the tunnel's URL. When set, requests for other hosts
	// (other than this machine) are refused; when empty, each request's own
	// host is used, as the tunnel forwarded it.
	PublicURL string
	// Logf, if set, gets a line for each tool call and OAuth event.
	Logf func(format string, args ...any)
}

// NewHandler returns the HTTP handler serving the MCP endpoint at /mcp, the
// OAuth endpoints that protect it, and /healthz.
func NewHandler(o Options) (http.Handler, error) {
	if o.Passcode == "" {
		return nil, fmt.Errorf("no passcode set")
	}
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var public *url.URL
	if o.PublicURL != "" {
		u, err := url.Parse(strings.TrimRight(o.PublicURL, "/"))
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("public URL %q is not a URL", o.PublicURL)
		}
		public = u
	}

	srv := NewServer(o.Jobs, o.DefaultModel, o.Models, logf)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		// Tunnels forward the public Host header to this loopback listener,
		// which the SDK's DNS-rebinding guard would refuse. Every MCP request
		// needs a bearer token, which a rebinding page can't get, and
		// checkHost below refuses unexpected hosts when PublicURL is set.
		DisableLocalhostProtection: true,
		SessionTimeout:             time.Hour,
	})
	oa := &oauthServer{store: o.Auth, passcode: o.Passcode, logf: logf, codes: map[string]*authCode{}}

	mux := http.NewServeMux()
	mux.Handle(mcpPath, oa.requireToken(streamable))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource" + mcpPath} {
		mux.HandleFunc(p, cors(oa.resourceMetadata))
	}
	for _, p := range []string{
		"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server" + mcpPath,
		"/.well-known/openid-configuration", "/.well-known/openid-configuration" + mcpPath, mcpPath + "/.well-known/openid-configuration",
	} {
		mux.HandleFunc(p, cors(oa.authServerMetadata))
	}
	mux.HandleFunc(registerPath, cors(oa.register))
	mux.HandleFunc(tokenPath, cors(oa.token))
	mux.HandleFunc(authorizePath, oa.authorize)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base, ok := requestBase(r, public)
		if !ok {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), baseKey{}, base)))
	}), nil
}

type baseKey struct{}

// baseURL is the URL clients reach dotshift at, e.g. https://abc.trycloudflare.com.
func baseURL(r *http.Request) string {
	base, _ := r.Context().Value(baseKey{}).(string)
	return base
}

// requestBase works out the URL clients reach dotshift at. With a public URL
// configured, it is always that URL, and only requests for its host or this
// machine's own names are served (a tunnel may forward either).
func requestBase(r *http.Request, public *url.URL) (string, bool) {
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = strings.TrimSpace(strings.Split(fh, ",")[0])
	}
	if host == "" || strings.ContainsAny(host, "/\\ \"'<>@?#") {
		return "", false
	}
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	if public != nil {
		if !strings.EqualFold(host, public.Host) && !isLoopback(strings.Trim(hostname, "[]")) {
			return "", false
		}
		return public.String(), true
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])); p == "https" || p == "http" {
		scheme = p
	}
	return scheme + "://" + host, true
}

// cors lets browser-based MCP clients read the OAuth endpoints.
func cors(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}
