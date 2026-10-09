package mcpintake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregwhitaker/dotshift/internal/agent"
	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/jobs"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const testPasscode = "abcde-fghjk-mnpqr-stuvw"

// shellAgent stands in for Codex: it runs a shell script in the worktree.
// The script sees the prompt on stdin and $SUMMARY as the notes file.
type shellAgent struct{ script string }

func (a shellAgent) Name() string { return "shell" }

func (a shellAgent) Command(ctx context.Context, t agent.Task) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", a.script)
	cmd.Dir = t.Dir
	cmd.Env = append(os.Environ(), "SUMMARY="+t.LastMessageFile)
	return cmd
}

const editScript = `cat > /dev/null; echo "line" >> hello.txt; echo "Added a line to hello.txt. Should I also update the README?" > "$SUMMARY"`

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRunner(t *testing.T, script string) *jobs.Runner {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	run(t, "", "git", "init", "--quiet", "-b", "main", repo)
	run(t, repo, "git", "config", "user.email", "greg@example.com")
	run(t, repo, "git", "config", "user.name", "Greg")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# repo\n"), 0o644)
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "--quiet", "-m", "init")
	cfg, err := config.Parse([]byte(fmt.Sprintf(
		"providers:\n  openai: {}\naliases:\n  smart: [openai/gpt-5]\n  fast: [openai/gpt-5-mini]\nrepos:\n  demo:\n    path: %s\n    test: test -f hello.txt\njobs:\n  timeout: 30s\n", repo)))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	store, err := jobs.OpenStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &jobs.Runner{Config: cfg, Store: store, Agent: shellAgent{script}, BaseURL: "http://127.0.0.1:4000/v1", APIKey: "sk-test", StateDir: state}
}

func newTestServer(t *testing.T, runner *jobs.Runner, publicURL string) (*httptest.Server, *AuthStore) {
	t.Helper()
	store, err := OpenAuthStore(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h, err := NewHandler(Options{
		Jobs: runner, DefaultModel: runner.Config.Jobs.Model, Models: runner.Config.AliasNames(),
		Auth: store, Passcode: testPasscode, PublicURL: publicURL, Logf: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, store
}

var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// approve plays the person on the approval page: it loads the page, then
// submits it with the passcode, and returns the redirect it gets back.
func approve(t *testing.T, authURL, passcode string) *url.URL {
	t.Helper()
	resp, err := http.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "Connect Test client to dotshift") {
		t.Fatalf("approval page: %d\n%s", resp.StatusCode, page)
	}
	u, _ := url.Parse(authURL)
	form := u.Query()
	form.Set("passcode", passcode)
	form.Set("decision", "allow")
	u.RawQuery = ""
	resp, err = noRedirects.PostForm(u.String(), form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("approval: status %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// connect runs the whole MCP OAuth flow with the SDK's own client: discovery,
// dynamic registration, approval, PKCE code exchange.
func connect(t *testing.T, srv *httptest.Server, authMethod string) *mcp.ClientSession {
	t.Helper()
	h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			RedirectURIs: []string{"http://127.0.0.1:9/callback"}, ClientName: "Test client",
			TokenEndpointAuthMethod: authMethod, GrantTypes: []string{"authorization_code", "refresh_token"},
		}},
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			loc := approve(t, args.URL, "ABCDE FGHJK MNPQR STUVW") // typed sloppily on purpose
			q := loc.Query()
			if q.Get("error") != "" {
				return nil, fmt.Errorf("authorization error: %s", q.Get("error"))
			}
			return &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", OAuthHandler: h}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		var msg string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msg += tc.Text
			}
		}
		return nil, msg
	}
	data, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(data, &out)
	return out, ""
}

