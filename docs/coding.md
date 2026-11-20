# Local coding

A job takes instructions and a repo, and ends as a branch with the agent's
work committed on it. Coding keeps your working copy separate. Dot can apply
the tested and reviewed result to that checkout when you ask.

1. dotshift adds a git worktree for the job on a new branch (`dotshift/<id>`
   unless you name one), starting from the repo's `HEAD`.
2. Subscription mode runs Codex or Claude Code there with native sign-in.
   API mode runs Codex CLI (`codex exec`) through the router. Settings are
   passed on the command line; your saved CLI configuration is not changed.
3. dotshift runs the configured validation command, commits the changes, and
   asks a fresh read-only session to review them. Failed tests and review
   findings return to the coding loop. Work is complete only after both pass.
   The finished worktree is removed and the branch stays. A job that changed
   nothing leaves no branch.

A job reports `done` after its worktree cleanup finishes, so an immediate
follow-up can reuse its branch. Successful review is checkpointed before
cleanup; a restart finishes cleanup without rerunning the agents. If cleanup
fails, the job blocks with the Git error. Fix that problem and use `resume_goal`
without new input to finish cleanup, then send any follow-up instructions.

## Choose a workspace once

Use `dotshift init -workspace ~/workspace` for a new installation. For an
existing configuration, add and restart the service:

```yaml
projects:
  root: ~/workspace
  discover: true
```

Dot's `list_repos` now discovers committed Git repositories directly under that
folder. Dot can use their names directly with `start_task`; there is no project
registration step. Discovery ignores symlinks, linked worktrees, nested
repositories and directories without an initial commit. Explicit `repos:`
entries take precedence, and existing uncommitted files remain untouched.
Choose the intended repository in your request; Dot should ask only if the
name is ambiguous or the location is outside the authorized workspace.

Tests default to `auto` when no custom project profiles are supplied. The runner
checks for a root `make test` target, a `package.json` test script (npm, pnpm,
Yarn or Bun), Go, Cargo, or Python tests. It resolves validation again after each
coding attempt and records the actual command with the tested commit. Missing
validation sends the job back to coding to establish meaningful tests. For
another build system, the agent can provide a root Makefile test target.
Explicit commands remain available for repositories that need them:

```yaml
repos:
  myapp:
    path: ~/code/myapp
    test: ./scripts/test.sh
```

Turning off `projects.discover` removes access to unregistered repositories.
Registered entries still follow their root and profile policy; a removed
profile cannot be bypassed by discovery. Repositories outside the workspace
must be explicitly allowlisted. Existing configurations do not turn on workspace
discovery automatically.

In API mode, first run `dotshift serve` in another terminal. Subscription
mode can run local jobs directly:

```sh
dotshift run -repo myapp "Add a --verbose flag to the CLI"
dotshift run -repo myapp -model claude -branch fix/typo "Fix the typo in README"
echo "long instructions..." | dotshift run -repo myapp -
```

`dotshift run` streams the agent's output and prints the result: the agent's
notes, a diffstat, the test result and how to review the branch. Ctrl-C
cancels the job. `-repo` can be left out when only one repo is allowlisted.

`dotshift jobs` lists every job with its status (`queued`, `running`, `done`,
`failed`, `blocked`, or `cancelled`), and `dotshift jobs ID` shows one in full. A failed or
cancelled job keeps its worktree so you can look at what the agent did;
`dotshift clean ID` removes it. Job state lives in `.dotshift/jobs.db`
(SQLite) and logs in `.dotshift/jobs/`.

Codex runs with `--sandbox workspace-write`, so it can only write inside the
worktree and has no network access for its commands. That's also why dotshift,
not the agent, makes the commit: a worktree's git metadata lives in the main
repo, outside the sandbox. In subscription mode, Claude Code uses its own
required Bash sandbox and restricted file tools.

Subscription agents use the official CLIs' saved sign-ins. Dotshift removes
ambient API credentials and provider overrides from child environments; it does
not read or copy OAuth credentials. User CLI customizations are disabled for
these jobs; organization-managed policy still applies. Provider extra-usage
settings remain under the account owner's control.

## New applications and follow-ups

Ask Dot to build the application in your conversation. Dot calls `create_project`
with a short name and optional description, then `start_task` or `start_goal`
with the objective and acceptance criteria. With automatic tests, no test
profile is needed. The initial repository contains a README and a seed commit;
the agent implements the app and its tests. Creation never overwrites a folder.

