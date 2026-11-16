# Install with Homebrew

Homebrew is the recommended installation path on macOS and Linux. It installs
the CLI and, on macOS, the menu bar app. Until the first stable release is
published, install the current `main` branch with `--HEAD`:

```sh
brew tap gregwhitaker/dotshift https://github.com/gregwhitaker/dotshift.git
brew install --HEAD gregwhitaker/dotshift/dotshift
```

The explicit tap URL is required because the source repository is named
`dotshift`, rather than Homebrew's conventional `homebrew-dotshift`.

Homebrew installs Go and uses Apple's Git on macOS, or installs Git on Linux.
macOS builds require macOS 13 or later and Apple's Command Line Tools
(`xcode-select --install`). The dashboard assets are already included, so
installation does not require Node.js.
The menu app is compiled and signed locally. Linux installs the CLI only.

For development, see the [source build instructions](../README.md#development).
The macOS app ZIPs in release artifacts are ad-hoc signed test builds; use
Homebrew for the initial installation.

## Set up

Install the official Codex and Claude Code CLIs and sign in using your own
accounts. Keep configuration and saved work outside Homebrew's Cellar:

```sh
mkdir -p ~/.config/dotshift
dotshift init -config ~/.config/dotshift/dotshift.yaml
codex login
claude auth login
```

Edit `~/.config/dotshift/dotshift.yaml` to add allowed repositories and their
test commands, or a project root and test profiles. `init` refuses to overwrite
an existing configuration. Saved jobs and authentication live in the adjacent
`~/.config/dotshift/.dotshift/` directory.

```sh
dotshift check -config ~/.config/dotshift/dotshift.yaml
dotshift ui -config ~/.config/dotshift/dotshift.yaml
```

For remote Dot access, configure Tailscale using the [connection guide](mcp.md).
Then run `dotshift connect -config ~/.config/dotshift/dotshift.yaml` for the
connection URL and authorization instructions. Local-only work can use
`dotshift ui -local-only -config ~/.config/dotshift/dotshift.yaml` without a tunnel.

On macOS, open the included menu app and optionally install login startup:

```sh
dotshift menubar -config ~/.config/dotshift/dotshift.yaml
dotshift service -config ~/.config/dotshift/dotshift.yaml install
```

The app is at `$(brew --prefix dotshift)/Dotshift.app`. Quitting it leaves the
background service running. Homebrew does not start a service automatically;
Dotshift manages its own login startup and crash recovery.

## Update

Quit the menu app, then stop the service before replacing its binaries. Active
work is checkpointed and resumes after startup:

```sh
dotshift stop -config ~/.config/dotshift/dotshift.yaml
brew update
brew upgrade --fetch-HEAD gregwhitaker/dotshift/dotshift
```

On macOS, rerun `dotshift service -config
~/.config/dotshift/dotshift.yaml install` if you use login startup; this refreshes
the executable path saved by the service. Reopen the menu app with `dotshift
menubar -config ~/.config/dotshift/dotshift.yaml`. If menu-app login startup is
enabled, turn it off and on again in Settings to register the new bundle.
Otherwise, restart with `dotshift ui -config ~/.config/dotshift/dotshift.yaml`.
On Linux, restart `dotshift serve` with the same configuration under your
service manager.

## Uninstall

Disable menu-app login startup in Settings, then quit the menu app. On macOS, remove
service login startup before uninstalling:

```sh
dotshift service -config ~/.config/dotshift/dotshift.yaml uninstall
dotshift stop -config ~/.config/dotshift/dotshift.yaml
brew uninstall gregwhitaker/dotshift/dotshift
brew untap gregwhitaker/dotshift
```

On Linux, stop and remove your service-manager entry before uninstalling.
Homebrew leaves your configuration, saved jobs, worktrees, and repository
branches in place.

## Maintainer checks

Before publishing a formula change, install it from a tap and run:

```sh
brew test gregwhitaker/dotshift/dotshift
brew audit --strict gregwhitaker/dotshift/dotshift
```

The formula test initializes a temporary configuration, starts and stops an
isolated local service, and verifies the macOS app signature. It makes no
model or tunnel requests.

The [release workflow](releasing.md) creates a source archive and generates a
matching `dotshift.rb` with its download URL and checksum. After publishing a
stable release, commit that generated formula to `main`. It retains `head` for
development installs. Stable users can then omit `--HEAD` when installing and
`--fetch-HEAD` when upgrading. Prereleases leave the stable formula unchanged.