func waitFinished(t *testing.T, cs *mcp.ClientSession, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, errMsg := call(t, cs, "task_status", map[string]any{"task_id": id})
		if errMsg != "" {
			t.Fatal(errMsg)
		}
		if out["finished"] == true {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return nil
}

func TestToolsOverOAuth(t *testing.T) {
	for _, method := range []string{"none", "client_secret_post", ""} {
		t.Run("auth method "+method, func(t *testing.T) {
			runner := newRunner(t, editScript)
			srv, _ := newTestServer(t, runner, "")
			cs := connect(t, srv, method)

			tools, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, tool := range tools.Tools {
				names = append(names, tool.Name)
			}
			if got := strings.Join(names, ","); got != "cancel_task,list_repos,send_input,start_task,task_result,task_status" {
				t.Fatalf("tools = %s", got)
			}

			repos, _ := call(t, cs, "list_repos", nil)
			if fmt.Sprint(repos["repos"]) != "[map[has_tests:true name:demo]]" || repos["default_model"] != "smart" {
				t.Fatalf("list_repos = %v", repos)
			}

			if _, msg := call(t, cs, "start_task", map[string]any{"repo": "elsewhere", "instructions": "x"}); !strings.Contains(msg, "not allowlisted") {
				t.Fatalf("start_task outside the allowlist: %q", msg)
			}

			started, msg := call(t, cs, "start_task", map[string]any{"instructions": "add hello.txt", "model": "fast"})
			if msg != "" {
				t.Fatal(msg)
			}
			id := started["task_id"].(string)
			if started["branch"] != "dotshift/"+id || started["model"] != "fast" {
				t.Fatalf("start_task = %v", started)
			}
			status := waitFinished(t, cs, id)
			if status["status"] != "done" {
				t.Fatalf("task_status = %v", status)
			}

			result, _ := call(t, cs, "task_result", map[string]any{"task_id": id, "include_diff": true})
			if result["commits"] != 1.0 || result["tests"] != "passed" || !strings.Contains(result["diff"].(string), "+line") ||
				!strings.Contains(result["notes"].(string), "Should I also update the README?") || !strings.Contains(result["diff_stat"].(string), "hello.txt") {
				t.Fatalf("task_result = %v", result)
			}

			follow, msg := call(t, cs, "send_input", map[string]any{"task_id": id, "input": "Yes, update the README too."})
			if msg != "" {
				t.Fatal(msg)
			}
			next := follow["task_id"].(string)
			if follow["follows_up"] != id || follow["branch"] != "dotshift/"+id || follow["model"] != "fast" {
				t.Fatalf("send_input = %v", follow)
			}
			waitFinished(t, cs, next)
			result, _ = call(t, cs, "task_result", map[string]any{"task_id": next})
			if result["status"] != "done" || result["commits"] != 2.0 {
				t.Fatalf("follow-up result = %v", result)
			}

			if _, msg := call(t, cs, "cancel_task", map[string]any{"task_id": next}); !strings.Contains(msg, "already done") {
				t.Fatalf("cancel_task on a finished task: %q", msg)
			}
		})
	}
}

func TestCancelOverMCP(t *testing.T) {
	runner := newRunner(t, `cat > /dev/null; echo working; sleep 30`)
	srv, _ := newTestServer(t, runner, "")
	cs := connect(t, srv, "none")
	started, msg := call(t, cs, "start_task", map[string]any{"repo": "demo", "instructions": "take forever"})
	if msg != "" {
		t.Fatal(msg)
	}
	id := started["task_id"].(string)
	for {
		st, _ := call(t, cs, "task_status", map[string]any{"task_id": id})
		if st["status"] == "running" && strings.Contains(fmt.Sprint(st["recent_output"]), "working") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, msg := call(t, cs, "cancel_task", map[string]any{"task_id": id}); msg != "" {
		t.Fatal(msg)
	}
	if st := waitFinished(t, cs, id); st["status"] != "cancelled" {
		t.Fatalf("after cancel_task = %v", st)
	}
}

func TestMCPNeedsToken(t *testing.T) {
	srv, _ := newTestServer(t, newRunner(t, editScript), "")
	for _, token := range []string{"", "made-up"} {
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := `Bearer resource_metadata="` + srv.URL + `/.well-known/oauth-protected-resource/mcp"`
		if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") != want {
			t.Errorf("token %q: %d %q", token, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}
}

// register makes a public client and returns its ID.
func register(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, err := http.Post(srv.URL+"/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"client_name":"Test client","token_endpoint_auth_method":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 201 || out["client_secret"] != nil {
		t.Fatalf("register: %d %v", resp.StatusCode, out)
	}
	return out["client_id"].(string)
}

func authorizeURL(srv *httptest.Server, clientID, verifier string) string {
	return srv.URL + "/oauth/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "state": {"s1"},
		"redirect_uri":   {"https://chatgpt.com/connector_platform_oauth_redirect"},
		"code_challenge": {challengeFor(verifier)}, "code_challenge_method": {"S256"},
	}.Encode()
}

func challengeFor(v string) string { return pkceChallenge(v) }

func token(t *testing.T, srv *httptest.Server, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(srv.URL+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestCodeAndRefreshRules(t *testing.T) {
	srv, _ := newTestServer(t, newRunner(t, editScript), "")
	id := register(t, srv)
	verifier := strings.Repeat("v", 50)
	loc := approve(t, authorizeURL(srv, id, verifier), testPasscode)
	if loc.Host != "chatgpt.com" || loc.Query().Get("state") != "s1" || loc.Query().Get("iss") != srv.URL {
		t.Fatalf("redirect = %s", loc)
	}
	code := loc.Query().Get("code")

	base := url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {code},
		"redirect_uri": {"https://chatgpt.com/connector_platform_oauth_redirect"}}
	wrong := url.Values{}
	for k, v := range base {
		wrong[k] = v
	}
	wrong.Set("code_verifier", strings.Repeat("x", 50))
	if status, out := token(t, srv, wrong); status != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %v", status, out)
	}
	// A failed exchange still uses the code up.
	base.Set("code_verifier", verifier)
	if status, _ := token(t, srv, base); status != 400 {
		t.Fatalf("reused code: %d", status)
	}

	loc = approve(t, authorizeURL(srv, id, verifier), testPasscode)
	base.Set("code", loc.Query().Get("code"))
	status, out := token(t, srv, base)
	if status != 200 || out["token_type"] != "Bearer" || out["refresh_token"] == nil {
		t.Fatalf("exchange: %d %v", status, out)
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {out["refresh_token"].(string)}}
	if status, out := token(t, srv, refresh); status != 200 || out["access_token"] == nil {
		t.Fatalf("refresh: %d %v", status, out)
	}
	if status, out := token(t, srv, refresh); status != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("refresh token reused: %d %v", status, out)
	}
}

func TestApprovalPage(t *testing.T) {
	srv, store := newTestServer(t, newRunner(t, editScript), "")
	id := register(t, srv)
	authURL := authorizeURL(srv, id, strings.Repeat("v", 50))

	// A redirect URI the client didn't register is never redirected to.
	evil := strings.Replace(authURL, "chatgpt.com", "evil.example", 1)
	if resp, _ := noRedirects.Get(evil); resp.StatusCode != 400 {
		t.Errorf("unregistered redirect_uri: %d", resp.StatusCode)
	}

	u, _ := url.Parse(authURL)
	form := u.Query()
	form.Set("decision", "deny")
	resp, _ := noRedirects.PostForm(srv.URL+"/oauth/authorize", form)
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc == nil || loc.Query().Get("error") != "access_denied" {
		t.Errorf("deny: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	form.Set("decision", "allow")
	form.Set("passcode", "wrong")
	for i := 0; i < maxFailures; i++ {
		resp, _ := noRedirects.PostForm(srv.URL+"/oauth/authorize", form)
		if resp.StatusCode != 401 {
			t.Fatalf("wrong passcode %d: %d", i, resp.StatusCode)
		}
	}
	form.Set("passcode", testPasscode)
	if resp, _ := noRedirects.PostForm(srv.URL+"/oauth/authorize", form); resp.StatusCode != 429 {
		t.Errorf("after %d wrong passcodes: %d", maxFailures, resp.StatusCode)
	}

	if n, err := store.RevokeAll(context.Background()); err != nil || n != 1 {
		t.Fatalf("RevokeAll = %d, %v", n, err)
	}
	if resp, _ := noRedirects.Get(authURL); resp.StatusCode != 400 {
		t.Errorf("revoked client: %d", resp.StatusCode)
	}
}

func metadata(t *testing.T, srv *httptest.Server, path, host string, header http.Header) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	req.Host = host
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHostsThroughATunnel(t *testing.T) {
	runner := newRunner(t, editScript)

	// No public URL: the tunnel's host and scheme are used as forwarded.
	srv, _ := newTestServer(t, runner, "")
	status, out := metadata(t, srv, "/.well-known/oauth-authorization-server", "abc.trycloudflare.com", http.Header{"X-Forwarded-Proto": {"https"}})
	if status != 200 || out["issuer"] != "https://abc.trycloudflare.com" || out["token_endpoint"] != "https://abc.trycloudflare.com/oauth/token" {
		t.Fatalf("metadata via tunnel: %d %v", status, out)
	}
	status, out = metadata(t, srv, "/.well-known/oauth-protected-resource/mcp", "abc.trycloudflare.com", http.Header{"X-Forwarded-Proto": {"https"}})
	if status != 200 || out["resource"] != "https://abc.trycloudflare.com/mcp" {
		t.Fatalf("resource metadata via tunnel: %d %v", status, out)
	}
	// The MCP endpoint answers a tunnel's Host header with 401, not the
	// SDK's DNS-rebinding 403.
	req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader("{}"))
	req.Host = "abc.trycloudflare.com"
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Errorf("/mcp via tunnel host: %d", resp.StatusCode)
	}

	// With a public URL, only it and this machine are served.
	srv, _ = newTestServer(t, runner, "https://dotshift.example.com/")
	if status, out := metadata(t, srv, "/.well-known/oauth-authorization-server", "dotshift.example.com", nil); status != 200 || out["issuer"] != "https://dotshift.example.com" {
		t.Fatalf("public host: %d %v", status, out)
	}
	if status, _ := metadata(t, srv, "/.well-known/oauth-authorization-server", "evil.example", nil); status != http.StatusMisdirectedRequest {
		t.Fatalf("other host: %d", status)
	}
	if status, _ := metadata(t, srv, "/healthz", "localhost:8787", nil); status != 200 {
		t.Fatalf("localhost: %d", status)
	}
}

func TestPasscode(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	p, err := Passcode(cfg, dir)
	if err != nil || len(p) != 23 || strings.Count(p, "-") != 3 {
		t.Fatalf("Passcode = %q, %v", p, err)
	}
	if again, _ := Passcode(cfg, dir); again != p {
		t.Errorf("passcode changed: %q then %q", p, again)
	}
	if info, _ := os.Stat(filepath.Join(dir, PasscodeFile)); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode())
	}
	cfg.MCP.PasscodeEnv = "DOTSHIFT_TEST_PASSCODE"
	t.Setenv("DOTSHIFT_TEST_PASSCODE", "short")
	if _, err := Passcode(cfg, dir); err == nil {
		t.Error("short passcode accepted")
	}
}
