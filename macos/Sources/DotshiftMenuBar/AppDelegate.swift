import AppKit
import MenuBarCore
import ServiceManagement
import UserNotifications

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate, UNUserNotificationCenterDelegate {
    private var statusItem: NSStatusItem!
    private let menu = NSMenu()
    private var timer: Timer?
    private var configuration: LaunchConfiguration?
    private var snapshot: Snapshot?
    private var connections: [Connection] = []
    private var connectionsDate: Date?
    private var connectionError: String?
    private var errorMessage: String?
    private var busy: String?
    private var refreshing = false
    private var checkingConnections = false
    private var menuOpen = false
    private var ready = false
    private var pendingConfiguration: LaunchConfiguration?
    private var generation = UUID()
    private var tracker = NotificationTracker()
    private var settingsWindow: NSWindow?
    private var loginButton: NSButton?
    private var completionButton: NSButton?
    private var attentionButton: NSButton?
    private var configurationLabel: NSTextField?
    private let defaults = UserDefaults.standard

    func applicationDidFinishLaunching(_ notification: Notification) {
        ready = true
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.menu = menu
        menu.delegate = self
        menu.autoenablesItems = false
        UNUserNotificationCenter.current().delegate = self
        rebuildMenu()
        timer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
        // Finder may deliver the launch document immediately after this event.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.25) { [weak self] in
            guard let self, self.configuration == nil else { return }
            if let pending = self.pendingConfiguration { self.configure(pending); return }
            if let data = self.defaults.data(forKey: "launchConfiguration"),
               let saved = try? JSONDecoder().decode(LaunchConfiguration.self, from: data) {
                self.configure(saved)
            } else { self.chooseConfiguration(nil) }
        }
    }

    func application(_ sender: NSApplication, openFiles filenames: [String]) {
        do {
            guard let filename = filenames.first, URL(fileURLWithPath: filename).lastPathComponent == "menubar-launch.json" else {
                throw MenuBarError("Launch Dotshift with `dotshift menubar`, or choose a configuration in Settings.")
            }
            let data = try Data(contentsOf: URL(fileURLWithPath: filename))
            let next = try JSONDecoder().decode(LaunchConfiguration.self, from: data)
            try next.validate()
            if ready { configure(next) } else { pendingConfiguration = next }
            sender.reply(toOpenOrPrint: .success)
        } catch {
            sender.reply(toOpenOrPrint: .failure)
            showError(error)
        }
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        // Quit means only this menu app. Never stop the service, unregister its
        // LaunchAgent, cancel work, or signal any CLI/service process here.
        .terminateNow
    }

    func applicationWillTerminate(_ notification: Notification) { timer?.invalidate() }

    private func configure(_ next: LaunchConfiguration) {
        guard busy == nil else { showError(MenuBarError("Wait for the current service action to finish before changing configuration.")); return }
        do { try next.validate() } catch { showError(error); return }
        if configuration == next { refresh(); return }
        generation = UUID()
        configuration = next
        defaults.set(try? JSONEncoder().encode(next), forKey: "launchConfiguration")
        snapshot = nil
        connections = []
        connectionsDate = nil
        connectionError = nil
        errorMessage = nil
        refreshing = false
        checkingConnections = false
        tracker.reset()
        updateSettings()
        perform(["ui", "-no-open"], label: "Starting…", timeout: 270)
    }

    private func refresh() {
        guard let configuration, !refreshing, busy == nil else { return }
        refreshing = true
        let requestGeneration = generation
        Task {
            do {
                let data = try await CommandRunner.run(configuration, ["desktop", "status"], timeout: 15)
                let next = try JSONDecoder().decode(Snapshot.self, from: data)
                guard generation == requestGeneration else { return }
                snapshot = next
                errorMessage = nil
                for event in tracker.changes(in: next) { notify(event) }
            } catch {
                guard generation == requestGeneration else { return }
                snapshot = nil
                tracker.reset()
                errorMessage = error.localizedDescription
            }
            refreshing = false
            rebuildMenu()
            if connectionsDate == nil || Date().timeIntervalSince(connectionsDate!) > 60 { refreshConnections() }
        }
    }

    private func refreshConnections() {
        guard let configuration, !checkingConnections else { return }
        checkingConnections = true
        let requestGeneration = generation
        Task {
            do {
                let data = try await CommandRunner.run(configuration, ["desktop", "connections"], timeout: 45)
                let next = try JSONDecoder().decode([Connection].self, from: data)
                guard generation == requestGeneration else { return }
                connections = next
                connectionError = nil
            } catch {
                guard generation == requestGeneration else { return }
                connections = []
                connectionError = error.localizedDescription
            }
            checkingConnections = false
            connectionsDate = Date()
            rebuildMenu()
        }
    }

    private func perform(_ arguments: [String], label: String, timeout: TimeInterval = 270) {
        guard let configuration, busy == nil else { return }
        generation = UUID()
        refreshing = false
        checkingConnections = false
        busy = label
        rebuildMenu()
        Task {
            do { _ = try await CommandRunner.run(configuration, arguments, timeout: timeout) }
            catch { errorMessage = error.localizedDescription; showError(error) }
            busy = nil
            connectionsDate = nil
            refresh()
            rebuildMenu()
        }
    }

    func menuWillOpen(_ menu: NSMenu) { menuOpen = true; refresh() }
    func menuDidClose(_ menu: NSMenu) {
        menuOpen = false
        DispatchQueue.main.async { [weak self] in self?.rebuildMenu() }
    }

    private func item(_ title: String, action: Selector? = nil, value: Any? = nil, enabled: Bool = true, key: String = "") -> NSMenuItem {
        let entry = NSMenuItem(title: title, action: action, keyEquivalent: key)
        entry.target = self
        entry.representedObject = value
        entry.isEnabled = enabled && action != nil
        return entry
    }

    private func rebuildMenu() {
        let attention = snapshot?.attention.count ?? 0
        let state = busy ?? (errorMessage != nil ? "Unavailable" : configuration == nil ? "Choose configuration" : snapshot == nil ? "Connecting…" : snapshot!.running ? "Running" : "Stopped")
        statusItem.button?.image = menuIcon(attention: attention > 0 || errorMessage != nil)
        statusItem.button?.toolTip = "dotshift · \(state)\n\(snapshot?.active.count ?? 0) active · \(attention) need attention"
        statusItem.button?.setAccessibilityLabel("dotshift, \(state), \(attention) need attention")
        guard !menuOpen else { return }
        menu.removeAllItems()
        let title = item("dotshift · \(state)")
        title.attributedTitle = NSAttributedString(string: title.title, attributes: [.font: NSFont.boldSystemFont(ofSize: 13)])
        menu.addItem(title)
        menu.addItem(item(snapshot?.running == true ? "\(snapshot!.active.count) active · \(attention) need attention" : "Background work runs independently"))
        if errorMessage != nil { menu.addItem(item("Show Connection Error…", action: #selector(showLastError))) }
        menu.addItem(.separator())
        let usable = configuration != nil && busy == nil
        menu.addItem(item("Open Dashboard…", action: #selector(openRoute), value: "/", enabled: usable, key: "o"))
        menu.addItem(item("Models…", action: #selector(openRoute), value: "/models", enabled: usable))
        menu.addItem(.separator())
        addWorkMenu("Needs Attention (\(attention))", work: snapshot?.attention ?? [], empty: "Nothing needs attention", filter: "attention")
        addWorkMenu("Active Work (\(snapshot?.active.count ?? 0))", work: snapshot?.active ?? [], empty: "No active work", filter: "active")
        addWorkMenu("Recent Completions", work: snapshot?.completed ?? [], empty: "No completed work", filter: "done")
        menu.addItem(.separator())
        let connectionsMenu = NSMenu()
        if connections.isEmpty { connectionsMenu.addItem(item(connectionError == nil ? "Checking connections…" : "Could not check connections", action: connectionError == nil ? nil : #selector(showConnectionError))) }
        for connection in connections {
            let entry = item("\(connection.name) · \(connection.status)", action: #selector(showConnection), value: connection)
            entry.toolTip = connection.detail
            connectionsMenu.addItem(entry)
        }
        if let checked = connectionsDate { connectionsMenu.addItem(item("Checked \(checked.formatted(date: .omitted, time: .shortened))")) }
        connectionsMenu.addItem(.separator())
        connectionsMenu.addItem(item(checkingConnections ? "Refreshing…" : "Refresh Connections", action: #selector(checkConnections), enabled: configuration != nil && !checkingConnections))
        addSubmenu("Connections", connectionsMenu)
        let service = NSMenu()
        service.addItem(item("Start Service", action: #selector(startService), enabled: usable && snapshot?.running != true))
        service.addItem(item("Stop Service", action: #selector(stopService), enabled: usable && snapshot?.running == true))
        service.addItem(item("Restart Service", action: #selector(restartService), enabled: usable))
        service.addItem(.separator())
        service.addItem(item("View Service Log…", action: #selector(openLog), enabled: configuration != nil))
        addSubmenu("Service", service)
        menu.addItem(item("Settings…", action: #selector(showSettings), key: ","))
        menu.addItem(.separator())
        let quit = item("Quit Menu Bar App", action: #selector(quitApp), key: "q")
        quit.toolTip = "Closes this menu app. The background service and its work keep running."
        menu.addItem(quit)
    }

    private func addSubmenu(_ title: String, _ submenu: NSMenu) {
        submenu.autoenablesItems = false
        let entry = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        entry.submenu = submenu
        entry.isEnabled = true
        menu.addItem(entry)
    }

    private func addWorkMenu(_ title: String, work: [WorkItem], empty: String, filter: String) {
        let submenu = NSMenu()
        if work.isEmpty { submenu.addItem(item(snapshot?.running == true ? empty : "Connect to the service to see work")) }
        for task in work.prefix(5) {
            let entry = item(task.menuTitle, action: #selector(openRoute), value: task.route, enabled: busy == nil)
            entry.toolTip = "\(task.repo)\n\(task.title)\n\(task.phase)"
            submenu.addItem(entry)
        }
        submenu.addItem(.separator())
        submenu.addItem(item("View All…", action: #selector(openRoute), value: "/?filter=\(filter)", enabled: configuration != nil && busy == nil))
        addSubmenu(title, submenu)
    }

    @objc private func openRoute(_ sender: NSMenuItem) {
        guard let route = sender.representedObject as? String else { return }
        perform(["ui", "-path", route], label: "Opening…")
    }
    @objc private func startService(_ sender: Any?) { perform(["ui", "-no-open"], label: "Starting…") }
    @objc private func stopService(_ sender: Any?) { perform(["stop"], label: "Stopping…", timeout: 110) }
    @objc private func restartService(_ sender: Any?) { perform(["desktop", "restart"], label: "Restarting…", timeout: 380) }
    @objc private func checkConnections(_ sender: Any?) { refreshConnections() }
    @objc private func quitApp(_ sender: Any?) { NSApp.terminate(nil) }
    @objc private func showLastError(_ sender: Any?) { showError(MenuBarError(errorMessage ?? "No connection error.")) }
    @objc private func showConnectionError(_ sender: Any?) { showError(MenuBarError(connectionError ?? "No connection error.")) }

    @objc private func showConnection(_ sender: NSMenuItem) {
        guard let connection = sender.representedObject as? Connection else { return }
        var detail = connection.detail ?? ""
        if let retry = connection.retry_at {
            let date = ISO8601DateFormatter().date(from: retry)
            detail += "\nRetry after \(date?.formatted(date: .abbreviated, time: .shortened) ?? retry)."
        }
        if connection.name == "Codex" || connection.name == "Claude" {
            detail += "\nThis checks CLI sign-in. Remaining subscription allowance is not available."
        }
        alert("\(connection.name) · \(connection.status)", detail)
    }

    @objc private func openLog(_ sender: Any?) {
        guard let configuration else { return }
        let url = URL(fileURLWithPath: configuration.config).deletingLastPathComponent().appendingPathComponent(".dotshift/service.log")
        guard FileManager.default.fileExists(atPath: url.path) else { showError(MenuBarError("The service has not created a log yet.")); return }
        NSWorkspace.shared.open(url)
    }

    private func showError(_ error: Error) { alert("dotshift", error.localizedDescription) }
    private func alert(_ title: String, _ detail: String) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = detail
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }

    // Draw the five equal circles of the Offset mark as a template, so macOS supplies the
    // proper color in light/dark menu bars. A smaller corner dot signals attention
    // without changing the mark or shifting neighboring menu-bar items.
    private func menuIcon(attention: Bool) -> NSImage {
        let image = NSImage(size: NSSize(width: 20, height: 20), flipped: false) { _ in
            NSColor.black.set()
            // SVG coordinates use a top-left origin; AppKit's unflipped image uses bottom-left.
            for (x, y) in [(13.0, 31.0), (33.0, 31.0), (13.0, 51.0), (33.0, 51.0), (49.0, 15.0)] {
                NSBezierPath(ovalIn: NSRect(x: 1 + (x-9)*0.28, y: 1 + (64-y-9)*0.28, width: 5.04, height: 5.04)).fill()
            }
            if attention {
                NSBezierPath(ovalIn: NSRect(x: 15.75, y: 2.75, width: 3.5, height: 3.5)).fill()
            }
            return true
        }
        image.isTemplate = true
        return image
    }

    @objc private func showSettings(_ sender: Any?) {
        if settingsWindow == nil {
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 470, height: 330), styleMask: [.titled, .closable], backing: .buffered, defer: false)
            window.title = "dotshift Settings"
            window.isReleasedWhenClosed = false
            let stack = NSStackView()
            stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 14
            stack.translatesAutoresizingMaskIntoConstraints = false
            let title = NSTextField(labelWithString: "dotshift")
            title.font = .boldSystemFont(ofSize: 20)
            stack.addArrangedSubview(title)
            loginButton = NSButton(checkboxWithTitle: "Launch menu bar app at login", target: self, action: #selector(toggleLogin))
            completionButton = NSButton(checkboxWithTitle: "Notify when work completes", target: self, action: #selector(toggleNotifications))
            attentionButton = NSButton(checkboxWithTitle: "Notify when work needs attention", target: self, action: #selector(toggleNotifications))
            completionButton?.identifier = NSUserInterfaceItemIdentifier("notifyCompleted")
            attentionButton?.identifier = NSUserInterfaceItemIdentifier("notifyAttention")
            for button in [loginButton!, completionButton!, attentionButton!] { stack.addArrangedSubview(button) }
            configurationLabel = NSTextField(wrappingLabelWithString: "")
            configurationLabel?.font = .systemFont(ofSize: 11); configurationLabel?.textColor = .secondaryLabelColor
            stack.addArrangedSubview(configurationLabel!)
            let buttons = NSStackView(views: [NSButton(title: "Choose Configuration…", target: self, action: #selector(chooseConfiguration)), NSButton(title: "Open Configuration…", target: self, action: #selector(openConfiguration))])
            buttons.spacing = 8; stack.addArrangedSubview(buttons)
            let note = NSTextField(wrappingLabelWithString: "Quitting the menu bar app leaves background work running. Use Service → Stop Service to stop it explicitly.")
            note.font = .systemFont(ofSize: 12); note.textColor = .secondaryLabelColor
            stack.addArrangedSubview(note)
            window.contentView!.addSubview(stack)
            NSLayoutConstraint.activate([stack.leadingAnchor.constraint(equalTo: window.contentView!.leadingAnchor, constant: 24), stack.trailingAnchor.constraint(equalTo: window.contentView!.trailingAnchor, constant: -24), stack.topAnchor.constraint(equalTo: window.contentView!.topAnchor, constant: 22)])
            window.center()
            settingsWindow = window
        }
        updateSettings()
        settingsWindow?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func updateSettings() {
        let status = SMAppService.mainApp.status
        loginButton?.state = status == .enabled ? .on : status == .requiresApproval ? .mixed : .off
        loginButton?.title = status == .requiresApproval ? "Launch at login (approve in System Settings)" : "Launch menu bar app at login"
        completionButton?.state = defaults.bool(forKey: "notifyCompleted") ? .on : .off
        attentionButton?.state = defaults.bool(forKey: "notifyAttention") ? .on : .off
        configurationLabel?.stringValue = configuration?.config ?? "Choose your dotshift.yaml configuration to get started."
        configurationLabel?.toolTip = configuration?.config
    }

    @objc private func toggleLogin(_ sender: NSButton) {
        do {
            switch SMAppService.mainApp.status {
            case .enabled: try SMAppService.mainApp.unregister()
            case .requiresApproval: SMAppService.openSystemSettingsLoginItems()
            default: try SMAppService.mainApp.register()
            }
            if SMAppService.mainApp.status == .requiresApproval { SMAppService.openSystemSettingsLoginItems() }
        } catch { showError(error) }
        updateSettings()
    }

    @objc private func toggleNotifications(_ sender: NSButton) {
        guard let key = sender.identifier?.rawValue else { return }
        if sender.state == .off { defaults.set(false, forKey: key); return }
        Task {
            do {
                let allowed = try await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound])
                defaults.set(allowed, forKey: key)
                if !allowed { alert("Notifications are disabled", "Allow Dotshift in System Settings → Notifications to enable this option.") }
            } catch { defaults.set(false, forKey: key); showError(error) }
            updateSettings()
        }
    }

    @objc private func chooseConfiguration(_ sender: Any?) {
        guard busy == nil else { showError(MenuBarError("Wait for the current service action to finish.")); return }
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSOpenPanel()
        panel.title = "Choose your dotshift.yaml configuration"
        panel.canChooseDirectories = false; panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let selected = panel.url else { return }
        let cli = Bundle.main.resourceURL!.appendingPathComponent("dotshift").path
        let path = configuration?.path ?? ((ProcessInfo.processInfo.environment["PATH"] ?? "") + ":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
        let url = selected.resolvingSymlinksInPath()
        configure(LaunchConfiguration(config: url.path, cli: cli, directory: url.deletingLastPathComponent().path, path: path))
    }

    @objc private func openConfiguration(_ sender: Any?) {
        guard let configuration else { chooseConfiguration(sender); return }
        NSWorkspace.shared.open(URL(fileURLWithPath: configuration.config))
    }

    private func notify(_ event: WorkEvent) {
        let key = event.kind == .completed ? "notifyCompleted" : "notifyAttention"
        guard defaults.bool(forKey: key), let configuration else { return }
        let content = UNMutableNotificationContent()
        content.title = event.kind == .completed ? "Work completed · \(event.work.repo)" : "Needs attention · \(event.work.repo)"
        content.body = event.work.title
        content.sound = .default
        content.userInfo = ["route": event.work.route, "config": configuration.config]
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: event.work.id + key, content: content, trigger: nil))
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) { completionHandler([.banner, .sound]) }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
        let info = response.notification.request.content.userInfo
        Task { @MainActor in
            if let config = info["config"] as? String, config == configuration?.config, let route = info["route"] as? String {
                perform(["ui", "-path", route], label: "Opening…")
            }
            completionHandler()
        }
    }
}
