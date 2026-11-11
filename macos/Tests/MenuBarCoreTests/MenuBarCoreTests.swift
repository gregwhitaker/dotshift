import XCTest
@testable import MenuBarCore

final class MenuBarCoreTests: XCTestCase {
    private func snapshot(_ jobs: [[String: Any]], running: Bool = true) throws -> Snapshot {
        let data = try JSONSerialization.data(withJSONObject: ["running": running, "execution_mode": "subscription", "mcp_enabled": false, "config_path": "/tmp/dotshift.yaml", "jobs": jobs])
        return try JSONDecoder().decode(Snapshot.self, from: data)
    }

    private func job(_ id: String = "one", status: String = "running", test: String = "", phase: String = "working") -> [String: Any] {
        ["id": id, "repo": "project", "prompt": "Build search\nAcceptance criteria", "status": status, "test_result": test, "created_at": "2026-11-09T10:00:00Z", "goal": ["phase": phase]]
    }

    func testWorkGroupsAndPhasesMatchDashboard() throws {
        let state = try snapshot([job(), job("queued", status: "queued"), job("blocked", status: "blocked"), job("failed", status: "failed"), job("input", status: "waiting_input"), job("bad-tests", status: "done", test: "failed"), job("done", status: "done"), job("cancelled", status: "cancelled")])
        XCTAssertEqual(state.active.count, 2)
        XCTAssertEqual(state.attention.count, 4)
        XCTAssertEqual(state.completed.map(\.id), ["done"])
        XCTAssertEqual(state.active.first?.phase, "Implementing")
        XCTAssertEqual(state.attention.last?.phase, "Tests failed")
        XCTAssertEqual(state.active.first?.title, "Build search")
    }

    func testReviewCooldownAndTerminalStatus() throws {
        var work = job(phase: "reviewing")
        work["goal"] = ["phase": "reviewing", "reviewer_selection": ["waiting_until": "2026-11-09T12:00:00Z"]]
        XCTAssertEqual(try snapshot([work]).jobs[0].phase, "Waiting for model")
        work["status"] = "blocked"
        XCTAssertEqual(try snapshot([work]).jobs[0].phase, "Needs input")
    }

    func testNotificationTransitionsDoNotReplayHistoryOrCallFailedTestsComplete() throws {
        var tracker = NotificationTracker()
        XCTAssertTrue(tracker.changes(in: try snapshot([job(), job("old", status: "done")])).isEmpty)
        let completed = try snapshot([job(status: "done"), job("old", status: "done")])
        let event = tracker.changes(in: completed)
        XCTAssertEqual(event.count, 1)
        XCTAssertEqual(event[0].kind, .completed)
        XCTAssertTrue(tracker.changes(in: completed).isEmpty)
        let failedTests = tracker.changes(in: try snapshot([job(status: "done", test: "failed")]))
        XCTAssertEqual(failedTests.count, 1)
        XCTAssertEqual(failedTests[0].kind, .attention)
        XCTAssertTrue(tracker.changes(in: try snapshot([], running: false)).isEmpty)
        XCTAssertTrue(tracker.changes(in: completed).isEmpty)
    }

    func testJobDeepLinksEscapePathAndQueryCharacters() throws {
        let state = try snapshot([job("a/b?filter=all#token")])
        XCTAssertEqual(state.jobs[0].route, "/goals/a%2Fb%3Ffilter%3Dall%23token")
    }

    func testCompletionsAreSortedByFinishTime() throws {
        var old = job("old", status: "done")
        old["finished_at"] = "2026-11-09T10:00:00Z"
        var recent = job("recent", status: "done")
        recent["finished_at"] = "2026-11-09T11:00:00Z"
        XCTAssertEqual(try snapshot([old, recent]).completed.map(\.id), ["recent", "old"])
    }

    func testCommandRunnerDrainsLargeResponsesAndPreservesArguments() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let executable = directory.appendingPathComponent("fake cli")
        try "#!/bin/sh\n/usr/bin/head -c 131072 /dev/zero\nprintf '%s\\n' \"$@\"\n".write(to: executable, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: executable.path)
        let config = LaunchConfiguration(config: directory.appendingPathComponent("space config.yaml").path, cli: executable.path, directory: directory.path, path: "/usr/bin:/bin")
        let output = try await CommandRunner.run(config, ["ui", "-path", "/projects?new=1"], timeout: 5)
        XCTAssertGreaterThan(output.count, 131072)
        XCTAssertTrue(String(decoding: output.suffix(300), as: UTF8.self).contains("/projects?new=1\n-config\n" + config.config))
    }
}
