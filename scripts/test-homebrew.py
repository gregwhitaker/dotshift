#!/usr/bin/env python3
"""Install and test the committed source through an isolated local Homebrew tap."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile

import release


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--skip-dependencies", action="store_true",
                        help="developer smoke test using existing dependencies; CI never uses this option")
    args = parser.parse_args()
    env = {**os.environ, "HOMEBREW_NO_AUTO_UPDATE": "1", "HOMEBREW_NO_ANALYTICS": "1",
           "HOMEBREW_NO_INSTALL_CLEANUP": "1", "HOMEBREW_NO_AUTOREMOVE": "1",
           "HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK": "1"}

    def brew(*args, check=True):
        return subprocess.run(["brew", *args], env=env, check=check)

    installed = subprocess.run(["brew", "list", "--versions", "dotshift"], env=env, capture_output=True, text=True)
    if installed.stdout.strip():
        raise SystemExit("Use a clean runner: this check will not replace an existing Dotshift installation.")
    formula = "dotshift-ci/check/dotshift"
    with tempfile.TemporaryDirectory(prefix="dotshift-homebrew-") as directory:
        temporary = Path(directory).resolve()
        archive = release.source("0.0.0", temporary)
        tap = temporary / "tap"
        (tap / "Formula").mkdir(parents=True)
        template = (release.ROOT / "Formula/dotshift.rb").read_text()
        (tap / "Formula/dotshift.rb").write_text(release.homebrew_formula(
            template, "0.0.0", archive.as_uri(), release.sha256(archive)))
        release.run("git", "init", "-q", "-b", "main", str(tap))
        release.run("git", "add", "Formula/dotshift.rb", cwd=tap)
        release.run("git", "-c", "user.name=Dotshift CI", "-c", "user.email=ci@example.invalid",
                    "commit", "-qm", "Local package test", cwd=tap)
        brew("tap", "dotshift-ci/check", tap.as_uri())
        try:
            # A normal dependency-resolving install verifies fresh-runner setup.
            options = ["--ignore-dependencies"] if args.skip_dependencies else []
            brew("install", "--no-ask", "--skip-link", *options, formula)
            brew("test", "--force", formula)
            brew("audit", "--strict", "--skip-style", formula)
            brew("style", str(release.ROOT / "Formula/dotshift.rb"))
        finally:
            brew("uninstall", formula, check=False)
            brew("untrust", "--formula", formula, check=False)
            brew("untap", "dotshift-ci/check", check=False)


if __name__ == "__main__":
    main()
