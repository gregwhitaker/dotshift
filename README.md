<p>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/svg/dotshift-logo-dark.svg">
    <img alt="dotshift" src="brand/svg/dotshift-logo-light.svg" height="56">
  </picture>
</p>

# dotshift

Local coding and review for your OpenAI Dot. Ask Dot to use dotshift on a
project; it works on a separate branch, runs tests, fixes failures, and gets a
fresh read-only review before marking the job complete.

**The default: OpenAI codes first. When it hits an observed usage limit, Claude
continues the same job and can finish a separate review without waiting for
OpenAI.** Files, progress, and test results survive the switch and service restarts.

Dot can choose a configured model alias, fallback policy, and reviewer for each
job. Once accepted, the job runs locally without further help from Dot. Dotshift
controls the coding agents; Dot's own conversation stays in Dot.

## Set up local coding

Install with Homebrew on macOS or Linux. The package includes the CLI and, on
macOS, the menu bar app. Until the first stable release, install the current
`main` branch with `--HEAD`:

```sh
brew tap gregwhitaker/dotshift https://github.com/gregwhitaker/dotshift.git
brew install --HEAD gregwhitaker/dotshift/dotshift
```

The [Homebrew guide](docs/homebrew.md) covers requirements, updates, and removal.
Install the official Codex and Claude Code CLIs, sign in using your subscription
accounts, and create a configuration outside Homebrew's Cellar:

```sh
codex login
claude auth login
mkdir -p ~/.config/dotshift
dotshift init -config ~/.config/dotshift/dotshift.yaml
```

`init` defaults to subscription mode. Add an allowed Git repository and its
validation command to `~/.config/dotshift/dotshift.yaml`. Keep the generated
model choices:

```yaml
aliases:
  smart: [openai/default, anthropic/default]
  codex: [openai/default]
  claude: [anthropic/default]
repos:
  myapp:
    path: ~/workspace/myapp
    test: npm test
jobs:
  mode: subscription
  model: smart
  strategy: prefer_openai
  reviewer_model: auto
  timeout: 30m
mcp:
  tunnel: tailscale
```

Use the validation command appropriate for the project. Each job needs one.
Jobs start from committed code and keep your working copy separate.
`openai/default` and `anthropic/default` use the CLIs' default models; aliases
can also contain explicit model names your accounts support. If needed, set
`jobs.agent.command` and `jobs.claude_command` to absolute CLI paths.

```sh
dotshift check -config ~/.config/dotshift/dotshift.yaml
# macOS: start the service and open the menu app
dotshift service -config ~/.config/dotshift/dotshift.yaml install
dotshift menubar -config ~/.config/dotshift/dotshift.yaml
dotshift connect -config ~/.config/dotshift/dotshift.yaml
```

`check` verifies CLI installation and sign-in without model calls. On macOS,
`service install` starts the service now, at login, and after a crash; `menubar`
opens the included status app. On Linux, start
`dotshift serve -config ~/.config/dotshift/dotshift.yaml` in the foreground or
under your own service manager.

Remote Dot access uses Tailscale Funnel; configure it using
[the connection guide](docs/mcp.md), then use `connect` for the connection URL
and authorization instructions. Local terminal jobs do not need a tunnel.
Keep the computer awake and connected while it works.

Then ask Dot:

> Use dotshift on myapp to add search, cover it with tests, and review it.
> Use OpenAI first, then Claude if OpenAI hits its limit.

## Keep the model choices

| Choice | How to ask Dot |
| --- | --- |
| OpenAI first, Claude fallback | Use `model: smart`, `strategy: prefer_openai` (the default) |
| A particular agent or model | Choose a configured alias such as `codex` or `claude` |
| Your own fallback order | Use `strategy: alias` with an ordered alias |
| Escalate after repeated failures | Use `strategy: adaptive` with an alias ordered from baseline to strongest |
| Separate review | Use `reviewer_model: auto`, or a configured reviewer alias |

Dot discovers these through `list_strategies`. Selection follows the chosen
policy and observed availability. Automatic review prefers another available
model; in subscription mode it can use the coding model in a fresh read-only
session. An explicitly named reviewer alias requires a different model.
See [model selection](docs/models.md) for configuration and recovery details.

Fallback reacts to limits reported by the providers; it cannot predict remaining
subscription tokens. If every eligible model is limited, the job waits for a
known reset or blocks with its work saved. Installation and sign-in failures
need to be fixed before resuming. Provider extra-usage settings still apply.
Subscription mode does not use the API router or enforce dollar caps.

## Let Dot create projects

To allow new projects inside a folder, add:

```yaml
projects:
  root: ~/workspace
  test_profiles:
    go: go test ./...
    node: npm test
    python: python3 -m unittest discover -s tests -v
```

Restart the service after configuration changes. Dot can then create a new
repository or register an existing committed repository directly inside that
folder, select a test profile, and start coding. Model choices apply to these
jobs too. See [local coding](docs/coding.md) for repository and worktree details.

## Check progress and recover work

