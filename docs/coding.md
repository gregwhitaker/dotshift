# Local coding

A job takes instructions and a repo, and ends as a branch with the agent's
work committed on it. Your working copy is never touched.

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

Jobs can run in repositories you configure below, or in projects registered
inside `projects.root` (see [Starting new projects](#starting-new-projects-with-dot)):

```yaml
repos:
  myapp:
    path: ~/code/myapp
    test: go test ./...      # required; its result is kept on the job

jobs:
  model: smart               # alias used when a job doesn't ask for one
  timeout: 1h
  agent:
    name: codex
    # command: /usr/local/bin/codex
  # worktree_dir: ~/dotshift-worktrees   # default: .dotshift/worktrees
```

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

## Starting new projects with Dot

Enable a workspace folder and named test commands in `dotshift.yaml` once:

```yaml
projects:
  root: ~/workspace
  test_profiles:
    go: go test ./...
    node: npm test
    python: python3 -m unittest discover -s tests -v
```

Restart `dotshift serve` after changing configuration. Dot can now take an
instruction such as **“Create a Node project named habit-tracker, build a local
habit tracker with weekly summaries, and keep fixing it until its tests pass.”**

Dot calls `list_repos` to discover the root and test profiles, then:

1. `create_project` with `name: "habit-tracker"`, `test_profile: "node"`, and an
   optional short description. This creates `~/workspace/habit-tracker` with a
   README and initial commit on `main`, and registers it immediately.
2. `start_goal` with `repo: "habit-tracker"`, the full objective, and concrete
   acceptance criteria. The agent scaffolds the application and meaningful
   tests, then uses the normal coding, testing, fixing, and review loop.
3. `task_status` and `task_result` to follow progress and report the result.

For a project you have already started, use `register_project` with its folder
name and a test profile instead. It must be a Git repository directly inside
`projects.root`, with at least one commit. Registration preserves your files;
jobs start from committed code, so commit any work you want the agent to use.
Repositories elsewhere can still be configured explicitly under `repos:`.

Project names are simple lowercase folder names (letters, digits, `-`, `_`, up
to 64 characters). Creation refuses existing paths. Registration refuses
symlinks, linked worktrees, and repositories outside the configured root. Dot
selects test commands from your local profiles; the MCP tools cannot supply an
arbitrary shell command. The agent must build a real test suite for the chosen
command. Add other profiles locally for different build tools or test runners.

The project catalog is saved in `.dotshift/jobs.db`; new entries are usable
immediately and survive restarts. Removing/changing the root or removing a
profile disables the affected managed entries. Explicit `repos:` entries take
precedence. Project creation itself makes no model calls. Goal results remain
on their task branch, which `task_result` identifies; switch to that branch in
the project folder to run or review the completed application.

## Project goals

Dot can hand off a whole project objective with the `start_goal` MCP tool.
The repository must have a test command under `repos.NAME.test`, or a test
profile selected through `create_project` or `register_project`.

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
