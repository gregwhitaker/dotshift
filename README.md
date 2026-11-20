<p>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="brand/svg/dotshift-logo-dark.svg">
    <img alt="dotshift" src="brand/svg/dotshift-logo-light.svg" height="56">
  </picture>
</p>

# dotshift

Make it easy for Dot to code autonomously on your computer. Describe a change
in your conversation; Dotshift runs the local coding, tests, fixes, and a
separate review. Ask Dot for follow-ups or to apply the finished changes.

Choose a workspace once. Dot discovers its repositories and can create new
ones there. Test commands are discovered from the code. OpenAI runs first by
default, with Claude taking over after an observed usage limit. Work continues
while Dot is disconnected and survives service restarts.

On macOS, the menu bar app shows whether Dot is ready, what it is doing, and
anything that needs your input. History and logs live under Troubleshooting.
Keep requests, results, and follow-ups in your conversation with Dot.

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
dotshift init -config ~/.config/dotshift/dotshift.yaml -workspace ~/workspace
```

Use the folder containing your repositories in place of `~/workspace`. This
explicitly lets Dot discover committed Git repositories directly inside that
folder and create new ones there. Existing files stay separate during coding.
Dot can apply a tested and reviewed result when your request calls for it.

`init` defaults to subscription mode and refuses to overwrite an existing
configuration. For an existing installation, add this once and restart:

```yaml
projects:
  root: ~/workspace
  discover: true
```

With no custom test profiles, validation defaults to `auto`: `make test`, a
package's test script, Go, Cargo, or Python tests. The agent sets up meaningful
tests and fixes validation failures. Existing explicit repository test commands
and model preferences remain supported. See [local coding](docs/coding.md) for
workspace boundaries and custom validation.

```sh
dotshift check -config ~/.config/dotshift/dotshift.yaml
# macOS: start the service and open the menu app
dotshift service -config ~/.config/dotshift/dotshift.yaml install
dotshift ui -config ~/.config/dotshift/dotshift.yaml
dotshift connect -config ~/.config/dotshift/dotshift.yaml
```

`check` verifies CLI installation and sign-in without model calls. On macOS,
`service install` starts the service now, at login, and after a crash; `ui`
opens the included menu bar app. On Linux, start
`dotshift serve -config ~/.config/dotshift/dotshift.yaml` in the foreground or
under your own service manager.

Remote Dot access uses Tailscale Funnel; configure it using
[the connection guide](docs/mcp.md), then use `connect` for the connection URL
and authorization instructions. Local terminal jobs do not need a tunnel.
Keep the computer awake and connected while it works.

Then ask Dot:

> Use dotshift to add search to myapp, cover it with tests, and apply the
> finished changes locally.

Dot selects the repository, follows the work, retrieves the tested result,
and applies it to a clean checkout when requested. A dirty or diverged checkout
is preserved. Continue the same conversation for revisions or recovery.

## Preferences stay optional

The default is OpenAI coding first, Claude fallback, and a fresh independent
review. Tell Dot when you want a particular configured model or policy; everyday
work uses your defaults. [Model selection](docs/models.md) covers explicit
aliases, fallback order, and adaptive escalation.

Fallback responds to limits reported by the providers. It cannot predict your
remaining subscription allowance. If all eligible models are limited, work
waits for a known reset or blocks with progress saved. Missing sign-in or
credentials still require you. Subscription mode does not enforce dollar caps.

## Check progress and recover work

```sh
dotshift ui -config ~/.config/dotshift/dotshift.yaml
dotshift jobs -config ~/.config/dotshift/dotshift.yaml
dotshift jobs -config ~/.config/dotshift/dotshift.yaml TASK_ID
dotshift logs -config ~/.config/dotshift/dotshift.yaml TASK_ID
```

The menu bar app is the main interface on macOS. It shows readiness and current
work directly, with **Stop Work** available while a request runs. **Needs you** is
reserved for a specific question, missing access, or a decision about a limit.
Routine coding problems remain Dot’s work and do not generate personal alerts.
Closing the menu app leaves coding running.

**Troubleshooting → History and Details** opens the browser archive. Related
follow-ups share one request and expandable history. Results distinguish changes
ready to apply from changes already applied locally; tests, diffs, and logs stay
in expandable details. Nothing in history must be managed to start new work.

The browser and menu share readiness checks for workspace, coding/review access,
and Dot’s authenticated connection. **Troubleshooting → Connection Details** lets
you check again. Checks do not start coding or change access. A running service
alone does not count as a confirmed Dot connection.

For direct browser access, use `dotshift ui -path /history -config FILE`.
`dotshift ui -path / -config FILE` shows current status. On Linux, `dotshift ui`
opens browser status by default. `-no-open` starts or reuses the service without
opening an interface. Old requests and technical notices remain in history;
manual dismissal is available inside a request’s technical details.

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
| `init [-workspace DIR]` | Authorize a workspace and write subscription defaults; refuses to overwrite existing config |
| `serve` | Run the coding service in the foreground |
| `service install\|uninstall` | Manage macOS login startup and crash recovery |
| `connect` | Show Dot connection details; `-revoke` disconnects clients |
| `check [ALIAS...]` | Check configured agent access |
| `ui [-no-open] [-local-only] [-path PATH]` | Open the macOS menu app; use `-path /history` for browser diagnostics |
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

The [macOS menu bar app](docs/status-menu.md) is a service client.
The marketing website in `website/` is separate and outside the local runner's
build and runtime. Core development focuses on local coding, model handoff,
review evidence, and reliable startup/reconnection.

## License

[Apache License, Version 2.0](LICENSE).
