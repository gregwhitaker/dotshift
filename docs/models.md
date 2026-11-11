# Model selection

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
can support it regardless of which provider is listed first. Use model IDs your
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
use `"alias"` to return to configured alias order. Tasks, goals, CLI runs, and
follow-ups use the same selection and review loop. `start_goal` accepts explicit
policy controls; `start_task` uses the configured policy, and `send_input`
inherits the previous job's choices. External router clients manage their own
agent lifecycle.

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
  "reviewer_model": "auto"
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
provider, then another model. Subscription mode also permits the coding model
as the last choice, always in a fresh read-only review session. An available
reviewer proceeds immediately instead of waiting for another provider's reset.
The reviewer gets a fresh context and read-only access after tests pass; its
findings feed another coding cycle, and its approval is required to finish.
API mode and explicitly named reviewer aliases retain the different-model
requirement. If every eligible reviewer is unavailable, the goal waits or
blocks. Reviewer pools are validated against every possible implementation model.
`goal.reviewer_selection` records the reviewer separately from coding selection.
With no separate reviewer configured, fresh review uses the coding model.

These options can also be defaults for new goals:

```yaml
jobs:
  model: smart
  strategy: adaptive
  escalate_after: 2
  reviewer_model: auto
```

API spending controls are documented separately in [API mode](api-mode.md#project-spending-caps-api-mode).
