# dotshift scope

Updated 2026-11-16. This scope replaces the earlier router-first plan.

## Core promise

The user describes work to Dot and gets tested local changes back without
using a project dashboard or managing agent iterations. Dot hands off the job. Dotshift selects from configured models,
works in an isolated Git worktree, runs tests, fixes failures, and obtains a
separate read-only review. It continues an accepted job while Dot is disconnected.

The first supported workflow is OpenAI coding until an observed allowance limit,
then Claude continuing the same files and completing a fresh review without
waiting for OpenAI. Dot's conversation remains in Dot.

## Keep

- MCP submission and discovery; CLI access to the same runner.
- One authorized workspace; repository discovery, local creation, and automatic
  validation. Explicit repository settings and test profiles remain optional.
- Configured model aliases, explicit choices, ordered fallback, OpenAI-first
  selection, adaptive escalation, and independent reviewer choices.
- One coding/test/review loop for tasks, goals, and follow-ups.
- Saved progress, branch isolation, cancellation, resume, and idempotent retries.
- Login startup, crash recovery, tunnel supervision, and authenticated access.
- A small optional monitor for current work, issues, and results. Put logs and
  technical details behind disclosure; keep follow-ups and recovery in Dot.
- Explicit application of tested and reviewed changes to a clean checkout.
  Preserve local edits and require fresh validation after divergent integration.

## Trim and defer

- Browser project creation and goal submission: Dot already owns this interaction.
- Dashboard statistics cards and product marketing inside the work monitor.
- API router, Docker setup, and accounting in the primary onboarding path.
  Existing API mode and its additional providers remain an explicit option.
- Expansion of the native app, marketing site, billing UI, or additional intake
  integrations. The optional macOS status menu remains only a service client.
- Another chat interface or a system that attempts to change Dot's own model.

## Architecture

```text
Dot / MCP client                     local CLI
       |                                 |
       +--------- shared job runner -----+
                          |
              saved progress + worktree
                          |
           selected coding agent -> tests
                          |           |
                          +-- fixes <-+
                          |
                  fresh read-only review
                          |
                    local result branch
```

Subscription execution uses the official Codex and Claude Code sign-ins.
Optional API execution uses the existing LiteLLM router. Model policy belongs
in the runner, so interfaces do not implement their own selection logic.

## Acceptance checks

Through authenticated MCP, discover an existing repository without registration,
submit plain instructions with default settings, repair failing tests, obtain
a separate review, recover after reconnecting, apply the exact reviewed commit,
and deliver a follow-up. Create and build a new repository without choosing a
test profile. None of these steps requires the dashboard. Automatic application
must refuse local edits, unreviewed commits, and diverged or moved destinations.

A small task and its follow-up should survive a simulated OpenAI limit, finish
with Claude, pass meaningful tests, and receive a fresh review. A service crash
should preserve progress and reconnect the tunnel. Repeating an identified
submission should return the original task. A pinned model alias and explicit
reviewer policy should retain their meaning.

The [README](../README.md) is the entry point; [MCP](mcp.md),
[model selection](models.md), and [local coding](coding.md) document behavior.
