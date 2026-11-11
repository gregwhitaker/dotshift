package config

// SubscriptionSample uses the official local CLIs and their own saved sign-ins.
const SubscriptionSample = `# Uses your ChatGPT/Codex and Claude Code subscription allowances.
# Install current official codex and claude CLIs, then sign in:
#   codex login
#   claude auth login
# No provider API keys, LiteLLM, or Docker required.

aliases:
  smart: [openai/default, anthropic/default]
  codex: [openai/default]
  claude: [anthropic/default]
# "default" uses the CLI's default subscription model. To pin a model, use
# openai/<Codex model name> or anthropic/<Claude Code model name or alias>.

# Allowlist repositories and a validation command before starting work.
repos: {}
#  myproject:
#    path: ~/src/myproject
#    test: go test ./...

# Uncomment to let Dot create or register projects directly in this folder.
# projects:
#   root: ~/workspace
#   test_profiles:
#     go: go test ./...
#     node: npm test
#     python: python3 -m unittest discover -s tests -v

jobs:
  mode: subscription
  model: smart
  strategy: prefer_openai
  reviewer_model: auto
  timeout: 30m
  # agent:
  #   command: /path/to/codex
  # claude_command: /path/to/claude
  # Set worktree_dir outside any repository's working copy if necessary.

mcp:
  tunnel: tailscale
`

// Sample is the optional API configuration written by `dotshift init -mode api`.
const Sample = `# dotshift configuration.
#
# Coding agents call the router with an alias (e.g. "smart") as the model name.
# Each alias lists models in order: the first serves the request and the rest
# are fallbacks tried when it errors or is rate limited.

router:
  host: 127.0.0.1        # keep on loopback; dotshift is for this machine
  port: 4000
  image: ghcr.io/berriai/litellm:main-stable
  # Clients authenticate with a key dotshift generates (run: dotshift key).
  # Uncomment to use a key from your environment instead.
  # master_key_env: DOTSHIFT_MASTER_KEY
  # The router reports each call's tokens and cost to dotshift, which keeps
  # them in .dotshift/usage.jsonl (see: dotshift usage). Set to off to stop,
  # or to an IP address if the container can't reach the one dotshift picks.
  # usage_listen: off

# API keys are read from these environment variables when dotshift starts.
# They are passed to the router container by name and never written to disk.
providers:
  openai:
    api_key_env: OPENAI_API_KEY
  anthropic:
    api_key_env: ANTHROPIC_API_KEY
  gemini:
    api_key_env: GEMINI_API_KEY
  ollama_chat:
    # Ollama running on this machine, reached from inside the container.
    base_url: http://host.docker.internal:11434

# Edit the model IDs to the ones your accounts have access to.
aliases:
  fast:
    - anthropic/claude-haiku-4-5
    - gemini/gemini-2.5-flash
  smart:
    - anthropic/claude-sonnet-5-5
    - openai/gpt-5
    - gemini/gemini-2.5-pro
  local:
    - ollama_chat/qwen2.5-coder

# Repositories jobs may work in. Jobs never touch these working copies: each
# job gets its own git worktree on a new branch.
repos: {}
#  myapp:
#    path: ~/code/myapp
#    test: go test ./...   # required; run independently before completion review

# Uncomment to let Dot create or register projects directly in this folder.
# projects:
#   root: ~/workspace
#   test_profiles:
#     go: go test ./...
#     node: npm test
#     python: python3 -m unittest discover -s tests -v

jobs:
  mode: api
  model: smart            # alias used when a job doesn't ask for one
  # Project goals can select prefer_openai to use this alias's OpenAI models
  # first, then its Anthropic models, remembering API limits across restarts.
  # strategy: prefer_openai # default: alias (existing router fallbacks)
  # Or use adaptive: order the chosen alias from baseline to strongest.
  # strategy: adaptive
  # escalate_after: 2       # repeated failures or stalled cycles before escalation
  # reviewer_model: auto   # prefer another provider; always a different model
  # max_cost_usd: 10        # per-goal reported-spend cap; 0 disables (default)
  # The final request may cross the cap. Costs include review and fallback.
  timeout: 1h
  agent:
    name: codex           # Codex CLI, run as: codex exec ...
    # command: /usr/local/bin/codex
    # args: ["--sandbox", "workspace-write"]

# The MCP endpoint your OpenAI dot (or any MCP client) calls to start and
# follow jobs. "dotshift serve" runs it; "dotshift connect" shows how to
# reach it through a tunnel and the passcode that approves a client.
mcp:
  listen: 127.0.0.1:8787
  # "dotshift serve" runs Tailscale Funnel, which serves the endpoint at
  # https://<machine>.<tailnet>.ts.net/mcp. Use "none" to run your own tunnel.
  tunnel: tailscale
  # public_url: https://dotshift.example.com   # the URL clients use, if not Funnel's
  # disabled: true
`
