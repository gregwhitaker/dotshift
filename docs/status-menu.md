# macOS menu bar app

The menu bar app is Dotshift’s main interface on macOS. It shows whether Dot can
work on this computer, what is happening now, and any input needed from you.
Requests and follow-ups stay in your conversation with Dot. The background
coding service runs independently of the menu app.

Install with [Homebrew](homebrew.md), then open the included menu app using
the configuration created during setup:

```sh
dotshift ui -config ~/.config/dotshift/dotshift.yaml
```

Homebrew installs the app at `$(brew --prefix dotshift)/Dotshift.app`, with its
own bundled CLI. The launcher remembers your configuration, working directory,
and executable search path, so it finds the same coding CLIs as your terminal.
You can also open the app directly and choose a configuration. Keep the selected
CLI at its saved location; choose the configuration again in Settings to use
the app's bundled CLI instead. See the Homebrew guide when upgrading.

The menu-bar icon is the Offset mark: five equal dots with one shifted to the
upper right. The dots are normally monochrome, with macOS supplying the
appropriate color. When work needs attention or the service is unavailable,
the existing upper-right dot turns Shift blue (`#2F54EB` on light menu bars,
`#7C98FF` on dark menu bars). The other four dots stay monochrome. Both states
keep the same shape and width, so neighboring menu items stay in place. The
app bundle uses the Ink-and-blue Offset app icon. Update and relaunch the menu
app to load updated branding.

The menu opens with the shared readiness status. Click it for the explanation.
**Current work** shows the request and its coding, testing, review, or automatic
retry state directly in the menu. **Stop Work** saves progress; Dot can resume it
later. A stopped request requiring routine repair is labeled **Waiting for Dot to
continue**, rather than suggesting that it is still running.

**Needs you** appears only for a specific question, missing access, or a decision
about an existing limit. It shows the actual request; click to read the full
explanation, then answer in your conversation with Dot. Routine test failures and
review fixes do not trigger personal notifications or the blue attention dot.
When idle, the menu says nothing is running and there is no work requiring input.
It does not list completed requests or old test runs.

**Troubleshooting** contains history, connection details, service logs, and explicit
service start/stop/restart actions. **History and Details** opens the optional
browser archive at `/history`; individual results keep their tests, diffs, logs,
and linked follow-ups. Existing records are preserved. The browser’s default page
is a small status view, with the archive behind Troubleshooting.

Connection details check the authorized workspace, configured coding and review
access, and Dot’s authenticated contact with this service. Public endpoint health
alone does not confirm Dot access. Checks make no model requests and do not
estimate remaining subscription allowance. Work refreshes every five seconds;
connection checks refresh every minute and can be requested with **Check Again**.
Checks never restart work or change sign-ins.

`dotshift ui` opens the menu app on macOS. `dotshift ui -path /history` explicitly
opens the browser archive; `-path /` opens browser status. `-no-open` starts or
reuses the service without opening either interface. Linux uses browser status.

**Settings** contains login startup, completion and personal-input notifications,
and configuration selection. Notifications are opt-in, require macOS permission,
and report new changes while the menu app is running. Launch at login starts
the menu app and reconnects to or starts the service; it is independent of
`dotshift service install`, which manages service startup and crash recovery.

**Quit Menu Bar App only quits the menu app.** It never stops the service,
cancels tasks, or removes service startup. Closing Settings also leaves work
running. Use **Troubleshooting → Stop Service** or `dotshift stop` to stop explicitly.

## Build and test from source

For development, build the native app with macOS 13+, Xcode Command Line Tools,
Go, and Node.js:

```sh
make menubar
./dotshift menubar -config /path/to/dotshift.yaml
```

This creates `build/Dotshift.app` with its own bundled CLI. You can move it to
Applications and launch it with
`./dotshift menubar -config /path/to/dotshift.yaml -app /Applications/Dotshift.app`.

Run the native model/notification/command tests with `make test-menubar`.
After building the app, `make test-menubar-lifecycle` uses a macOS desktop
session and an isolated fake agent to verify that a running goal finishes
after the real native app receives Quit. It makes no model requests.
The CLI bridge and dashboard links are covered by `go test ./...` and
`npm --prefix web test`. Local bundles are ad-hoc signed; distributing the app
to other Macs requires Developer ID signing and notarization.
