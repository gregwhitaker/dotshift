package mcpintake

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// dotshift is its own OAuth authorization server, with one user: you. A
// client (ChatGPT) registers itself, sends you to the approval page, and you
// approve it by typing the passcode `dotshift connect` shows. MCP clients
// find all of this through the standard metadata documents, so connecting is
// just pasting the MCP URL into the client.

const (
	scope          = "jobs"
	accessTTL      = time.Hour
	refreshTTL     = 30 * 24 * time.Hour
	codeTTL        = 5 * time.Minute
	maxFailures    = 10
	failureWindow  = 10 * time.Minute
	maxOAuthBody   = 64 << 10
	authorizePath  = "/oauth/authorize"
	tokenPath      = "/oauth/token"
	registerPath   = "/oauth/register"
	mcpPath        = "/mcp"
	authMethodNone = "none"
)

type authCode struct {
	clientID    string
	redirectURI string
	challenge   string
	expires     time.Time
}

type oauthServer struct {
	store    *AuthStore
	passcode string
	logf     func(string, ...any)

	mu       sync.Mutex
	codes    map[string]*authCode
	failures []time.Time
}

func (o *oauthServer) authServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         base,
		"authorization_endpoint":                         base + authorizePath,
		"token_endpoint":                                 base + tokenPath,
		"registration_endpoint":                          base + registerPath,
		"scopes_supported":                               []string{scope},
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{authMethodNone, "client_secret_post", "client_secret_basic"},
		"authorization_response_iss_parameter_supported": true,
	})
}

func (o *oauthServer) resourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + mcpPath,
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{scope},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "dotshift",
	})
}

// requireToken guards the MCP endpoint.
func (o *oauthServer) requireToken(next http.Handler) http.Handler {
	verify := func(ctx context.Context, tok string, _ *http.Request) (*auth.TokenInfo, error) {
		clientID, expires, err := o.store.lookup(ctx, accessToken, tok)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, auth.ErrInvalidToken
		}
		if err != nil {
			return nil, err
		}
		return &auth.TokenInfo{Scopes: []string{scope}, Expiration: expires, UserID: clientID}, nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opts := &auth.RequireBearerTokenOptions{ResourceMetadataURL: baseURL(r) + "/.well-known/oauth-protected-resource" + mcpPath}
		auth.RequireBearerToken(verify, opts)(next).ServeHTTP(w, r)
	})
}

// register implements dynamic client registration (RFC 7591).
func (o *oauthServer) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		ClientName   string   `json:"client_name"`
		Scope        string   `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOAuthBody)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body is not JSON client metadata")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirect(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must be https, or http on this machine: "+u)
			return
		}
	}
	if req.AuthMethod == "" {
		req.AuthMethod = "client_secret_basic" // RFC 7591's default
	}
	if req.AuthMethod != authMethodNone && req.AuthMethod != "client_secret_post" && req.AuthMethod != "client_secret_basic" {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method "+req.AuthMethod)
		return
	}
	if len(req.ClientName) > 200 {
		req.ClientName = req.ClientName[:200]
	}
	c := &client{ID: "dsc_" + randomToken()[:24], Name: req.ClientName, RedirectURIs: req.RedirectURIs}
	var secret string
	if req.AuthMethod != authMethodNone {
		secret = randomToken()
		c.SecretHash = hash(secret)
	}
	if err := o.store.addClient(r.Context(), c); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	o.logf("oauth: registered client %q", displayName(c))
	resp := map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        time.Now().Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"token_endpoint_auth_method": req.AuthMethod,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      scope,
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	writeJSON(w, http.StatusCreated, resp)
}

func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return isLoopback(u.Hostname())
	}
	return false
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var approvePage = template.Must(template.New("approve").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Connect to dotshift</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:28rem;margin:3rem auto;padding:0 1rem;color:#1d1d1f;background:#fff}
@media (prefers-color-scheme:dark){body{color:#eee;background:#161616}input{background:#222;color:#eee}}
h1{font-size:1.4rem} .err{color:#c62828} input[type=password]{width:100%;font-size:1.1rem;padding:.5rem;box-sizing:border-box;margin:.5rem 0 1rem}
button{font-size:1rem;padding:.5rem 1.2rem;margin-right:.5rem} code{font-size:.95em}
</style></head><body>
<h1>Connect {{.Client}} to dotshift</h1>
<p>{{.Client}} is asking to start, follow and cancel coding tasks in the repos you allowlisted, on this machine.</p>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="{{.Action}}">
{{range $k, $v := .Params}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
<label for="passcode">Passcode (run <code>dotshift connect</code> to see it)</label>
<input id="passcode" name="passcode" type="password" autocomplete="off" autofocus>
<button name="decision" value="allow" type="submit">Allow</button>
<button name="decision" value="deny" type="submit">Deny</button>
</form></body></html>
`))

var authorizeParams = []string{"response_type", "client_id", "redirect_uri", "state", "code_challenge", "code_challenge_method", "scope", "resource"}

