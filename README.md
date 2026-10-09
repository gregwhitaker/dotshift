# dotshift

dotshift is a local model router for coding agents. You give it one config file
listing your providers and a few aliases like `fast`, `smart` and `local`. It
runs a [LiteLLM](https://github.com/BerriAI/litellm) proxy in Docker that speaks
the OpenAI API, so any coding agent with a custom base URL can use OpenAI,
Anthropic, Gemini or a local Ollama model by asking for an alias.

This is milestone 1 of the plan: the router. Letting an OpenAI dot hand work to
dotshift over MCP comes next.

## Requirements

- Go 1.24+ to build
- Docker, which runs the LiteLLM proxy
- API keys for the providers you want, as environment variables

## Quick start

```sh
go install github.com/gregwhitaker/dotshift/cmd/dotshift@latest

dotshift init                      # writes dotshift.yaml
export ANTHROPIC_API_KEY=...       # and/or OPENAI_API_KEY, GEMINI_API_KEY
dotshift serve                     # starts the router, Ctrl-C stops it
```

`dotshift serve` prints the base URL (`http://127.0.0.1:4000/v1` by default)
and follows the router's logs.

## Commands

| Command | What it does |
| --- | --- |
| `dotshift init` | Writes a starter `dotshift.yaml` |
| `dotshift serve` | Generates the LiteLLM config, starts the container, waits until it's healthy, then follows its logs |
| `dotshift status` | Shows whether the router is up and which models it serves |
| `dotshift stop` | Removes the router container |
| `dotshift key` | Prints the API key clients send to the router |
| `dotshift litellm-config` | Prints the LiteLLM config generated from `dotshift.yaml` |

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

## Development

```sh
go test ./...
go build ./cmd/dotshift
```
