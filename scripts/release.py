#!/usr/bin/env python3
"""Build release archives and the matching Homebrew formula without publishing."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = [("linux", "amd64"), ("linux", "arm64"), ("darwin", "amd64"), ("darwin", "arm64")]
REPOSITORY = "https://github.com/gregwhitaker/dotshift"


def version(value):
    value = value.removeprefix("v")
    number = r"(?:0|[1-9][0-9]*)"
    identifier = r"(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
    if not re.fullmatch(rf"{number}\.{number}\.{number}(?:-{identifier}(?:\.{identifier})*)?", value):
        raise argparse.ArgumentTypeError("expected vMAJOR.MINOR.PATCH, optionally with a prerelease such as -rc.1")
    return value


def run(*args, cwd=ROOT, env=None):
    subprocess.run(args, cwd=cwd, env=env, check=True)


def output(*args, cwd=ROOT):
    return subprocess.check_output(args, cwd=cwd, text=True).strip()


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def homebrew_formula(template, release_version, source_url, checksum):
    # Replace a previous stable release while preserving the development HEAD.
    template = re.sub(r'^  (?:url|sha256) .*\n', '', template, flags=re.MULTILINE)
    marker = f'  homepage "{REPOSITORY}"\n'
    if template.count(marker) != 1:
        raise ValueError("Homebrew template is missing its expected homepage")
    if not re.fullmatch(r"[a-f0-9]{64}", checksum):
        raise ValueError("invalid source checksum")
    version(release_version)
    return template.replace(marker, marker + f"  url {json.dumps(source_url)}\n  sha256 \"{checksum}\"\n")


def build(release_version, target_os, arch, destination):
    if (target_os, arch) not in PLATFORMS:
        raise ValueError("unsupported release platform")
    host = (output("go", "env", "GOHOSTOS"), output("go", "env", "GOHOSTARCH"))
    if target_os == "darwin" and host != (target_os, arch):
        raise ValueError("macOS app releases must be built on a Mac of the matching architecture")
    slug = f"dotshift_{release_version}_{target_os}_{arch}"
    stage = ROOT / "build" / "release-stage" / slug
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir(parents=True)
    commit = output("git", "rev-parse", "HEAD")
    date = output("git", "show", "-s", "--format=%cI", "HEAD")
    ldflags = f"-s -w -X main.version={release_version} -X main.commit={commit} -X main.buildDate={date}"
    env = {**os.environ, "CGO_ENABLED": "0", "GOOS": target_os, "GOARCH": arch}
    cli = stage / "dotshift"
    run("go", "build", "-trimpath", "-ldflags", ldflags, "-o", str(cli), "./cmd/dotshift", env=env)
    if host == (target_os, arch):
        reported = output(str(cli), "version")
        if not reported.startswith(f"dotshift {release_version} (commit {commit},"):
            raise ValueError(f"incorrect binary version: {reported}")
        run(str(cli), "help")
    for name in ("LICENSE", "README.md"):
        shutil.copy2(ROOT / name, stage / name)
    shutil.copytree(ROOT / "docs", stage / "docs")
    (stage / "brand/svg").mkdir(parents=True)
    for name in ("dotshift-logo-dark.svg", "dotshift-logo-light.svg"):
        shutil.copy2(ROOT / "brand/svg" / name, stage / "brand/svg" / name)
    with tarfile.open(destination / f"{slug}.tar.gz", "w:gz") as archive:
        archive.add(stage, arcname=slug)
    if target_os == "darwin":
        run("bash", "scripts/build-menubar.sh", env={**os.environ,
            "DOTSHIFT_CLI_PATH": str(cli), "DOTSHIFT_VERSION": release_version})
        app = ROOT / "build" / "Dotshift.app"
        run("/usr/bin/codesign", "--verify", "--deep", "--strict", str(app))
        run("/usr/bin/ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", str(app),
            str(destination / f"Dotshift_{release_version}_{target_os}_{arch}.zip"))


def source(release_version, destination, root=ROOT):
    # Archive committed files only, including the prebuilt dashboard; never
    # package local configurations, credentials, worktrees, or build output.
    path = destination / f"dotshift-{release_version}.tar.gz"
    run("git", "archive", "--format=tar.gz", f"--prefix=dotshift-{release_version}/",
        f"--output={path}", "HEAD", cwd=root)
    return path


def assemble(release_version, destination, template):
    expected = {f"dotshift_{release_version}_{system}_{arch}.tar.gz" for system, arch in PLATFORMS}
    expected |= {f"Dotshift_{release_version}_darwin_{arch}.zip" for arch in ("amd64", "arm64")}
    source_name = f"dotshift-{release_version}.tar.gz"
    expected.add(source_name)
    actual = {path.name for path in destination.iterdir()}
    if actual != expected:
        raise ValueError(f"incomplete release: missing={sorted(expected - actual)}, unexpected={sorted(actual - expected)}")
    for name in expected:
        if not (destination / name).is_file() or (destination / name).is_symlink():
            raise ValueError(f"release asset is not a regular file: {name}")
    if "-" not in release_version:
        url = f"{REPOSITORY}/releases/download/v{release_version}/{source_name}"
        formula = homebrew_formula(template, release_version, url, sha256(destination / source_name))
        (destination / "dotshift.rb").write_text(formula)
        expected.add("dotshift.rb")
    checksums = "".join(f"{sha256(destination / name)}  {name}\n" for name in sorted(expected))
    (destination / "SHA256SUMS").write_text(checksums)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("version", "build", "source", "assemble"):
        command = commands.add_parser(name)
        command.add_argument("version", type=version)
        if name != "version":
            command.add_argument("--output", type=Path, default=ROOT / "build" / "release")
        if name == "build":
            command.add_argument("--os", choices=("linux", "darwin"), required=True)
            command.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    args = parser.parse_args()
    if args.command == "version":
        print(args.version)
        return
    destination = args.output.resolve()
    destination.mkdir(parents=True, exist_ok=True)
    if args.command == "build":
        build(args.version, args.os, args.arch, destination)
    elif args.command == "source":
        source(args.version, destination)
    else:
        assemble(args.version, destination, (ROOT / "Formula/dotshift.rb").read_text())


if __name__ == "__main__":
    main()
