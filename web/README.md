# Local work monitor

An optional React/TypeScript view of Dot's local work, embedded in the Go
binary. The overview shows work in progress, issues, and results. Repository
names are context on each job. Logs and technical details are collapsed.
There is no project sidebar, model dashboard, or iteration-management form.

Each row represents one user request. Explicitly linked follow-ups share a
short title and expandable history; unrelated requests are never grouped just
because they use the same repository. A later linked result with passing
validation can resolve an earlier failed check without rewriting its history.
Running follow-ups remain current. Unrelated success, cancellation and age
never resolve a failure.

The service supplies the same outcome labels to the monitor and menu bar:
ready to apply, applied locally, not applied, or missing validation/review.
Only independently reviewed work can be presented as ready to apply. Display
checks of the checkout refresh within five seconds; applying always rechecks
the actual checkout. Attention explains the needed input or recovery step and
offers a labeled message to copy into Dot, carrying the task context. The
original instructions, task reference and manual dismissal live in details.

The result follows the outcome status directly, with readable Markdown. Changes,
tests, logs, and original instructions are below it in collapsed sections. Logs
and diffs load only when their section is open. Raw HTML and automatically fetched
report images are disabled.

**Connection and help** shows checks of the running service's authorized workspace,
default coding/review access, and Dot connection. Browser and menu share one cached
result, refreshed every minute; **Check again** explicitly rechecks without changing
sign-ins, limits, or jobs. Slow checks do not block normal status polling. A public
health response alone never means Dot is connected: recent authenticated contact
in this service session is also required, and its time is shown. Stale checks return
to an unchecked state. Checks make no coding/model requests and cannot measure
remaining subscription allowance. First connection and reconnect instructions stay
inside this help panel, with a context-bearing message to hand routine recovery to Dot.

Dot handles discovery, creation, submission, follow-ups, recovery, and applying
reviewed results through MCP. The monitor can stop work or dismiss an issue.
It displays whether a completed result has been applied, but does not apply
changes itself. Keep all execution and model policy in the shared runner.

Use the Offset brand kit: Paper and Ink surfaces, Shift blue actions, Manrope
for interface text, and IBM Plex Mono for technical details. Dark mode uses
Night and Ink with the lighter blue accent. Keep semantic status colors for
indicators and diffs. Avoid project-management forms, analytics cards, billing
screens, or another chat interface.

From the root, `make web` synchronizes the shared brand assets and fonts,
installs locked dependencies, typechecks, and builds. `make brand-check` detects
drift between the brand masters, dashboard, and marketing copies.
`make test` also runs the UI model tests and Go suite. Development needs Node.js
24+; installed dotshift needs neither Node nor a frontend server. `dist/` is
generated and tracked for Go embedding. Rebuild Go after changing the assets.

Open with `dotshift ui -config FILE`. For isolated UI checks use a subscription
config with `repos: {}` and `mcp.disabled: true`, then stop that config's service
with `dotshift stop -config FILE`. Use a local fake agent for job-action QA.
Never put service tokens in frontend source, local storage, or query strings.

Overview and job details poll every two seconds (less often in hidden tabs).
Diffs poll every five seconds and show committed changes only. The service owns execution; closing the browser
never cancels work. Older `/projects` and `/models` entry points lead to the work monitor.
