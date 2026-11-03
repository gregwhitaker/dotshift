package config

// Sample is the dotshift.yaml written by `dotshift init`.
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
#    test: go test ./...   # optional; run after the agent, result kept on the job

jobs:
  model: smart            # alias used when a job doesn't ask for one
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
