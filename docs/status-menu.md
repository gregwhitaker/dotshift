# Optional macOS status menu

This optional status client is separate from the background coding service.
The service does not depend on the menu app.

Build the native app with macOS 13+, Xcode Command Line Tools, Go, and Node.js:

```sh
make menubar
./dotshift menubar -config /path/to/dotshift.yaml
```

This creates `build/Dotshift.app` with its own bundled CLI. The launcher remembers
your configuration, working directory, and executable search path, so it finds
the same coding CLIs as your terminal. You can move the app to Applications and
use `dotshift menubar -app /Applications/Dotshift.app`, or open the app directly
and choose a configuration. Keep the selected CLI at its saved location; choose
the configuration again in Settings to use the app's bundled CLI instead.

The menu-bar icon is the Offset mark: five equal dots with one shifted to the
upper right. It is a monochrome template so macOS supplies the appropriate
color. A smaller notification dot appears in the lower-right corner when work
needs attention or the service reports an error. Both states keep the same
width, so neighboring menu items stay in place. The app bundle uses the
Ink-and-blue Offset app icon. Rebuild and relaunch the menu app to
load updated branding.

The menu shows the service state and counts of active work and work needing
attention. **Open Dashboard** opens the work monitor; **Models** shows the configured
aliases and policies. Start new work by asking Dot. Work submenus show up to five tasks with their current phase;
select one to open its details, or use **View All** for a filtered list.

**Connections** checks local service status, the remote MCP health endpoint,
subscription CLI sign-in, and observed model cooldowns. These checks make no
model requests and do not estimate remaining subscription allowance. Work
refreshes every five seconds; connection checks refresh every minute and can
also be requested manually. **Service** provides explicit start, stop, restart,
and log actions. Stopping saves active goals for the next service start.

**Settings** contains login startup, completion and attention notifications,
and configuration selection. Notifications are opt-in, require macOS permission,
and report new changes while the menu app is running. Launch at login starts
the menu app and reconnects to or starts the service; it is independent of
`dotshift service install`, which manages service startup and crash recovery.

**Quit Menu Bar App only quits the menu app.** It never stops the service,
cancels tasks, or removes service startup. Closing Settings also leaves work
running. Use **Service → Stop Service** or `dotshift stop` to stop explicitly.

Run the native model/notification/command tests with `make test-menubar`.
After building the app, `make test-menubar-lifecycle` uses a macOS desktop
session and an isolated fake agent to verify that a running goal finishes
after the real native app receives Quit. It makes no model requests.
The CLI bridge and dashboard links are covered by `go test ./...` and
`npm --prefix web test`. Local bundles are ad-hoc signed; distributing the app
to other Macs requires Developer ID signing and notarization.
