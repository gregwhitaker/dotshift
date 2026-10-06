# dotshift MCP spike

A throwaway MCP server that answers one question before we build the real
intake: **will your OpenAI dot call a write tool (`start_task`) on a custom
MCP app added in ChatGPT developer mode?**

Nothing here does real work. `start_task` records the request to
`spike-tasks.jsonl` and prints a big banner in the terminal. The read-only
tools (`list_repos`, `search`, `fetch`, `task_status`) are a baseline, so a
missing `start_task` call can be told apart from a connector that isn't
reaching the server at all.

This is its own Go module under `spike/`, separate from the main dotshift
code, and will be deleted or folded into `dotshift serve` once we have the
answer.

## Run it

Needs Go 1.25+.

```sh
cd spike/mcp
go run . -secret "$(openssl rand -hex 16)"
```

It listens on `127.0.0.1:8787` and serves MCP only at `/<secret>/mcp`.
The secret path is the only protection, since the spike uses ChatGPT's
"No authentication" option; real OAuth comes with the proper intake. Stop
the server and tunnel when you're done.

Flags: `-addr`, `-secret`, `-tasks` (JSONL file), `-repos` (comma-separated).

## Expose it with a tunnel

Pick one. Each prints a public `https://` host.

```sh
cloudflared tunnel --url http://127.0.0.1:8787        # Cloudflare quick tunnel
tailscale funnel 8787                                  # Tailscale Funnel
ngrok http 8787                                        # ngrok
```

Check it: `curl https://<host>/healthz` should print `ok`.

## Add it to ChatGPT

1. Settings → Apps & Connectors → Advanced settings → turn on **Developer mode**.
2. Settings → Apps & Connectors → **Create**.
3. Name: `dotshift spike`. MCP server URL: `https://<host>/<secret>/mcp`.
   Authentication: **No authentication**. Tick "I trust this application" and create.
4. ChatGPT should list the five tools. If it says some are unavailable on your
   plan, note which ones; that is the answer.

## The test

Run these in order and watch the server terminal.

1. **Plain ChatGPT, read baseline.** In a chat with developer mode and the
   `dotshift spike` app enabled: "Use dotshift spike to list my repos."
   Expect `list_repos called` in the log.
2. **Plain ChatGPT, write.** "Use dotshift spike to start a task on
   gregwhitaker/dotshift: add a hello-world Go program." Expect the
   `*** start_task CALLED ***` banner (ChatGPT may ask you to confirm first).
3. **Your dot, write.** Make sure the dot can use the app, then ask it the
   same thing as step 2. This is the step that matters.

## What to report back

For each step: did the call show up in the log, did ChatGPT or the dot show
an error or refuse, and what did it say. Paste the server log lines (they
include the client name and user agent, which tells us whether the call came
from ChatGPT itself or the dot).

| Result | What it means for dotshift |
| --- | --- |
| Step 3 logs `start_task` | MCP intake works as planned. |
| Steps 1–2 work, step 3 doesn't | The dot can't use custom apps; we need a different intake. |
| Step 1 works, step 2 is blocked | Plan limit (read/fetch only) applies; write tools need a Business/Enterprise plan, or we redesign around read-only tools. |
| Nothing reaches the log | Tunnel or URL problem; check `/healthz` and the secret path. |

Automated check (no ChatGPT needed): `go test ./...`