```sh
dotshift ui -config ~/.config/dotshift/dotshift.yaml
dotshift jobs -config ~/.config/dotshift/dotshift.yaml
dotshift jobs -config ~/.config/dotshift/dotshift.yaml TASK_ID
dotshift logs -config ~/.config/dotshift/dotshift.yaml TASK_ID
```

The browser is a small monitor: work, model choices, logs, tests, changes, and
completion notes. It can cancel or resume unfinished work. Start new work with
Dot or `dotshift run`; project creation and job submission live in the coding
interface. Closing the browser or optional status menu leaves work running.

To clear an issue you have reviewed, open the job and choose **Dismiss from
attention**. It leaves the dashboard's Needs attention list and the menu bar's
attention count, while keeping its status, branch, files, and history in
**All work**. Use **Show in attention** to undo. A new failure or blocker after
resuming needs attention again. You can also use
`dotshift dismiss -config ~/.config/dotshift/dotshift.yaml JOB_ID`, or add
`-undo` to restore it.

Tasks, goals, terminal runs, and follow-ups share the same coding, fallback,
testing, and review loop. Review receives the actual diff, changed files, base
and tested commits, and test output. Findings go back into coding. The latest
review evidence is saved next to the job log as `.log.review-evidence.txt`.

After a disconnect, Dot uses `list_tasks` to recover job IDs. Submissions can
include a unique `request_id`; retrying the same ID and arguments returns the
original job. Use `send_input` for more work after a completed job, or
`resume_goal` for a blocker or cancelled job. Both retain the branch and context.
The [MCP reference](docs/mcp.md) lists all tools and reconnection behavior.

`dotshift stop` checkpoints active jobs. On the next start, they resume from
their saved phase and worktree. Explicitly cancelled or blocked jobs wait for
resume. With the macOS service installed, crashes restart automatically and
the tunnel reconnects. `stop` unloads the service for this login session;
`ui` starts it again. `service uninstall` removes automatic startup.

Keep the installed executable at its saved path. Rerun `service install` if you
move it. The service receipt and logs live in `.dotshift/` next to the config.
The browser stays on loopback and uses a private authenticated link; only the
MCP intake is exposed through the configured tunnel.

## Commands

All commands accept `-config FILE` (default `dotshift.yaml` in the current
directory). Use `-config ~/.config/dotshift/dotshift.yaml` with the Homebrew
setup above; put flags before positional arguments such as job IDs.

| Command | Purpose |
| --- | --- |
| `init` | Write subscription defaults; refuses to overwrite existing config |
| `serve` | Run the coding service in the foreground |
| `service install\|uninstall` | Manage macOS login startup and crash recovery |
| `connect` | Show Dot connection details; `-revoke` disconnects clients |
| `check [ALIAS...]` | Check configured agent access |
| `ui [-no-open] [-local-only]` | Open or reconnect to the local monitor |
| `menubar` | Open the macOS status menu; quitting it leaves work running |
| `run -repo NAME [-model ALIAS] "instructions"` | Run a local coding job and wait |
| `jobs [ID]`, `logs ID` | Inspect saved work and output |
| `cancel ID` | Stop a job and preserve its work |
| `dismiss [-undo] ID` | Hide or restore the current issue in Needs attention; keep work and history |
| `clean ID` | Remove an unfinished job's retained worktree |
| `status`, `stop` | Check or stop the local service |

## Optional API providers

API mode remains available for OpenAI, Anthropic, Gemini, Ollama, and supported
custom endpoints. Use `dotshift init -mode api` with a new config and follow
[the API guide](docs/api-mode.md) for Docker, provider keys, checks, and usage
accounting. Existing configurations retain their execution mode; configs that
omit `jobs.mode` retain the older API behavior.

## Development

To build from source instead of using Homebrew, install Go 1.25+, clone this
repository, and build with the checked-in dashboard assets:

```sh
git clone https://github.com/gregwhitaker/dotshift.git
cd dotshift
go build -o dotshift ./cmd/dotshift
```

Use `./dotshift` in place of `dotshift` in the setup commands above. To develop
the dashboard too, install Node.js 24+ and use:

```sh
make                  # build the monitor and ./dotshift
make test             # frontend build/tests and Go tests
```

Node.js 24+ is needed only to develop `web/`. Its generated `web/dist` is embedded
in the Go binary; rebuild it with `make web` after UI edits. Go-only changes can
use `go test ./...` and `go build` with the checked-in assets.

The full test target also checks release tooling with Python 3.9+. GitHub Actions
runs Linux/macOS tests, race detection, native lifecycle checks, and a Homebrew
installation. See the [release guide](docs/releasing.md) for CI setup, packaging
rehearsals, tagged draft releases, and publishing the stable Homebrew formula.
`dotshift version` identifies the installed version and source commit.

The optional [macOS status menu](docs/status-menu.md) is a service client.
The marketing website in `website/` is separate and outside the local runner's
build and runtime. Core development focuses on local coding, model handoff,
review evidence, and reliable startup/reconnection.

## License

[Apache License, Version 2.0](LICENSE).
