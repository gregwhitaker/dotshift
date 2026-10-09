# dotshift

dotshift is a local model router for coding agents. You give it one config file
listing your providers and a few aliases like `fast`, `smart` and `local`. It
runs a [LiteLLM](https://github.com/BerriAI/litellm) proxy in Docker that speaks
the OpenAI API, so any coding agent with a custom base URL can use OpenAI,
Anthropic, Gemini or a local Ollama model by asking for an alias.

dotshift also runs coding jobs: `dotshift run` hands instructions to Codex CLI,
which works in its own git worktree on a new branch and calls models through
the router. Your OpenAI dot (or any MCP client) can start and follow the same
jobs over MCP, through a tunnel to your machine.

## Requirements

- Go 1.25+ to build
- Docker, which runs the LiteLLM proxy
- API keys for the providers you want, as environment variables

## Quick start

```sh
go install github.com/gregwhitaker/dotshift/cmd/dotshift@latest

dotshift init                      # writes dotshift.yaml
export ANTHROPIC_API_KEY=...       # and/or OPENAI_API_KEY, GEMINI_API_KEY
dotshift serve                     # starts the router and MCP intake, Ctrl-C stops them
```

`dotshift serve` prints the base URL (`http://127.0.0.1:4000/v1` by default)
and follows the router's logs.

## Commands

| Command | What it does |
| --- | --- |
| `dotshift init` | Writes a starter `dotshift.yaml` |
| `dotshift serve` | Generates the LiteLLM config, starts the container, waits until it's healthy, starts the MCP intake (see [Connecting your dot](#connecting-your-dot-mcp)), then follows the router's logs |
| `dotshift status` | Shows whether the router is up and which models it serves |
| `dotshift stop` | Removes the router container |
| `dotshift key` | Prints the API key clients send to the router |
| `dotshift litellm-config` | Prints the LiteLLM config generated from `dotshift.yaml` |
| `dotshift connect` | Shows how to connect ChatGPT and the passcode that approves it; `-revoke` disconnects every client |
| `dotshift run "..."` | Runs a coding job in an allowlisted repo and waits for it (see [Jobs](#jobs)) |
| `dotshift jobs [ID]` | Lists jobs, or shows one job's result |
| `dotshift logs ID` | Prints a job's agent output |
| `dotshift cancel ID` | Stops a running job |
| `dotshift clean ID` | Removes the worktree a failed or cancelled job left behind |

All commands take `-config FILE` (default `dotshift.yaml`).

## Configuration

```yaml
router:
  host: 127.0.0.1
  port: 4000
  image: ghcr.io/berriai/litellm:main-stable

providers:
  anthropic:
    api_key_env: ANTHROPIC_API_KEY
  openai:
    api_key_env: OPENAI_API_KEY
  ollama_chat:
    base_url: http://host.docker.internal:11434

aliases:
  smart:
    - anthropic/claude-sonnet-5-5   # serves the request
    - openai/gpt-5                  # tried if the first one fails
  local:
    - ollama_chat/qwen2.5-coder
```

- **Providers** are `openai`, `anthropic`, `gemini`, `ollama` and
  `ollama_chat`. `base_url` points a provider at another endpoint, which also
  covers any OpenAI-compatible server (vLLM, LM Studio, OpenRouter) under
  `openai`.
- **Aliases** are what clients ask for. Models are written `provider/model`;
  the first one serves the request and the rest are fallbacks, tried in order.
- **Keys** are named, never stored. dotshift passes each variable to the
  container by name, so values stay out of config files and the `docker`
  command line.
- **Client auth.** LiteLLM requires a master key. dotshift generates one on
  first run and keeps it in `.dotshift/master_key` (mode 600); `dotshift key`
  prints it. Set `router.master_key_env` to use your own instead.

Generated files live in `.dotshift/` next to `dotshift.yaml`.

## Using it from Codex CLI

Add to `~/.codex/config.toml`:

```toml
[model_providers.dotshift]
name = "dotshift"
base_url = "http://127.0.0.1:4000/v1"
env_key = "DOTSHIFT_API_KEY"
wire_api = "responses"
```

Then:

```sh
export DOTSHIFT_API_KEY=$(dotshift key)
codex -c model_provider=dotshift -m smart
```

Any other agent that accepts an OpenAI base URL and API key works the same way.

## Jobs

A job takes instructions and a repo, and ends as a branch with the agent's
work committed on it. Your working copy is never touched.

1. dotshift adds a git worktree for the job on a new branch (`dotshift/<id>`
   unless you name one), starting from the repo's `HEAD`.
2. It runs Codex CLI there (`codex exec`), pointed at the router with the alias
   you asked for. The router settings are passed on the command line, so
   `~/.codex/config.toml` needs no changes.
3. When the agent finishes, dotshift runs the repo's test command if it has
   one, commits what the agent left, records the result, and removes the
   worktree. The branch stays. A job that changed nothing leaves no branch.

Jobs can only run in repos you allowlist:

```yaml
repos:
  myapp:
    path: ~/code/myapp
    test: go test ./...      # optional; its result is kept on the job

jobs:
  model: smart               # alias used when a job doesn't ask for one
  timeout: 1h
  agent:
    name: codex
    # command: /usr/local/bin/codex
    # args: ["--sandbox", "workspace-write"]
  # worktree_dir: ~/dotshift-worktrees   # default: .dotshift/worktrees
```

With `dotshift serve` running in another terminal:

```sh
dotshift run -repo myapp "Add a --verbose flag to the CLI"
dotshift run -repo myapp -model fast -branch fix/typo "Fix the typo in README"
echo "long instructions..." | dotshift run -repo myapp -
```

`dotshift run` streams the agent's output and prints the result: the agent's
notes, a diffstat, the test result and how to review the branch. Ctrl-C
cancels the job. `-repo` can be left out when only one repo is allowlisted.

`dotshift jobs` lists every job with its status (`queued`, `running`, `done`,
`failed` or `cancelled`), and `dotshift jobs ID` shows one in full. A failed or
cancelled job keeps its worktree so you can look at what the agent did;
`dotshift clean ID` removes it. Job state lives in `.dotshift/jobs.db`
(SQLite) and logs in `.dotshift/jobs/`.

Codex runs with `--sandbox workspace-write`, so it can only write inside the
worktree and has no network access for its commands. That's also why dotshift,
not the agent, makes the commit: a worktree's git metadata lives in the main
repo, outside the sandbox.

## Connecting your dot (MCP)

`dotshift serve` also runs an MCP server on `127.0.0.1:8787` with these tools:

| Tool | What it does |
| --- | --- |
| `list_repos` | Lists the allowlisted repos and the model aliases |
| `start_task` | Starts a job: repo, instructions, optional alias, branch and base |
| `task_status` | Status, the end of the agent's output while it runs, its notes when done |
| `task_result` | Notes, branch, commits, diffstat, test result, and the diff if asked |
| `send_input` | Follows up on a finished task on the same branch (see below) |
| `cancel_task` | Stops a running task |

The tools go through the same job runner as `dotshift run`, so the same
allowlist applies: a task can only run in a repo under `repos:`, and only on
a new branch in its own worktree.

ChatGPT only connects to MCP servers on the internet, so the endpoint needs a
tunnel. Run `dotshift connect` for the steps; in short:

1. Start a tunnel to `http://127.0.0.1:8787`, e.g.
   `cloudflared tunnel --url http://127.0.0.1:8787`. Only the MCP endpoint goes
   through it; the router stays on this machine.
2. In ChatGPT, turn on developer mode and create an app with the MCP server URL
   `https://<tunnel host>/mcp` and OAuth authentication.
3. ChatGPT opens dotshift's approval page. Type the passcode
   `dotshift connect` shows.

dotshift is its own OAuth server with one user, you: ChatGPT registers itself,
and nothing can call the tools until you approve it with the passcode. Access
tokens last an hour and refresh for 30 days; they're stored hashed in
`.dotshift/auth.db`. `dotshift connect -revoke` disconnects every client, and
`dotshift connect -new-passcode` replaces the passcode. After 10 wrong
passcodes in 10 minutes the approval page stops accepting tries for a while.

```yaml
mcp:
  listen: 127.0.0.1:8787
  # public_url: https://dotshift.example.com  # refuse requests for other hosts
  # passcode_env: DOTSHIFT_MCP_PASSCODE       # instead of the generated passcode
  # disabled: true
```

Without `public_url`, dotshift takes its URL from each request as the tunnel
forwards it, which suits quick tunnels whose URL changes on every start. Set
it when your tunnel has a fixed name.

**Follow-ups.** Codex runs non-interactively, so an agent can't stop to ask a
question mid-task; it asks in its notes when it finishes. `send_input`
answers it: it starts a new task on the same branch whose agent is told the
earlier instructions and notes, and adds its commits on top. It works on a
finished task whose worktree is gone (every `done` task, or a failed one after
`dotshift clean`). `dotshift jobs` shows which task a follow-up came from.

Stopping `dotshift serve` cancels the tasks it is running.

## Development

```sh
go test ./...
go build ./cmd/dotshift
```
