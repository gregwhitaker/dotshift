# Local work monitor

A small React/TypeScript monitor embedded in the dotshift Go binary. It shows
local service connectivity, jobs, model aliases and policies, logs, tests,
changes, and completion notes. Cancel and resume are the only work mutations.
Dot submits work and creates or registers projects through MCP; the CLI can
also run jobs. Keep these paths on the shared runner.

The Models view reads `/api/models`, which uses the same strategy catalog as
Dot. Do not duplicate model selection rules in the frontend. Availability is
observed runner state, not a sign-in check or a remaining-token estimate.

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
Diffs poll every five seconds and show committed changes only. Model choices
refresh every ten seconds. The service owns execution; closing the browser
never cancels work. Older `/projects` links redirect to the work monitor.