Existing custom `test_profiles` remain supported. Dot may choose one when a
specific validation setup is appropriate. `register_project` remains available
for installations with discovery disabled. Registration and creation persist
in the local catalog without editing configuration for each application.

Dot follows an accepted task to completion with `task_status` and `task_result`.
After reconnecting it uses `list_tasks` to recover work. Unique `request_id`
values prevent duplicate tasks when a submission response is lost. A completed
task takes follow-up instructions through `send_input` on the same branch.
For blocked or cancelled work Dot uses `resume_goal`, resolving routine coding
questions from the existing request. Missing access, user decisions and explicit
limits still require appropriate input; retries must not silently remove caps.

## Applying results locally

`task_result` includes `delivery` for a completed result with changes. This
identifies the exact tested commit and the original checkout's current branch
and HEAD, plus `ready`, `applied`, or a blocker. When the user's request includes
applying the change, Dot passes those fields to `apply_task`. The tool only
fast-forwards a clean checkout to that reviewed commit. It never switches
branches, resets, stashes, pushes, or applies an unreviewed branch tip.
An unchanged retry is safe after a lost response.

Local edits are preserved and require a decision about how to proceed. If the
destination diverged, Dot can start a new integration task from the current
destination commit, incorporate the previous result, and run tests and review
again before applying it. A request for a proposal or a separate branch leaves
its result on that branch. Publishing and pushing require their own scope.

## Project goals

Dot can hand off a whole project objective with the `start_goal` MCP tool.
Both `start_task` and `start_goal` use the same autonomous workflow. Automatic
validation is available to discovered repositories and the default project
profile; explicit repository commands and custom profiles remain supported.

```json
{
  "repo": "myapp",
  "goal": "Add a --verbose flag, cover normal and error output in tests, and document its usage.",
  "model": "smart"
}
```

The tool returns a `task_id` immediately. The goal keeps working while dot is
disconnected. `task_status` reports its phase, completed iterations, latest
assessment, and test results. `task_result` returns the accumulated branch,
diff, and outcome. `dotshift jobs` and `dotshift logs` also show goal progress.

Each iteration gives the coding agent the original goal, its cumulative
progress notes, remaining work, and the latest test failures. dotshift runs
the configured tests and commits the iteration's changes. Failed tests feed
the next attempt. Once the agent reports completion and tests pass, a fresh
agent invocation reviews the goal and code with a read-only sandbox. Review
findings feed another coding iteration. The goal reaches `done` only when
that review confirms completion against the tested code.

The runner supplies the reviewer with the base and tested commit IDs, changed
files, a branch diff, and validation output. The implementation's completion
claim is omitted from the review prompt. Evidence is bounded; truncation is
explicit, and material changes that cannot be inspected must not be approved.
The latest evidence is saved beside the task log as `.log.review-evidence.txt`.
Python runs disable bytecode generation, and new projects using the `python`
profile include a `.gitignore` for Python caches and virtual environments.

Goals have no iteration limit by default. Set `max_iterations` in `start_goal`
to bound the number of coding and testing cycles. Each agent invocation and
test command uses `jobs.timeout`. A goal becomes `blocked` when it reaches
the iteration limit, the agent identifies an external dependency requiring
input, or three consecutive agent execution or response-format attempts fail.
With the adaptive strategy, repeated coding failures instead advance through
the model ladder and block if its strongest model also stalls. In API mode, a spending cap
can also block further model calls. Ordinary test failures feed the coding loop.

`resume_goal` continues a blocked or cancelled goal on its existing branch.
It accepts the same `task_id`, optional `input`, an optional replacement
`model`, and an optional new total `max_iterations`. Set that limit to `0`
to remove it. Input supplements the original objective. `cancel_task` stops
the current agent or test process and preserves the worktree.

Goal state is stored in `.dotshift/jobs.db`. Stopping the service saves active
goals as queued. Starting `dotshift serve` resumes them from
their saved phase and worktree, including partial edits. Recovery terminates
any recorded agent or test process left by an interrupted supervisor and
rechecks the repository allowlist. Blocked and cancelled goals wait for an
explicit `resume_goal` call. Keep their worktrees to continue them.
