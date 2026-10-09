# dotshift plan

Repo copy of the [dotshift plan](https://claude.ai/code/artifact/aaee05fe-71d2-45f2-9e8c-34703deb0c1b) (the canonical, editable version). Last synced 2026-10-09.

## Summary

dotshift is a local service your OpenAI dot calls over MCP to hand off coding work. dotshift does the work on your machine with the model you choose and returns the result. The same router also serves any coding agent you run yourself.

The dot can't be re-pointed: it runs in OpenAI's cloud on Astra, with no setting to swap its model. What it can do is call plugins, and a plugin is an MCP server plus skills ([AdaptlyPost](https://adaptlypost.com/blog/openai-dots-plugin-social-media-posting)). MCP is an open standard, so the dot is the only vendor in the loop.

- **Part 1, the router:** an OpenAI-compatible endpoint on localhost, provided by LiteLLM Proxy, with aliases and fallback rules for which model handles what.
- **Part 2, the hand-off:** a pluggable intake layer. The first intake is an MCP server the dot calls with tools like `start_task`; the job runner behind it never knows which intake was used.

## Decisions so far

| Decision | Choice | Date |
| --- | --- | --- |
| How the dot reaches dotshift | MCP plugin (custom app in ChatGPT developer mode). No GitHub-dependent designs. | 2026-10-06 |
| Language for dotshift's own code | Go. No Python code in this repo. | 2026-10-06 |
| Provider adapters and routing | LiteLLM Proxy as a prebuilt sidecar | 2026-10-06 |
| How LiteLLM runs | Its official Docker image only | 2026-10-06 |
| Greg's ChatGPT plan | 20x max (individual), so the custom-app read/fetch limit may apply | 2026-10-06 |

## Architecture

```
  Your dot (OpenAI cloud)
        |  MCP tool calls
        v
  Tunnel + OAuth (public HTTPS URL)
        |
  ======|=========== your machine (dotshift) ===================
        v
  MCP intake  (start_task, task_status, ...)      dotshift run (CLI intake)
        |                                               |
        +-------------------+---------------------------+
                            v
                       Job runner  (git worktree per job + coding agent)
                            |   OpenAI API, alias e.g. "smart"
  Your own agents --------->|
  (Codex CLI, Aider, ...)   v
                       LiteLLM Proxy  (Docker, 127.0.0.1:4000)
  ==========================|====================================
                            v
          OpenAI     Anthropic     Gemini     Ollama / custom
```

Everything flows into the router: the dot's jobs and your own agents both reach models through the same OpenAI-compatible endpoint. Only the MCP endpoint is exposed, through the tunnel.

## Part 1: the router

The router is useful on its own: any coding agent that accepts a custom base URL can use it, with or without the dot.

**What it exposes** (served by LiteLLM Proxy)

- `POST /v1/responses` and `POST /v1/chat/completions`, including streaming and tool calls.
- `GET /v1/models`, listing the configured aliases (for example `fast`, `smart`, `local`).
- Later: a local view of recent requests, which model served each, tokens and cost.

**Routing rules** (in `dotshift.yaml`, compiled to LiteLLM config)

- By alias: the agent asks for `smart` and gets whatever it's mapped to.
- Fallback: if a model errors or rate-limits, the next model in the alias's list is tried.
- Per task: the job runner can request a specific alias per task.
- Later, by cost or request size, once there's usage data.

**Which coding agent**

Codex CLI supports custom model providers with their own base URL, so it runs against dotshift unchanged. Other agents (Aider, OpenHands, opencode) work the same way. The plan starts with Codex CLI.

## Part 2: how the dot hands off work

The dot calls dotshift directly over MCP. Intake is a small interface, so MCP is one adapter and others (a CLI, Slack, a webhook) can be added without touching the job runner or router.

**MCP tools**

| Tool | What it does |
| --- | --- |
| `list_repos` | Lists the local repos dotshift is allowed to work in |
| `start_task` | Starts a coding job: repo, instructions, optional model alias and branch name |
| `task_status` | Returns progress, the agent's latest notes, and whether it's waiting on input |
| `task_result` | Returns the diff summary, test results, and branch name |
| `send_input` | Answers a question the agent asked mid-task |
| `cancel_task` | Stops a job |

**How a job runs**

1. You ask your dot for a change, and it calls `start_task`.
2. dotshift creates a git worktree on a new branch, so jobs never touch your working copy.
3. It launches the coding agent in that worktree, pointed at the router with the requested alias.
4. The agent edits, runs tests and commits. Pushing is optional and works with any git remote.
5. The dot polls `task_status`, answers questions with `send_input`, and reports back to you.

**Reaching your machine**

ChatGPT only connects to remote MCP servers ([OpenAI help](https://help.openai.com/en/articles/12584461-developer-mode-apps-and-full-mcp-connectors-in-chatgpt-beta)). dotshift exposes only its MCP endpoint through a tunnel you pick (Cloudflare Tunnel, Tailscale Funnel or ngrok), protected by OAuth. The router stays on localhost.

**Intake adapters**

| Intake | When it helps |
| --- | --- |
| MCP (first) | The dot calls dotshift directly |
| CLI (`dotshift run`) | You start jobs from your terminal, no dot involved |
| Others later | Slack, a webhook, or an issue tracker, only if you want them |

## Tech choices

dotshift is written in Go, and LiteLLM runs beside it as a prebuilt proxy configured only with YAML. No Python code lives in this repo.

| Choice | Proposal | Why |
| --- | --- | --- |
| Language | Go | One static binary, good at long-running daemons and supervising processes |
| Model routing | LiteLLM Proxy, run as a sidecar | Already serves `/v1/chat/completions` and `/v1/responses`, aliases, fallbacks, usage logs and 100+ providers |
| Running LiteLLM | Its official Docker image | Prebuilt; dotshift starts, health-checks and restarts it |
| MCP server | Official MCP Go SDK, streamable HTTP transport | The transport ChatGPT's remote MCP expects |
| Config | One `dotshift.yaml`; dotshift generates LiteLLM's `config.yaml` from it | One file to edit |
| Secrets | Provider keys from environment variables, passed to LiteLLM by name | Keeps keys out of config files and command lines |
| Job state | SQLite | One file, no server to run |
| Packaging | `dotshift serve` starts the MCP server, job runner and LiteLLM together | One command to run locally |

**Where LiteLLM fits**

| Layer | Owned by | Job |
| --- | --- | --- |
| MCP intake | dotshift (Go) | Tools the dot calls, OAuth, repo allowlist |
| Job runner | dotshift (Go) | Worktrees, launching the coding agent, job state in SQLite |
| Supervisor and config | dotshift (Go) | Turns `dotshift.yaml` into LiteLLM config, runs and restarts the proxy |
| Coding agent | Codex CLI (swappable) | Reads code, edits, runs tests; talks only to LiteLLM |
| Router and adapters | LiteLLM Proxy (prebuilt) | Aliases like `smart`, fallbacks, usage, and translation for OpenAI, Anthropic, Gemini, Ollama and others |

Replacing LiteLLM later means writing Go adapters behind the same localhost URL; nothing above that layer changes.

## Milestones

Milestone 2 is the gate for the MCP work: it proves your dot will call a custom MCP tool that starts work.

1. **Router MVP with LiteLLM.** `dotshift serve` starts LiteLLM Proxy, which exposes `/v1/chat/completions` and `/v1/responses` with OpenAI, Anthropic, Gemini and Ollama behind aliases. Done when Codex CLI completes a small coding task through it on each provider. *Status: code in PR #2; awaiting a run with real keys.*
2. **MCP spike (gate).** A stub MCP server with `ping` and a `start_task` that only logs, behind a tunnel with OAuth. Done when your dot calls both from ChatGPT.
3. **Job runner.** Intake interface, worktree per job, Codex CLI launched against the router, status stored in SQLite. Usable from a `dotshift run` CLI.
4. **MCP intake v1.** All six tools wired to the job runner, plus the repo allowlist.
5. **Routing polish.** Per-task model choice, a cost and usage view, and per-model smoke tests. (Fallback chains already shipped in milestone 1.)

## Open questions and risks

The biggest risk is the ChatGPT plan. OpenAI's help page lists full MCP with write actions for Business, Enterprise and Edu, and read/fetch only for individual plans. That page doesn't mention dots, so milestone 2 tests it directly.

- [ ] If the dot won't call `start_task`, is it acceptable to present it as a read-style tool, or to upgrade the plan?
- [ ] Which tunnel do you already use, if any?
- [ ] Should dotshift push branches, or only leave local branches for you?
- [ ] Which repos should be on the allowlist first?

**Other risks**

- A public endpoint that runs code on your machine is a serious attack surface. It needs OAuth, a repo allowlist, worktree isolation, and no shell access beyond what the agent runs.
- Tool-call and streaming formats differ between providers, so some models will be worse at coding agents than others. Per-model smoke tests catch this early.
- Dots and developer mode are new and in beta, so their plugin behaviour may change.

## Sources

- [OpenAI DevDay 2026 recap, benchlm.ai](https://benchlm.ai/blog/posts/openai-devday-2026)
- [OpenAI Dots and routing, orcarouter.ai](https://www.orcarouter.ai/blog/openai-dots-agents-devday-2026)
- [OpenAI dots plugin setup, AdaptlyPost](https://adaptlypost.com/blog/openai-dots-plugin-social-media-posting)
- [Developer mode and MCP apps in ChatGPT, OpenAI help](https://help.openai.com/en/articles/12584461-developer-mode-apps-and-full-mcp-connectors-in-chatgpt-beta)
- [DevDay 2026 recap, InfoQ](https://www.infoq.com/news/2026/10/openai-devday-2026/)
