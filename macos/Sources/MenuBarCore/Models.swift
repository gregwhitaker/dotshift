import Foundation

public struct LaunchConfiguration: Codable, Equatable, Sendable {
    public let config: String
    public let cli: String
    public let directory: String
    public let path: String

    public init(config: String, cli: String, directory: String, path: String) {
        self.config = config
        self.cli = cli
        self.directory = directory
        self.path = path
    }

    public func validate() throws {
        let files = FileManager.default
        guard [config, cli, directory].allSatisfy({ $0.hasPrefix("/") }),
              files.fileExists(atPath: config), files.isExecutableFile(atPath: cli),
              files.fileExists(atPath: directory) else {
            throw MenuBarError("Choose an existing configuration and a working dotshift executable.")
        }
    }
}

public struct MenuBarError: LocalizedError {
    public let message: String
    public init(_ message: String) { self.message = message }
    public var errorDescription: String? { message }
}

public struct Snapshot: Decodable {
    public let running: Bool
    public let execution_mode: String
    public let mcp_enabled: Bool
    public let config_path: String
    public let jobs: [WorkItem]

    public var active: [WorkItem] { jobs.filter(\.isActive) }
    public var attention: [WorkItem] { jobs.filter(\.needsAttention) }
    public var completed: [WorkItem] {
        jobs.filter { $0.status == "done" && !$0.needsAttention }
            .sorted { ($0.finished_at ?? $0.created_at) > ($1.finished_at ?? $1.created_at) }
    }
}

public struct WorkItem: Decodable {
    public let id: String
    public let repo: String
    public let prompt: String
    public let status: String
    public let test_result: String?
    public let created_at: String
    public let finished_at: String?
    public let goal: Progress?

    public struct Progress: Decodable {
        public let phase: String
        public let model_selection: Selection?
        public let reviewer_selection: Selection?
    }
    public struct Selection: Decodable { public let waiting_until: String? }

    public var isActive: Bool { status == "running" || status == "queued" }
    public var needsAttention: Bool {
        ["blocked", "failed", "waiting_input"].contains(status) || (status == "done" && test_result == "failed")
    }
    public var title: String {
        let line = prompt.trimmingCharacters(in: .whitespacesAndNewlines).components(separatedBy: .newlines).first ?? ""
        return line.isEmpty ? "Untitled task" : line
    }
    public var phase: String {
        if status == "done" && test_result == "failed" { return "Tests failed" }
        if status == "running" {
            let waiting = goal?.phase == "reviewing"
                ? goal?.reviewer_selection?.waiting_until ?? goal?.model_selection?.waiting_until
                : goal?.model_selection?.waiting_until
            if waiting != nil { return "Waiting for model" }
            return ["working": "Implementing", "testing": "Testing", "reviewing": "Reviewing"][goal?.phase ?? ""] ?? "Running"
        }
        return ["queued": "Queued", "blocked": "Needs input", "waiting_input": "Needs input",
                "failed": "Failed", "done": "Complete", "cancelled": "Cancelled"][status] ?? status
    }
    public var route: String {
        let safe = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-_"))
        return "/goals/" + (id.addingPercentEncoding(withAllowedCharacters: safe) ?? "")
    }
    public var menuTitle: String {
        let text = "\(repo) · \(title)"
        return "\(text.count > 58 ? String(text.prefix(55)) + "…" : text) — \(phase)"
    }
}

public struct Connection: Decodable {
    public let name: String
    public let status: String
    public let detail: String?
    public let retry_at: String?
}

public struct WorkEvent {
    public enum Kind { case completed, attention }
    public let kind: Kind
    public let work: WorkItem
}

// Establish a baseline on launch/reconnect; don't notify about historical work.
public struct NotificationTracker {
    private var previous: [String: String]?
    public init() {}
    public mutating func reset() { previous = nil }
    public mutating func changes(in snapshot: Snapshot) -> [WorkEvent] {
        guard snapshot.running else { reset(); return [] }
        var next = [String: String]()
        var events = [WorkEvent]()
        for work in snapshot.jobs {
            let state = work.needsAttention ? "attention:\(work.status)" : work.status
            next[work.id] = state
            guard let previous, previous[work.id] != state else { continue }
            if work.needsAttention { events.append(WorkEvent(kind: .attention, work: work)) }
            else if work.status == "done" { events.append(WorkEvent(kind: .completed, work: work)) }
        }
        previous = next
        return events
    }
}
