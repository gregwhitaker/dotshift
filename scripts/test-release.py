#!/usr/bin/env python3
"""Check release input validation, source isolation, and artifact completeness."""
import argparse
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

import release


class ReleaseTests(unittest.TestCase):
    def test_version_rejects_invalid_tags_and_paths(self):
        self.assertEqual(release.version("v1.2.3-rc.1"), "1.2.3-rc.1")
        self.assertEqual(release.version("0.1.0"), "0.1.0")
        for invalid in ("main", "v1.2", "1.02.3", "1.2.3-01", "../1.2.3", "1.2.3;echo bad", "1.2.3\n"):
            with self.subTest(tag=invalid), self.assertRaises(argparse.ArgumentTypeError):
                release.version(invalid)

    def test_source_contains_only_committed_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "README.md").write_text("committed\n")
            subprocess.run(["git", "-C", str(root), "add", "README.md"], check=True)
            subprocess.run(["git", "-C", str(root), "-c", "user.name=Release Test", "-c",
                            "user.email=test@example.invalid", "commit", "-qm", "Fixture"], check=True)
            (root / "README.md").write_text("uncommitted\n")
            (root / "dotshift.yaml").write_text("private configuration\n")
            path = release.source("1.2.3", root, root=root)
            with tarfile.open(path) as archive:
                self.assertEqual(archive.getnames(), ["dotshift-1.2.3", "dotshift-1.2.3/README.md"])
                self.assertEqual(archive.extractfile("dotshift-1.2.3/README.md").read(), b"committed\n")

    def assets(self, path, version):
        names = [f"dotshift_{version}_{system}_{arch}.tar.gz" for system, arch in release.PLATFORMS]
        names += [f"Dotshift_{version}_darwin_{arch}.zip" for arch in ("amd64", "arm64")]
        names += [f"dotshift-{version}.tar.gz"]
        for name in names:
            (path / name).write_bytes(name.encode())
        return names

    def test_stable_formula_and_checksums_match_assets(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            names = self.assets(path, "1.2.3")
            template = (release.ROOT / "Formula/dotshift.rb").read_text()
            release.assemble("1.2.3", path, template)
            formula = (path / "dotshift.rb").read_text()
            self.assertIn('/releases/download/v1.2.3/dotshift-1.2.3.tar.gz"', formula)
            self.assertIn(f'sha256 "{release.sha256(path / "dotshift-1.2.3.tar.gz")}"', formula)
            self.assertIn('head "https://github.com/gregwhitaker/dotshift.git"', formula)
            lines = (path / "SHA256SUMS").read_text().splitlines()
            self.assertEqual(len(lines), len(names) + 1)
            for line in lines:
                digest, name = line.split("  ")
                self.assertEqual(digest, release.sha256(path / name))
            updated = release.homebrew_formula(formula, "1.2.4", "https://example.invalid/dotshift-1.2.4.tar.gz", "a" * 64)
            self.assertEqual(updated.count("\n  url "), 1)
            self.assertNotIn("/releases/download/v1.2.3/", updated)

    def test_prerelease_does_not_replace_stable_formula(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            self.assets(path, "1.2.3-rc.1")
            release.assemble("1.2.3-rc.1", path, "")
            self.assertFalse((path / "dotshift.rb").exists())

    def test_missing_or_extra_platform_artifact_prevents_assembly(self):
        for extra in (False, True):
            with self.subTest(extra=extra), tempfile.TemporaryDirectory() as directory:
                path = Path(directory)
                names = self.assets(path, "1.2.3")
                if extra:
                    (path / "unexpected.tar.gz").write_bytes(b"stale artifact")
                else:
                    (path / names[0]).unlink()
                with self.assertRaisesRegex(ValueError, "incomplete release"):
                    release.assemble("1.2.3", path, "")
                self.assertFalse((path / "SHA256SUMS").exists())


if __name__ == "__main__":
    unittest.main()
