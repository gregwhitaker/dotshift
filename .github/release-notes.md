Download the CLI archive for your operating system and architecture (`amd64`
for Intel/AMD, `arm64` for Apple Silicon or Linux ARM64). Each CLI archive
includes the license and setup documentation. macOS app ZIPs include their own
CLI. Run `dotshift version` to identify a build.

Verify downloads against `SHA256SUMS`. On macOS use `shasum -a 256 -c SHA256SUMS`;
on Linux use `sha256sum -c SHA256SUMS`, with the listed assets in the same folder.

The macOS apps are ad-hoc signed, not Developer ID signed or notarized. Homebrew
builds the app locally and is the recommended installation path for this release.
Direct app distribution still needs the signing/notarization step described in
the maintainer release guide.

Stable releases include a generated `dotshift.rb`. After publication, the
maintainer updates the tap with this file to enable `brew install
gregwhitaker/dotshift/dotshift`. Prereleases do not update the stable formula.