// authorize shows the approval page (GET) and handles its answer (POST).
func (o *oauthServer) authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	get := r.Form.Get
	c, err := o.store.client(r.Context(), get("client_id"))
	if err != nil {
		http.Error(w, "Unknown client. Remove dotshift from the app and add it again.", http.StatusBadRequest)
		return
	}
	redirectURI := get("redirect_uri")
	if redirectURI == "" && len(c.RedirectURIs) == 1 {
		redirectURI = c.RedirectURIs[0]
	}
	if !slices.Contains(c.RedirectURIs, redirectURI) {
		http.Error(w, "The redirect URI is not one this client registered.", http.StatusBadRequest)
		return
	}
	// From here on, problems go back to the client.
	fail := func(code, desc string) {
		q := url.Values{"error": {code}, "error_description": {desc}, "iss": {baseURL(r)}}
		if s := get("state"); s != "" {
			q.Set("state", s)
		}
		http.Redirect(w, r, withQuery(redirectURI, q), http.StatusFound)
	}
	switch {
	case get("response_type") != "code":
		fail("unsupported_response_type", "only response_type=code is supported")
		return
	case get("code_challenge") == "" || get("code_challenge_method") != "S256":
		fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}

	params := map[string]string{}
	for _, k := range authorizeParams {
		if v := get(k); v != "" {
			params[k] = v
		}
	}
	params["redirect_uri"] = redirectURI
	show := func(status int, msg string) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' "+originOf(redirectURI)+"; frame-ancestors 'none'")
		w.WriteHeader(status)
		_ = approvePage.Execute(w, map[string]any{"Client": displayName(c), "Params": params, "Error": msg, "Action": authorizePath})
	}
	if r.Method == http.MethodGet {
		show(http.StatusOK, "")
		return
	}
	if get("decision") == "deny" {
		o.logf("oauth: you denied %q", displayName(c))
		fail("access_denied", "the owner denied access")
		return
	}
	if !o.allowAttempt() {
		show(http.StatusTooManyRequests, "Too many wrong passcodes. Wait a few minutes and try again.")
		return
	}
	if !o.checkPasscode(get("passcode")) {
		o.logf("oauth: wrong passcode for %q", displayName(c))
		show(http.StatusUnauthorized, "That passcode is not right.")
		return
	}
	code := randomToken()
	o.mu.Lock()
	now := time.Now()
	for k, v := range o.codes {
		if now.After(v.expires) {
			delete(o.codes, k)
		}
	}
	o.codes[hash(code)] = &authCode{clientID: c.ID, redirectURI: redirectURI, challenge: get("code_challenge"), expires: now.Add(codeTTL)}
	o.mu.Unlock()
	o.logf("oauth: you approved %q", displayName(c))
	q := url.Values{"code": {code}, "iss": {baseURL(r)}}
	if s := get("state"); s != "" {
		q.Set("state", s)
	}
	http.Redirect(w, r, withQuery(redirectURI, q), http.StatusFound)
}

func (o *oauthServer) allowAttempt() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	cutoff := time.Now().Add(-failureWindow)
	o.failures = slices.DeleteFunc(o.failures, func(t time.Time) bool { return t.Before(cutoff) })
	return len(o.failures) < maxFailures
}

func (o *oauthServer) checkPasscode(got string) bool {
	ok := subtle.ConstantTimeCompare([]byte(normalizePasscode(got)), []byte(normalizePasscode(o.passcode))) == 1
	if !ok {
		o.mu.Lock()
		o.failures = append(o.failures, time.Now())
		o.mu.Unlock()
	}
	return ok && o.passcode != ""
}

// normalizePasscode forgives case, spaces and dashes, so a passcode read off
// one screen and typed on a phone still matches.
func normalizePasscode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// token implements the token endpoint for authorization codes and refresh tokens.
func (o *oauthServer) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBody)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "bad form body")
		return
	}
	get := r.PostForm.Get
	clientID, secret, basic := r.BasicAuth()
	if basic {
		clientID, _ = url.QueryUnescape(clientID)
		secret, _ = url.QueryUnescape(secret)
	} else {
		clientID, secret = get("client_id"), get("client_secret")
	}
	c, err := o.store.client(r.Context(), clientID)
	if err != nil || (c.SecretHash != "" && subtle.ConstantTimeCompare([]byte(hash(secret)), []byte(c.SecretHash)) != 1) {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong secret")
		return
	}

	switch get("grant_type") {
	case "authorization_code":
		o.mu.Lock()
		ac := o.codes[hash(get("code"))]
		delete(o.codes, hash(get("code")))
		o.mu.Unlock()
		switch {
		case ac == nil || time.Now().After(ac.expires) || ac.clientID != c.ID:
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown or expired code")
			return
		case get("redirect_uri") != "" && get("redirect_uri") != ac.redirectURI:
			oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match")
			return
		case !pkceMatches(get("code_verifier"), ac.challenge):
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match")
			return
		}
		o.logf("oauth: %q connected", displayName(c))
	case "refresh_token":
		owner, _, err := o.store.lookup(r.Context(), refreshToken, get("refresh_token"))
		if err != nil || owner != c.ID {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown or expired refresh token")
			return
		}
		if ok, err := o.store.consume(r.Context(), refreshToken, get("refresh_token")); err != nil || !ok {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token already used")
			return
		}
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
		return
	}

	o.store.pruneExpired(r.Context())
	access, err := o.store.issue(r.Context(), accessToken, c.ID, accessTTL)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	refresh, err := o.store.issue(r.Context(), refreshToken, c.ID, refreshTTL)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

func pkceMatches(verifier, challenge string) bool {
	if verifier == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(pkceChallenge(verifier)), []byte(challenge)) == 1
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func displayName(c *client) string {
	if c.Name != "" {
		return c.Name
	}
	return "An MCP client"
}

func withQuery(raw string, q url.Values) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	existing := u.Query()
	for k, vs := range q {
		existing[k] = vs
	}
	u.RawQuery = existing.Encode()
	return u.String()
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
