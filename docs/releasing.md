# CI and releases

The workflows live in `.github/workflows/ci.yml` and
`.github/workflows/release.yml`. Push them to `main` to enable GitHub Actions.
They use the repository's built-in `GITHUB_TOKEN`; no personal token or model
credentials are needed. Jobs have read-only repository access except the final
job that creates or updates a draft release.

## Continuous integration

CI runs on pull requests, pushes to `main`, manual dispatch, and release builds:

- Linux on the latest stable Go and the latest Go 1.25 patch; macOS on stable Go.
- Go formatting, `go vet`, the full Go suite with `-race`, and CLI build checks.
- Brand synchronization, dashboard build/tests, and a check that the committed
  dashboard assets match the source.
- macOS native unit tests, app build, and the real Quit/service lifecycle test.
- A dependency-resolving Homebrew install of the checked-out commit, followed
  by the formula test, audit, and style checks. The test creates and removes a
  temporary tap and package; it refuses to replace an existing installation.
- Workflow validation with actionlint and release-tooling regression tests.

Set **CI passed** as a required status check in the repository's `main` branch
rules after its first successful run. It fails if any required job fails or is
skipped. Actions are pinned to commit SHAs; Dependabot proposes weekly updates
to those pins.

Local equivalents are `make test`, `go test -race ./...`, `make test-menubar`,
and, after building the app, `make test-menubar-lifecycle`. Python 3.9+ is needed
for the release tests and packaging scripts. `python3 scripts/test-homebrew.py`
is intended for a clean machine and installs normal Homebrew dependencies.
For a local smoke test with dependencies already installed, add
`--skip-dependencies`; CI always exercises dependency resolution.

## Rehearse without publishing

Use **Actions → Release → Run workflow** on `main`, or:

```sh
gh workflow run release.yml --ref main -f version=0.0.0-dev
```

Manual runs execute CI and produce a downloadable `release-bundle` workflow
artifact. They do not create a tag or a GitHub release. This verifies all four
native runners before the first release: Linux amd64/arm64 and macOS
Intel/Apple Silicon. Workflow artifacts are retained for seven days.

To exercise packaging locally on an Apple Silicon Mac:

```sh
make web
make test-release
python3 scripts/release.py build 0.0.0-dev --os darwin --arch arm64
make test-menubar-lifecycle
```

Use `--arch amd64` on an Intel Mac. Linux CLI builds can be cross-compiled;
macOS app archives require a Mac of the matching architecture. Files are
written under `build/release/`. Use a fresh output directory for each version.

## Create the first release

1. Merge and push the release commit to `main`. Commit any regenerated
   dashboard assets and wait for CI to pass. Rehearse the release workflow once
   before creating the first tag.
2. From a clean checkout of that commit, create and push an annotated tag:

   ```sh
   git tag -a v0.1.0 -m "Dotshift 0.1.0"
   git push origin v0.1.0
   ```

   Tags must be `vMAJOR.MINOR.PATCH`, optionally with a prerelease suffix such
   as `v0.1.0-rc.1`. Build metadata (`+...`) is not supported.
3. The release workflow reruns CI for that tag, builds every platform, verifies
   the bundled native apps, and creates a **draft** GitHub release. It does not
   publish automatically. Retrying can refresh a draft but cannot overwrite a
   published release.
4. Review the generated notes and download the draft's files:

   ```sh
   gh release download v0.1.0 --dir build/release-review
   cd build/release-review
   shasum -a 256 -c SHA256SUMS
   ```

   On Linux, use `sha256sum -c SHA256SUMS`. Test the downloaded CLI on a clean
   machine and check `dotshift version`, setup, service startup, and shutdown.
5. Publish the reviewed draft in GitHub, then update Homebrew as described below.
   Keep published tags and assets immutable; fixes receive a new version.

Each release contains CLI `.tar.gz` archives for all four platforms, two macOS
app `.zip` archives, a source archive containing the committed dashboard,
`SHA256SUMS`, and (for stable versions) `dotshift.rb`. CLI archives contain the
license and setup documentation. Both CLI and app bundle versions come from
the tag; CLI builds also identify the source commit and its timestamp.

The current app archives are ad-hoc signed test builds. Homebrew compiles and
signs its app locally. Direct macOS app distribution still requires Developer
ID signing and notarization before presenting those archives as normal
downloadable Mac installers. The draft notes state this limitation. The build
script accepts `DOTSHIFT_SIGN_IDENTITY` for signing, but the workflow does not
yet provision a certificate or submit a notarization request.

## Publish the stable Homebrew formula

After publishing the GitHub release, copy its generated formula into the tap:

```sh
gh release download v0.1.0 --pattern dotshift.rb --dir build/homebrew-release --clobber
cp build/homebrew-release/dotshift.rb Formula/dotshift.rb
brew style Formula/dotshift.rb
git add Formula/dotshift.rb
git commit -m "Release Homebrew formula for 0.1.0"
git push origin main
```

The formula pins the uploaded source archive and its SHA-256 checksum. CI
tests the formula against the same committed source using a local archive;
after publication, verify a normal `brew install
gregwhitaker/dotshift/dotshift` from the public tap on a clean machine. Keep the
`head` stanza for development installations. Prerelease drafts deliberately
omit a stable formula so release candidates cannot replace the stable version.

For the first release, also update the Homebrew guide's introductory commands
from the temporary `--HEAD` instructions to the stable installation command.
