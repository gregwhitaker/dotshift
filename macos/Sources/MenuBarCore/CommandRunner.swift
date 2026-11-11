import Foundation
import Darwin

public enum CommandRunner {
    // A CLI invocation is a short-lived client. The CLI itself detaches service
    // startup. There is deliberately no shutdown/deinit hook or process-group
    // cancellation tied to the menu application's lifetime.
    public static func run(_ configuration: LaunchConfiguration, _ arguments: [String], timeout: TimeInterval = 30) async throws -> Data {
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .utility).async {
                do {
                    let process = Process()
                    process.executableURL = URL(fileURLWithPath: configuration.cli)
                    process.arguments = arguments + ["-config", configuration.config]
                    process.currentDirectoryURL = URL(fileURLWithPath: configuration.directory)
                    var environment = ProcessInfo.processInfo.environment
                    environment["PATH"] = configuration.path
                    process.environment = environment
                    process.standardInput = FileHandle.nullDevice
                    let pipe = Pipe()
                    process.standardOutput = pipe
                    process.standardError = pipe
                    try process.run()
                    let deadline = DispatchWorkItem {
                        if process.isRunning {
                            process.terminate()
                            DispatchQueue.global().asyncAfter(deadline: .now() + 3) {
                                if process.isRunning { kill(process.processIdentifier, SIGKILL) }
                            }
                        }
                    }
                    DispatchQueue.global().asyncAfter(deadline: .now() + timeout, execute: deadline)
                    // Drain before waitUntilExit so a large task list cannot fill
                    // the pipe and deadlock both processes.
                    let output = pipe.fileHandleForReading.readDataToEndOfFile()
                    process.waitUntilExit()
                    deadline.cancel()
                    guard process.terminationStatus == 0 else {
                        let message = String(data: output, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                        throw MenuBarError(message.isEmpty ? "The dotshift command stopped or timed out. View the service log for details." : message)
                    }
                    continuation.resume(returning: output)
                } catch { continuation.resume(throwing: error) }
            }
        }
    }
}
