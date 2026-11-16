Install with **Homebrew** on macOS or Linux. Follow the
[installation guide](https://github.com/gregwhitaker/dotshift/blob/main/docs/homebrew.md)
for the current tap commands, configuration, startup, and upgrades. The package
includes the CLI and, on macOS, the menu bar app built and signed locally.
Run `dotshift version` to identify your installed build.

CLI archives are also available for manual installation and testing (`amd64`
for Intel/AMD, `arm64` for Apple Silicon or Linux ARM64). Each includes the
license and setup documentation.

Verify downloads against `SHA256SUMS`. On macOS use `shasum -a 256 -c SHA256SUMS`;
on Linux use `sha256sum -c SHA256SUMS`, with the listed assets in the same folder.

The macOS app ZIPs are test artifacts with their own CLI. They are ad-hoc signed;
Developer ID signing and notarization are still required before they become
supported direct Mac installers. Use Homebrew for the initial installation.
