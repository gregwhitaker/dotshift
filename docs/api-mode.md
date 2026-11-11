# Optional API mode

The main setup uses subscription CLIs. Use this mode for API credentials, Gemini,
Ollama, or another supported endpoint. It remains available through explicit
configuration; Docker and usage accounting are not part of subscription setup.

## API mode requirements

- Go 1.25+ to build
- Docker, which runs the LiteLLM proxy
- Codex CLI for coding tasks and project goals
- API keys for the providers you want, as environment variables

## API mode quick start

```sh
go install github.com/gregwhitaker/dotshift/cmd/dotshift@latest

dotshift init -mode api            # writes an API configuration
export ANTHROPIC_API_KEY=...       # and/or OPENAI_API_KEY, GEMINI_API_KEY
dotshift serve                     # starts the router and MCP intake, Ctrl-C stops them
```

`dotshift serve` prints the base URL (`http://127.0.0.1:4000/v1` by default)
and follows the router's logs.

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

See [model selection](models.md) and [local coding](coding.md) for job controls.
