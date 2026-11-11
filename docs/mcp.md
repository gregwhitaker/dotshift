# Dot connection and MCP tools

`dotshift serve` also runs an MCP server on `127.0.0.1:8787` with these tools:

| Tool | What it does |
| --- | --- |
| `list_repos` | Lists allowed repos, model aliases, and the project root and test profiles |
| `create_project` | Creates and registers a new local Git project ready for `start_goal` |
| `register_project` | Registers an existing committed Git repository inside the project root |
| `list_strategies` | Lists execution mode, spending-cap support, model policies, eligible aliases, and observed availability |
| `start_task` | Starts coding with configured model selection, fallback, tests, and separate review |
| `start_goal` | Starts a persistent project goal with autonomous coding, testing, fixes, and completion review |
| `list_tasks` | Recovers recent task IDs and status after reconnecting; supports repo and unfinished-work filters |
| `task_status` | Status, the end of the agent's output while it runs, its notes when done |
| `task_result` | Notes, branch, commits, diffstat, test result, and the diff if asked |
| `send_input` | Follows up on completed work on the same branch, preserving context, model policy, fallback, tests, and separate review |
| `resume_goal` | Continues a blocked or cancelled goal, with optional input, model or strategy change, retry after allowance resets/access is restored, or iteration limit |
| `cancel_task` | Stops a running task |

The tools go through the same job runner as `dotshift run`, so the same
allowlist applies: a task can only run in a repo under `repos:` or a registered
project inside `projects.root`, and only on a new branch in its own worktree.

For `start_task`, `start_goal`, and `send_input`, provide a unique `request_id`
for each requested change. If the connection drops, retry with the same ID
and unchanged arguments: Dotshift returns the original task, including after
a service restart. Reusing the ID for different work is rejected. Older
clients may omit the ID; use `list_tasks` to recover their submitted work
before retrying, since submissions without an ID cannot be deduplicated.

This setup gives ChatGPT a public HTTPS endpoint through
[Tailscale Funnel](https://tailscale.com/kb/1223/funnel). `dotshift serve`
runs `tailscale funnel 8787`, which serves the endpoint at
your machine's `https://<machine>.<tailnet>.ts.net` name, and stops it on
exit. Only the MCP endpoint is published; the router stays on this machine.
Dotshift supervises the tunnel and retries startup failures or unexpected
exits with a delay that grows from one to thirty seconds. When Tailscale
comes back online, the tunnel reconnects without restarting Dotshift.
Shutdown stops the CLI's whole process group, including the macOS shell
wrapper's child, so a restart does not leave a conflicting listener behind.
On macOS, the runner records its tunnel process and kernel start time in
`.dotshift/funnel.json`. After a service crash it can reap that exact orphaned
process group before reconnecting, without resetting other Tailscale mappings.
Run `dotshift connect` for the steps; in short:

1. Once per machine: install Tailscale, log in (`tailscale up`), and run
   `tailscale funnel 8787` once by hand. If Funnel isn't on for your tailnet
   yet, it prints a link to turn it on; then press Ctrl-C.
2. In your browser, open [ChatGPT Plugins](https://chatgpt.com/plugins).
   Use **+ → Add custom MCP server**. Name it `dotshift`, select the
   **Server URL** connection, and enter
   `https://<machine>.<tailnet>.ts.net/mcp` (`dotshift connect` prints yours).
   Choose **OAuth**. If prompted for client registration, use dynamic
   registration (DCR); dotshift supplies the registration endpoint, so you
   do not need static client credentials. Confirm the warning and create the plugin.
3. Install it from your personal plugins. When dotshift's authorization page
   opens, enter the passcode from `dotshift connect`.
4. Open a Work chat and select `dotshift` with `@`. Ask it to list repositories
   and model strategies to check the connection.

These steps follow the [current custom MCP server guide](https://developers.openai.com/api/docs/guides/custom-mcp-server)
and [plugin quickstart](https://developers.openai.com/plugins/quickstart).

If Tailscale isn't installed, logged in or allowed to use Funnel, `dotshift
serve` says why and keeps running with the endpoint reachable only from this
machine while retrying. Setup and recovery messages appear in the service
log. The computer must remain awake and connected for Dot to reach it.

dotshift is its own OAuth server with one user, you: ChatGPT registers itself,
and nothing can call the tools until you approve it with the passcode. Access
tokens last an hour and refresh for 30 days; they're stored hashed in
`.dotshift/auth.db`. `dotshift connect -revoke` disconnects every client, and
`dotshift connect -new-passcode` replaces the passcode. After 10 wrong
passcodes in 10 minutes the approval page stops accepting tries for a while.
Requests for any host but the Funnel name (or this machine) are refused.

```yaml
mcp:
  listen: 127.0.0.1:8787
  tunnel: tailscale                           # or none, to run your own tunnel
  # public_url: https://dotshift.example.com  # the URL clients use, if not Funnel's
  # passcode_env: DOTSHIFT_MCP_PASSCODE       # instead of the generated passcode
  # disabled: true
```

With `tunnel: none`, point your own tunnel at `http://127.0.0.1:8787`. Set
`public_url` to its URL if it has a fixed name; without it, dotshift takes its
URL from each request as the tunnel forwards it.

**Follow-ups.** Use `send_input` for new instructions after completed work.
It starts a new task on the same branch with earlier instructions and notes,
the original model policy and reviewer, and the same tests and review loop.
Iteration limits carry over as the allowance for the new task. An API spending
cap carries forward only its remaining amount. `dotshift jobs` shows which
task a follow-up came from. Use `resume_goal` to answer a blocker or continue
cancelled work in its existing worktree.

Stopping the service queues active tasks and goals for recovery on the next
start. Explicitly cancelled tasks remain cancelled.
