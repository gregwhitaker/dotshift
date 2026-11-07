<p>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/svg/dotshift-logo-dark.svg">
    <img alt="dotshift" src="brand/svg/dotshift-logo-light.svg" height="56">
  </picture>
</p>

# dotshift

dotshift is a local coding service your OpenAI dot (or any MCP client) calls
to hand off work. Give it a project goal, a model alias, and an optional model
strategy, and it keeps
coding, testing, and fixing issues on its own branch until tests and a
completion review pass. Progress is saved between attempts and resumes when
`dotshift serve` restarts.

One config file lists your execution mode, allowed repositories, test commands,
and model aliases. **Subscription mode** runs the official Codex and Claude Code
CLIs with your own saved subscription sign-ins. **API mode** runs Codex CLI through
a [LiteLLM](https://github.com/BerriAI/litellm) proxy in Docker, which routes requests
to OpenAI, Anthropic, Gemini, or a local Ollama model.
Dot reaches the MCP tools through Tailscale Funnel. You can also run individual
jobs with `dotshift run`, or point another coding agent at the router.

## Use your existing subscriptions

Build this checkout with Go 1.25+, install current official
[Codex CLI](https://learn.chatgpt.com/docs/cli) and
[Claude Code](https://code.claude.com/docs/en/setup), and sign in through their
own login flows using your ChatGPT and Claude subscription accounts:

```sh
go build -o dotshift ./cmd/dotshift
codex login
claude auth login
./dotshift init -mode subscription
```

Add your repositories and test commands to `dotshift.yaml`, or enable
`projects.root` as described below so Dot can create projects for you. Then:

```sh
./dotshift check       # local CLI/auth checks; no model calls
./dotshift serve       # job runner and MCP intake; no Docker or LiteLLM
```

The generated configuration uses:

```yaml
aliases:
  smart: [openai/default, anthropic/default]
  codex: [openai/default]
  claude: [anthropic/default]
repos:
  myproject:
    path: ~/src/myproject
    test: go test ./...
jobs:
  mode: subscription
  model: smart
  strategy: prefer_openai
  reviewer_model: auto
  timeout: 30m
```

`openai/default` uses Codex's default subscription model; `anthropic/default`
uses Claude Code's. You can pin model names supported by your plan, or Claude
Code aliases such as `sonnet` and `opus`. Set `jobs.agent.command` and
`jobs.claude_command` to absolute executable paths if they aren't on PATH.
The CLIs must support the flags checked by `dotshift check`; update them when
the check requests it. Claude runs with restricted file tools and a required
Bash sandbox; the independent reviewer has only Read, Glob, and Grep tools.
Claude customizations and Codex user configuration are disabled for these runs;
organization-managed policy still applies.

For project goals, `prefer_openai` tries Codex first, then Claude Code after an
observed subscription limit. Worktree changes and test output survive the
switch. Known reset times are saved and waited for when all candidates are
limited. If the CLI doesn't report a reset, the goal blocks; resume it with
`retry_unavailable: true` after allowance is available again. Sign-in and
installation errors block immediately with setup instructions. Single tasks
use only the first model in their alias; use a goal for managed fallback.

Adaptive escalation and independent reviewers work in both modes. List explicit
models in order from baseline to strongest for `adaptive`; default models do
not imply a strength ranking. With `reviewer_model: auto`, completion can wait
for the other provider's allowance even when implementation has finished.
Dotshift does not silently let the implementer replace an unavailable independent
reviewer. If a CLI default is used, a different provider is required to establish
review independence.

Subscription mode never falls back to the API router. It removes ambient API
credentials/provider overrides from CLI child environments and checks native
subscription sign-in before each attempt. It never reads or copies the CLIs'
OAuth credentials. **Provider-side extra usage or purchased-credit settings
still apply**; disable paid extra usage in your provider account if you want
included allowance only. Dotshift cannot meter remaining plan allowance or
enforce a dollar cap on it. `max_cost_usd` is rejected here; use goal
`max_iterations` (for example 20) and `jobs.timeout` instead. See
[Codex noninteractive authentication](https://learn.chatgpt.com/docs/non-interactive-mode)
and [Claude Code subscription sign-in](https://support.claude.com/en/articles/11145838-use-claude-code-with-your-pro-max-or-ultra-plan).

Use Tailscale Funnel when Dot needs to reach this computer remotely; it is not
needed for local CLI jobs. Follow [Connecting your dot](#connecting-your-dot-mcp).
An existing goal retains the execution mode it started with; to change modes,
start a new goal from that goal's branch. Old configs without `jobs.mode` keep
API behavior. When converting a config, remove provider API-key/base-URL
settings, non-OpenAI/Anthropic models, custom agent args, and dollar caps.

## API mode requirements

- Go 1.25+ to build
- Docker, which runs the LiteLLM proxy
- Codex CLI for coding tasks and project goals
- API keys for the providers you want, as environment variables

## API mode quick start

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
| `dotshift init [-mode api\|subscription]` | Writes a starter `dotshift.yaml`; defaults to API mode |
| `dotshift serve` | Starts the job runner and MCP intake; API mode also starts the router container |
| `dotshift status` | Shows the configured subscription mode, or API router status |
| `dotshift stop` | Removes the API router container; stop subscription serving with Ctrl-C |
| `dotshift check [ALIAS...]` | Subscription: checks installed CLIs and sign-in without model calls. API: tests chat, Responses streaming, tool continuation, and goal JSON for every model |
| `dotshift usage [PERIOD]` | API requests, tokens, and cost per model; subscription allowance is checked in the providers' usage screens |
| `dotshift key` | Prints the API key clients send to the router |
| `dotshift litellm-config` | Prints the LiteLLM config generated from `dotshift.yaml` |
| `dotshift connect` | Shows how to connect ChatGPT and the passcode that approves it; `-revoke` disconnects every client |
| `dotshift run "..."` | Runs a coding job in an allowlisted repo and waits for it (see [Jobs](#jobs)) |
| `dotshift jobs [ID]` | Lists jobs, or shows one job's result |
| `dotshift logs ID` | Prints a job's agent output |
| `dotshift cancel ID` | Stops a running job |
| `dotshift clean ID` | Removes the worktree a failed or cancelled job left behind |

All commands take `-config FILE` (default `dotshift.yaml`).

## API configuration

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

## Checking models

In API mode, `dotshift check` tests each model on its own with fallbacks turned off. It
checks chat and function calling through `/chat/completions`, then exercises
the `/responses` protocol used by the coding runner.

- `STREAM` requires a successful streaming response with text output.
- `TOOL LOOP` requests a function call, supplies its result with the matching
  call ID, and verifies that the model can use that result in a second request.
- `GOAL JSON` checks the same strict output schema used by project goals.

The Responses checks reject failed, incomplete, or truncated streams and
preserve reasoning items when continuing after a tool call. They use the
documented Responses streaming and function-calling formats.
[Streaming reference](https://developers.openai.com/api/docs/guides/streaming-responses)
[Function-calling reference](https://developers.openai.com/api/docs/guides/function-calling)

Each model uses up to six requests, which count toward usage. The command
exits non-zero if any check fails. Passing these checks establishes protocol
compatibility; completing a live coding goal also verifies the CLI, sandbox,
repository environment, and MCP connection together.

```
$ dotshift check
ALIAS  MODEL                        ROLE        CHAT     TOOLS    STREAM   TOOL LOOP  GOAL JSON
fast   anthropic/claude-haiku-4-5   primary     ok 0.8s  ok 1.1s  ok 0.9s  ok 2.0s    ok 1.0s
smart  anthropic/claude-sonnet-5-5  primary     ok 1.4s  ok 1.9s  ok 1.5s  ok 3.0s    ok 1.7s
smart  openai/gpt-5                 fallback 1  FAIL     FAIL     FAIL     FAIL       FAIL

Failures:
  openai/gpt-5 chat: 401 Unauthorized: AuthenticationError: Incorrect API key provided
  openai/gpt-5 tools: 401 Unauthorized: AuthenticationError: Incorrect API key provided
  openai/gpt-5 Responses stream: 401 Unauthorized: AuthenticationError: Incorrect API key provided
  openai/gpt-5 Responses tool loop: 401 Unauthorized: AuthenticationError: Incorrect API key provided
  openai/gpt-5 goal JSON: 401 Unauthorized: AuthenticationError: Incorrect API key provided
```

## Usage and cost

In API mode, while `dotshift serve` runs, the router reports every call (alias, model,
tokens, cost, and the error if it failed) to dotshift, which appends it to
`.dotshift/usage.jsonl`. Prompts and responses are not recorded. Costs are
LiteLLM's list-price estimates, not your provider's bill.

```
$ dotshift usage 7d
ALIAS  MODEL                        REQUESTS  FAILED  INPUT TOKENS  OUTPUT TOKENS  COST (USD)
smart  anthropic/claude-sonnet-5-5  212       3       1843302       61022          6.45
smart  openai/gpt-5                 3         0       30114         1290           0.05
total                               215       3       1873416       62312          6.50
```

This uses LiteLLM's `generic_api` logging callback, so it needs no database.
dotshift listens where the container can reach it: on loopback with Docker
Desktop, and on the Docker bridge address on Linux. If that doesn't work on
your machine, `serve` warns and carries on without usage; set
`router.usage_listen` to an address that works, or to `off`.

## Using the API router from Codex CLI

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
2. Subscription mode runs Codex or Claude Code there with native sign-in.
   API mode runs Codex CLI (`codex exec`) through the router. Settings are
   passed on the command line; your saved CLI configuration is not changed.
3. When the agent finishes, dotshift runs the repo's test command if it has
   one, commits what the agent left, records the result, and removes the
   worktree. The branch stays. A job that changed nothing leaves no branch.

Jobs can run in repositories you configure below, or in projects registered
inside `projects.root` (see [Starting new projects](#starting-new-projects-with-dot)):

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

In API mode, first run `dotshift serve` in another terminal. Subscription
mode can run local jobs directly:

```sh
dotshift run -repo myapp "Add a --verbose flag to the CLI"
dotshift run -repo myapp -model fast -branch fix/typo "Fix the typo in README"
echo "long instructions..." | dotshift run -repo myapp -
```

`dotshift run` streams the agent's output and prints the result: the agent's
notes, a diffstat, the test result and how to review the branch. Ctrl-C
cancels the job. `-repo` can be left out when only one repo is allowlisted.

`dotshift jobs` lists every job with its status (`queued`, `running`, `done`,
`failed`, `blocked`, or `cancelled`), and `dotshift jobs ID` shows one in full. A failed or
cancelled job keeps its worktree so you can look at what the agent did;
`dotshift clean ID` removes it. Job state lives in `.dotshift/jobs.db`
(SQLite) and logs in `.dotshift/jobs/`.

Codex runs with `--sandbox workspace-write`, so it can only write inside the
worktree and has no network access for its commands. That's also why dotshift,
not the agent, makes the commit: a worktree's git metadata lives in the main
repo, outside the sandbox. In subscription mode, Claude Code uses its own
required Bash sandbox and restricted file tools.

## Starting new projects with Dot

Enable a workspace folder and named test commands in `dotshift.yaml` once:

```yaml
projects:
  root: ~/workspace
  test_profiles:
    go: go test ./...
    node: npm test
    python: python3 -m unittest discover -s tests -v
```

Restart `dotshift serve` after changing configuration. Dot can now take an
instruction such as **“Create a Node project named habit-tracker, build a local
habit tracker with weekly summaries, and keep fixing it until its tests pass.”**

Dot calls `list_repos` to discover the root and test profiles, then:

1. `create_project` with `name: "habit-tracker"`, `test_profile: "node"`, and an
   optional short description. This creates `~/workspace/habit-tracker` with a
   README and initial commit on `main`, and registers it immediately.
2. `start_goal` with `repo: "habit-tracker"`, the full objective, and concrete
   acceptance criteria. The agent scaffolds the application and meaningful
   tests, then uses the normal coding, testing, fixing, and review loop.
3. `task_status` and `task_result` to follow progress and report the result.

For a project you have already started, use `register_project` with its folder
name and a test profile instead. It must be a Git repository directly inside
`projects.root`, with at least one commit. Registration preserves your files;
jobs start from committed code, so commit any work you want the agent to use.
Repositories elsewhere can still be configured explicitly under `repos:`.

Project names are simple lowercase folder names (letters, digits, `-`, `_`, up
to 64 characters). Creation refuses existing paths. Registration refuses
symlinks, linked worktrees, and repositories outside the configured root. Dot
selects test commands from your local profiles; the MCP tools cannot supply an
arbitrary shell command. The agent must build a real test suite for the chosen
command. Add other profiles locally for different build tools or test runners.

The project catalog is saved in `.dotshift/jobs.db`; new entries are usable
immediately and survive restarts. Removing/changing the root or removing a
profile disables the affected managed entries. Explicit `repos:` entries take
precedence. Project creation itself makes no model calls. Goal results remain
on their task branch, which `task_result` identifies; switch to that branch in
the project folder to run or review the completed application.

## Project goals

Dot can hand off a whole project objective with the `start_goal` MCP tool.
The repository must have a test command under `repos.NAME.test`, or a test
profile selected through `create_project` or `register_project`.

```json
{
  "repo": "myapp",
  "goal": "Add a --verbose flag, cover normal and error output in tests, and document its usage.",
  "model": "smart"
}
```

The tool returns a `task_id` immediately. The goal keeps working while dot is
disconnected. `task_status` reports its phase, completed iterations, latest
assessment, and test results. `task_result` returns the accumulated branch,
diff, and outcome. `dotshift jobs` and `dotshift logs` also show goal progress.

Each iteration gives the coding agent the original goal, its cumulative
progress notes, remaining work, and the latest test failures. dotshift runs
the configured tests and commits the iteration's changes. Failed tests feed
the next attempt. Once the agent reports completion and tests pass, a fresh
agent invocation reviews the goal and code with a read-only sandbox. Review
findings feed another coding iteration. The goal reaches `done` only when
that review confirms completion against the tested code.

Goals have no iteration limit by default. Set `max_iterations` in `start_goal`
to bound the number of coding and testing cycles. Each agent invocation and
test command uses `jobs.timeout`. A goal becomes `blocked` when it reaches
the iteration limit, the agent identifies an external dependency requiring
input, or three consecutive agent execution or response-format attempts fail.
With the adaptive strategy, repeated coding failures instead advance through
the model ladder and block if its strongest model also stalls. A spending cap
can also block further model calls. Ordinary test failures feed the coding loop.

`resume_goal` continues a blocked or cancelled goal on its existing branch.
It accepts the same `task_id`, optional `input`, an optional replacement
`model`, and an optional new total `max_iterations`. Set that limit to `0`
to remove it. Input supplements the original objective. `cancel_task` stops
the current agent or test process and preserves the worktree.

Goal state is stored in `.dotshift/jobs.db`. Stopping the service saves active
goals as queued. Starting `dotshift serve` with MCP enabled resumes them from
their saved phase and worktree, including partial edits. Recovery terminates
any recorded agent or test process left by an interrupted supervisor and
rechecks the repository allowlist. Blocked and cancelled goals wait for an
explicit `resume_goal` call. Keep their worktrees to continue them.

## Model strategies

Dot calls `list_strategies` to discover the execution mode, supported controls,
eligible model aliases, and observed availability. A goal can choose its policy independently of other
goals:

```json
{
  "repo": "myapp",
  "goal": "Implement the feature, cover its acceptance criteria with tests, and fix failures.",
  "model": "smart",
  "strategy": "prefer_openai"
}
```

| Strategy | Behavior |
| --- | --- |
| `alias` | Uses configured alias order. Subscription goals use explicit CLI selection. API mode uses router fallbacks unless a reviewer or cap enables explicit deployment selection. |
| `prefer_openai` | Uses the alias's OpenAI models first, then its Anthropic models. Remembers observed allowance limits, exhausted API credits, and cooldowns across goals and restarts. |
| `adaptive` | Uses an alias ordered from baseline to strongest. Escalates after repeated failed tests, unchanged coding cycles, agent execution failures, or rejected completion reviews. |

`prefer_openai` requires both providers in the selected alias. It preserves the
configured order within each provider, ignores other providers in that alias,
and never selects models from a different alias. For example, the `smart` alias
above supports it even though Anthropic is listed first. Use model IDs your
accounts support. `dotshift check smart` verifies CLI sign-in in subscription
mode, or coding protocol compatibility in API mode.
To make this the default for new goals, set:

```yaml
jobs:
  model: smart
  strategy: prefer_openai
```

In API mode, each managed agent attempt uses an authenticated local gateway
that pins its model and disables LiteLLM retries and fallbacks. An actual API
limit or quota error stops that attempt. The next attempt gets the same branch,
partial edits, original objective, progress notes, and test results in a fresh
agent context. Provider-specific conversation state is not moved between models.

- Credit or spend-limit exhaustion skips every model using that provider
  configuration. An explicit access-restoration date is honored. Without one,
  the provider stays unavailable until an explicit retry after access is restored.
- Rate limits honor `Retry-After` or exhausted-bucket reset headers. Without
  either, the cooldown is one minute. Throttles of up to five seconds wait for
  the preferred model; longer waits use an available fallback.
- Provider server errors use a 30-second cooldown unless the response specifies
  a retry time. If every candidate is temporarily unavailable, the goal waits
  until the earliest eligible retry. These waits survive service restarts and
  remain cancellable.
- With `prefer_openai`, OpenAI becomes preferred again at the next coding attempt
  after recovery. Adaptive goals retain the tier they advanced to. Without a
  separate reviewer, a working model stays selected through completion review.
- Context length, authentication, malformed requests, and local router connection
  failures are not classified as depleted credits. They retain the existing
  bounded execution retry behavior, including adaptive escalation after repeated
  coding execution failures.

`task_status` and `task_result` expose `goal.model_selection`, including the active
model, selection reason, switch count, last limit failure, and any wait deadline.
The same information appears in `dotshift jobs ID` and the job log. Availability
is inferred from observed failures, not a query of remaining account balances or
a guarantee that an untested model works. Quota state is shared by provider
configuration; temporary rate/server cooldowns are shared by model across aliases.

If all candidates need credits or access restored, the goal becomes `blocked`.
After restoring access, continue the same goal with:

```json
{"task_id": "YOUR_TASK_ID", "retry_unavailable": true}
```

Pass that object to `resume_goal`. It clears cached unavailability for the goal's
candidate models and their provider configurations, including state shared with
other goals. It does not change provider limits or purchase credits. Ordinary
resume preserves cached failures. `resume_goal` also accepts a new `strategy`;
use `"alias"` to return to configured alias order. Strategy controls
currently apply to project goals, not one-shot tasks or external router clients.

### Adaptive escalation and independent reviewers

Configure the coding alias in baseline-to-strongest order, with at least two
distinct models. Dot can select all three controls in one `start_goal` call:

```json
{
  "repo": "myapp",
  "goal": "Implement the feature, test every acceptance criterion, and fix review findings.",
  "model": "smart",
  "strategy": "adaptive",
  "escalate_after": 2,
  "reviewer_model": "auto",
  "max_cost_usd": 10
}
```

`escalate_after` defaults to 2. Each failed test cycle counts as a stall; a
successful test cycle with `continue` and no file changes also counts. Repeated
coding execution/report failures and completion review rejections have their
own counters. Meaningful progress clears the test/no-change counter; escalation
clears the tier's counters. The goal records its tier, reason, and escalation
count in `goal.adaptive`. A fresh coding invocation receives the same objective,
files, test output, and review findings. A stronger tier is retained across
restarts and ordinary resumes, including advancement caused by unavailable
lower tiers. If the strongest configured model also stalls, the goal blocks
with its work preserved. Resume can change the threshold or supply instructions;
choosing a new coding alias or strategy resets its ladder state.

`reviewer_model: "auto"` picks from the coding alias, preferring a different
provider and always excluding the actual implementation model. Or provide a
dedicated reviewer alias. Its configured order controls reviewer preference.
The reviewer gets a fresh context and read-only checkout after tests pass;
its findings feed another coding cycle, and its approval is required to finish.
Availability fallback also excludes the implementer. If no distinct reviewer
is available, the goal waits or blocks instead of silently self-reviewing.
Reviewer pools are validated against every possible implementation model.
`goal.reviewer_selection` records the reviewer separately from coding selection.
With no separate reviewer configured, fresh review uses the coding model.

These options can also be defaults for new goals:

```yaml
jobs:
  model: smart
  strategy: adaptive
  escalate_after: 2
  reviewer_model: auto
  max_cost_usd: 10
```

### Project spending caps (API mode)

`max_cost_usd` limits the **reported model spending for one goal**, across all
coding, repair, escalation, fallback, and review requests. Before each request,
the gateway checks the saved total. Once it reaches the cap, further calls are
blocked. The request that crosses the cap can finish and may exceed it; if that
request finishes the final review, the goal can still complete. Tests and local
commands are not model charges. Different goals have separate totals, even in
the same repository. This is not an account-wide billing limit.

Caps default to disabled (`0`). A positive cap requires usage recording. Costs
come from LiteLLM's terminal response receipts or authenticated usage callbacks,
with a request ID injected by the gateway. Pending requests and totals are saved
in SQLite; duplicate receipts do not double-charge, and later higher reported
costs update the total. Streaming header costs are ignored because they may be
preliminary. Missing or null costs are not counted as zero: the gateway waits
up to 15 seconds for accounting, then blocks with a pending receipt rather than
sending more requests. Late callbacks can settle that receipt after restart.
An explicitly reported zero cost is accepted. Keep the router's model pricing
current; this cap follows its reported costs, not a provider invoice or a
guarantee of remaining account credits.

`goal.budget` shows the limit, spent and remaining amounts, request and pending
counts, and spending by model and coding/review role. `dotshift jobs ID` shows
the total too. Resuming, changing models, and retrying restored API access never
reset spending. With the user's authorization, `resume_goal` can set a new
**total** `max_cost_usd`, or `0` to remove the cap while retaining its ledger.
Dot must not raise or remove a cap on its own to get past a blocker. Goals that
started without a cap have no complete cost ledger; to add a cap, start a new
goal from the saved branch and explicitly begin a new spending total.

## Connecting your dot (MCP)

`dotshift serve` also runs an MCP server on `127.0.0.1:8787` with these tools:

| Tool | What it does |
| --- | --- |
| `list_repos` | Lists allowed repos, model aliases, and the project root and test profiles |
| `create_project` | Creates and registers a new local Git project ready for `start_goal` |
| `register_project` | Registers an existing committed Git repository inside the project root |
| `list_strategies` | Lists execution mode, spending-cap support, model policies, eligible aliases, and observed availability |
| `start_task` | Starts a job: repo, instructions, optional alias, branch and base |
| `start_goal` | Starts a persistent project goal with autonomous coding, testing, fixes, and completion review |
| `task_status` | Status, the end of the agent's output while it runs, its notes when done |
| `task_result` | Notes, branch, commits, diffstat, test result, and the diff if asked |
| `send_input` | Follows up on a finished task on the same branch (see below) |
| `resume_goal` | Continues a blocked or cancelled goal, with optional input, model or strategy change, retry after allowance resets/access is restored, or iteration limit |
| `cancel_task` | Stops a running task |

The tools go through the same job runner as `dotshift run`, so the same
allowlist applies: a task can only run in a repo under `repos:` or a registered
project inside `projects.root`, and only on a new branch in its own worktree.

This setup gives ChatGPT a public HTTPS endpoint through
[Tailscale Funnel](https://tailscale.com/kb/1223/funnel). `dotshift serve`
runs `tailscale funnel 8787`, which serves the endpoint at
your machine's `https://<machine>.<tailnet>.ts.net` name, and stops it on
exit. Only the MCP endpoint is published; the router stays on this machine.
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
machine.

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

**Follow-ups.** Codex runs non-interactively, so an agent can't stop to ask a
question mid-task; it asks in its notes when it finishes. `send_input`
answers it: it starts a new task on the same branch whose agent is told the
earlier instructions and notes, and adds its commits on top. It works on a
finished task whose worktree is gone (every `done` task, or a failed one after
`dotshift clean`). `dotshift jobs` shows which task a follow-up came from.

Stopping `dotshift serve` cancels ordinary tasks and queues active project
goals for recovery on the next start.

## Development

```sh
go test ./...
go build ./cmd/dotshift
```

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
