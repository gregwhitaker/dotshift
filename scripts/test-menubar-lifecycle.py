#!/usr/bin/env python3
"""Quit the real native app while a gated, fake-agent goal is still running.

Requires a macOS desktop session and a built app. Uses a temporary app identity,
repository, config, service, and fake CLI; makes no model or tunnel requests.
"""
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]
APP = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else ROOT / "build/Dotshift.app"


def run(*args, cwd=None, timeout=30):
    result = subprocess.run(args, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{Path(args[0]).name} failed: {result.stderr.strip()}")
    return result.stdout


def wait_for(predicate, timeout=25):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(0.1)
    raise AssertionError("Timed out waiting for the lifecycle test condition")


def quit_app(identifier):
    # NSRunningApplication.terminate sends the normal macOS Quit event. This
    # exercises applicationShouldTerminate, not SIGKILL or a simulated teardown.
    script = """
import AppKit
let apps = NSRunningApplication.runningApplications(withBundleIdentifier: CommandLine.arguments[1])
guard let app = apps.first else { exit(0) }
guard app.terminate() else { exit(2) }
let deadline = Date().addingTimeInterval(10)
while !app.isTerminated && Date() < deadline {
    RunLoop.current.run(until: Date().addingTimeInterval(0.1))
}
exit(app.isTerminated ? 0 : 3)
"""
    run("/usr/bin/swift", "-e", script, identifier)


def main():
    if sys.platform != "darwin" or not APP.is_dir():
        raise SystemExit("Build the macOS app with make menubar first.")
    os.environ.setdefault("CLANG_MODULE_CACHE_PATH", str(ROOT / "build/module-cache"))
    os.environ.setdefault("SWIFTPM_MODULECACHE_OVERRIDE", str(ROOT / "build/module-cache"))
    identifier = "com.dotshift.menubar.test." + uuid.uuid4().hex
    with tempfile.TemporaryDirectory(prefix="dotshift-menubar-") as temporary:
        directory = Path(temporary).resolve()
        app = directory / "Dotshift Lifecycle.app"
        shutil.copytree(APP, app)
        plist = app / "Contents/Info.plist"
        info = plistlib.loads(plist.read_bytes())
        info["CFBundleIdentifier"] = identifier
        info["CFBundleName"] = "Dotshift Lifecycle Test"
        plist.write_bytes(plistlib.dumps(info))
        run("/usr/bin/codesign", "--force", "--sign", "-", str(app))
        cli = app / "Contents/Resources/dotshift"
        repo = directory / "repo"
        repo.mkdir()
        run("git", "init", "-q", str(repo))
        run("git", "config", "user.name", "Dotshift Test", cwd=repo)
        run("git", "config", "user.email", "test@example.invalid", cwd=repo)
        (repo / "README.md").write_text("Lifecycle fixture\n")
        run("git", "add", "README.md", cwd=repo)
        run("git", "commit", "-qm", "Fixture", cwd=repo)
        started = directory / "agent-started"
        gate = directory / "release-agent"
        fake = directory / "fake-codex"
        # Quote paths for the shell separately from the YAML/JSON encodings.
        import shlex
        fake.write_text("""#!/bin/sh
set -eu
case "$*" in
  *--help*) echo '--ignore-user-config --json'; exit 0;;
  *login\\ status*) echo 'Logged in using ChatGPT'; exit 0;;
esac
cat >/dev/null
last=''
readonly=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message) last="$2"; shift;;
    --sandbox) if [ "$2" = read-only ]; then readonly=true; fi; shift;;
  esac
  shift
done
if [ "$readonly" = false ]; then
  touch STARTED
  while [ ! -f GATE ]; do sleep 0.1; done
  printf 'finished after menu quit\\n' > result.txt
fi
printf '%s\\n' '{"status":"complete","summary":"Lifecycle passed","next_steps":""}' > "$last"
echo '{"type":"turn.completed"}'
""".replace("STARTED", shlex.quote(str(started))).replace("GATE", shlex.quote(str(gate))))
        fake.chmod(0o700)
        config = directory / "dotshift.yaml"
        config.write_text(f"""aliases:
  smart: [openai/default]
repos:
  fixture:
    path: {json.dumps(str(repo))}
    test: test -f result.txt
jobs:
  mode: subscription
  model: smart
  reviewer_model: auto
  timeout: 2m
  agent:
    command: {json.dumps(str(fake))}
mcp:
  disabled: true
""")
        receipt = directory / ".dotshift/service.json"
        service = None
        try:
            run(str(cli), "menubar", "-config", str(config), "-app", str(app), cwd=directory)
            wait_for(receipt.exists)
            service = json.loads(receipt.read_text())
            client = urllib.request.build_opener(urllib.request.ProxyHandler({}))

            def request(path, body=None):
                payload = json.dumps(body).encode() if body is not None else None
                req = urllib.request.Request(service["url"] + "/api" + path, data=payload, headers={
                    "Authorization": "Bearer " + service["token"], "Content-Type": "application/json",
                })
                with client.open(req, timeout=3) as response:
                    return json.load(response)

            job = request("/goals", {"repo": "fixture", "prompt": "Create result.txt after the test releases the gate", "max_iterations": 2})
            wait_for(started.exists)
            assert request("/jobs/" + job["id"])["status"] == "running"
            quit_app(identifier)
            after = json.loads(receipt.read_text())
            assert after["pid"] == service["pid"] and after["instance"] == service["instance"], "Quit replaced or stopped the service"
            assert request("/jobs/" + job["id"])["status"] == "running", "Quit interrupted active work"
            gate.touch()
            result = wait_for(lambda: (data if (data := request("/jobs/" + job["id"]))["status"] in ("done", "failed", "blocked") else None))
            assert result["status"] == "done" and result["test_result"] == "passed", result.get("error")
            assert result["goal"]["reviewer_selection"], "Goal did not complete independent review"
            print("PASS: native Quit left the same service and active goal running; the goal then passed tests and review.")
        finally:
            gate.touch()
            try:
                quit_app(identifier)
            finally:
                try:
                    if receipt.exists():
                        run(str(cli), "stop", "-config", str(config), cwd=directory, timeout=60)
                finally:
                    # Remove only this test's unique preference domain.
                    subprocess.run(["/usr/bin/defaults", "delete", identifier], capture_output=True)


if __name__ == "__main__":
    main()
