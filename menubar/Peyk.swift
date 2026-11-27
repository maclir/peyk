// The Peyk menu bar app: shows whether the laptop agent is publishing and
// starts, stops or restarts its launchd job.
import AppKit
import SwiftUI

let env = ProcessInfo.processInfo.environment
let agentLabel = "com.peyk.agent"
let home = FileManager.default.homeDirectoryForCurrentUser
let healthURL = home.appendingPathComponent(".peyk/agent.json")
let logURL = home.appendingPathComponent(".peyk/agent.log")
let plistPath = home.appendingPathComponent("Library/LaunchAgents/\(agentLabel).plist").path
let repo = env["PEYK_REPO"]
let domain = "gui/\(getuid())"

enum AgentState {
    case running, unreachable, starting, crashed, stopped

    var word: String {
        switch self {
        case .running: "running"
        case .unreachable: "offline on the phone"
        case .starting: "starting"
        case .crashed: "restarting after a crash"
        case .stopped: "stopped"
        }
    }
}

/// What `peyk check-update` prints: the checkout against its upstream.
struct Version: Decodable {
    let commit: String
    let behind: Int
}

/// Runs this checkout's own peyk binary.
func peyk(_ args: String...) -> (status: Int32, output: Data) {
    guard let repo else { return (-1, Data()) }
    let p = Process()
    p.executableURL = URL(fileURLWithPath: repo).appendingPathComponent("bin/peyk")
    p.arguments = args
    p.currentDirectoryURL = URL(fileURLWithPath: repo)
    let pipe = Pipe()
    p.standardOutput = pipe
    p.standardError = pipe
    do { try p.run() } catch { return (-1, Data("\(error)".utf8)) }
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    p.waitUntilExit()
    return (p.terminationStatus, data)
}

struct Health: Decodable {
    let pid: Int32
    let startedAt: Date
    let refreshedAt: Date?
    let workers: Int
    let unreachable: String?
    let usage: Usage?
    let actions: Actions?

    enum CodingKeys: String, CodingKey {
        case pid, workers, unreachable, usage, actions
        case startedAt = "started_at"
        case refreshedAt = "refreshed_at"
    }
}

struct Window: Decodable {
    let used: Double
    let resetsAt: Date

    enum CodingKeys: String, CodingKey {
        case used
        case resetsAt = "resets_at"
    }
}

struct Usage: Decodable {
    let fiveHour: Window?
    let sevenDay: Window?
    let antigravity: [Pool]?

    enum CodingKeys: String, CodingKey {
        case fiveHour = "five_hour"
        case sevenDay = "seven_day"
        case antigravity
    }
}

/// One Antigravity quota, shared by a group of models such as Gemini.
struct Pool: Decodable {
    let name: String
    let fiveHour: Window?
    let sevenDay: Window?

    enum CodingKeys: String, CodingKey {
        case name
        case fiveHour = "five_hour"
        case sevenDay = "seven_day"
    }
}

struct Actions: Decodable {
    let included: Int
    let used: Int
    let resetsAt: Date

    enum CodingKeys: String, CodingKey {
        case included, used
        case resetsAt = "resets_at"
    }
}

/// One quota as the phone's header bars show it: the percentage left, and
/// the words its menu line says.
struct Meter: Hashable {
    let left: Int
    let line: String
}

/// Claude's 5-hour and 7-day windows, as board.js usageLeft reads them: a
/// window past its reset, or one Claude Code no longer reports, is whole.
func claudeMeters(_ u: Usage?, now: Date) -> [Meter] {
    guard let u, u.fiveHour != nil || u.sevenDay != nil else { return [] }
    return windowMeters("Claude", u.fiveHour, u.sevenDay, now: now)
}

func windowMeters(_ name: String, _ fiveHour: Window?, _ sevenDay: Window?, now: Date) -> [Meter] {
    [("5h", fiveHour), ("7d", sevenDay)].map { label, w in
        guard let w, w.resetsAt > now else { return Meter(left: 100, line: "\(name) \(label): 100% left") }
        let left = max(0, min(100, Int((100 - w.used).rounded())))
        return Meter(left: left, line: "\(name) \(label): \(left)% left · resets in \(ago(w.resetsAt.timeIntervalSince(now)))")
    }
}

/// Each Antigravity pool's two windows, as board.js antigravityLeft reads
/// them: the menu lists every pool, and the icon shows the tightest pool
/// for each window, as board.js tightest picks it.
func antigravityMeters(_ u: Usage?, now: Date) -> (bars: [Meter], lines: [Meter]) {
    let pools = (u?.antigravity ?? []).map { windowMeters("Antigravity \($0.name)", $0.fiveHour, $0.sevenDay, now: now) }
    guard !pools.isEmpty else { return ([], []) }
    let bars = (0..<2).map { i in pools.map { $0[i] }.min { $0.left < $1.left }! }
    return (bars, pools.flatMap { $0 })
}

/// The month's included Actions minutes left, as board.js actionsLeft reads
/// them: a month past its reset is whole again.
func actionsMeter(_ a: Actions?, now: Date) -> Meter? {
    guard let a, a.included > 0 else { return nil }
    let used = a.resetsAt > now ? max(0, min(a.included, a.used)) : 0
    let left = a.included - used
    return Meter(left: Int((100 * Double(left) / Double(a.included)).rounded()),
                 line: "GitHub Actions: \(left.formatted()) of \(a.included.formatted()) minutes left")
}

/// What launchd reports about the agent's job.
struct Job {
    var loaded = false
    var running = false
    var lastExit: Int?
}

func readJob() -> Job {
    let (status, out) = launchctl(["print", "\(domain)/\(agentLabel)"])
    guard status == 0 else { return Job() }
    var job = Job(loaded: true)
    for line in out.split(separator: "\n") {
        let t = line.trimmingCharacters(in: .whitespaces)
        if t == "state = running" { job.running = true }
        if t.hasPrefix("last exit code = ") { job.lastExit = Int(t.dropFirst("last exit code = ".count)) }
    }
    return job
}

func parseDate(_ s: String) -> Date? {
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let d = f.date(from: s) { return d }
    f.formatOptions = [.withInternetDateTime]
    return f.date(from: s)
}

/// The health file names the agent's pid; one left by a killed agent names a
/// dead process and does not count.
func readHealth() -> Health? {
    guard let data = try? Data(contentsOf: healthURL) else { return nil }
    let d = JSONDecoder()
    d.dateDecodingStrategy = .custom { decoder in
        let s = try decoder.singleValueContainer().decode(String.self)
        guard let date = parseDate(s) else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: s))
        }
        return date
    }
    guard let h = try? d.decode(Health.self, from: data), kill(h.pid, 0) == 0 else { return nil }
    return h
}

func lastLogLine() -> String? {
    guard let handle = try? FileHandle(forReadingFrom: logURL) else { return nil }
    defer { try? handle.close() }
    let end = (try? handle.seekToEnd()) ?? 0
    try? handle.seek(toOffset: end > 4096 ? end - 4096 : 0)
    let text = String(decoding: handle.readDataToEndOfFile(), as: UTF8.self)
    return text.split(separator: "\n").last.map(String.init)
}

func ago(_ seconds: TimeInterval) -> String {
    let s = Int(seconds)
    if s < 60 { return "\(s)s" }
    if s < 3600 { return "\(s / 60)m" }
    if s < 86400 { return "\(s / 3600)h \(s % 3600 / 60)m" }
    return "\(s / 86400)d \(s % 86400 / 3600)h"
}

final class Model: ObservableObject {
    @Published var state = AgentState.starting
    @Published var detail: [String] = []
    @Published var claude: [Meter] = []
    @Published var antigravity: (bars: [Meter], lines: [Meter]) = ([], [])
    @Published var actions: Meter?
    /// The last check's answer: nil until checked, then the version or why
    /// the check failed.
    @Published var version: Version?
    @Published var updateNote: String?
    @Published var updating = false
    let origin = env["PEYK_ORIGIN"].flatMap(URL.init(string:))
    private var timer: Timer?

    init() {
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in self?.refresh() }
    }

    func refresh() {
        DispatchQueue.global(qos: .utility).async {
            let job = readJob()
            let health = job.running ? readHealth() : nil
            let now = Date()
            var state: AgentState
            var detail: [String] = []
            switch (job.loaded, job.running, health) {
            case (false, _, _):
                state = .stopped
                detail = ["Starts again at login"]
            case (true, true, let h?):
                state = h.unreachable == nil ? .running : .unreachable
                var line = "Up \(ago(now.timeIntervalSince(h.startedAt)))"
                if let r = h.refreshedAt { line += " · refreshed \(ago(now.timeIntervalSince(r))) ago" }
                detail = [line, "Watching \(h.workers) worker\(h.workers == 1 ? "" : "s")"]
                if let e = h.unreachable { detail.insert("Can't reach Firestore: \(e.prefix(80))", at: 0) }
            case (true, true, nil):
                state = .starting
                detail = ["Connecting to Firestore…"]
            case (true, false, _):
                state = (job.lastExit ?? 0) != 0 ? .crashed : .starting
                if let line = lastLogLine() { detail = [String(line.prefix(80))] }
            }
            let claude = claudeMeters(health?.usage, now: now)
            let antigravity = antigravityMeters(health?.usage, now: now)
            let actions = actionsMeter(health?.actions, now: now)
            DispatchQueue.main.async {
                self.state = state
                self.detail = detail
                self.claude = claude
                self.antigravity = antigravity
                self.actions = actions
            }
        }
    }

    func act(_ args: String...) {
        DispatchQueue.global(qos: .userInitiated).async {
            let (status, out) = launchctl(args)
            if status != 0 { NSLog("launchctl %@: %@", args.joined(separator: " "), out) }
            DispatchQueue.main.async { self.refresh() }
        }
    }

    func checkForUpdates() {
        updateNote = "Checking…"
        DispatchQueue.global(qos: .userInitiated).async {
            let (status, out) = peyk("check-update")
            let v = status == 0 ? try? JSONDecoder().decode(Version.self, from: out) : nil
            DispatchQueue.main.async {
                self.version = v
                if let v {
                    self.updateNote = v.behind == 0 ? "Up to date at \(v.commit)" : nil
                } else {
                    self.updateNote = "Check failed: \(String(decoding: out, as: UTF8.self).prefix(80))"
                }
            }
        }
    }

    /// Fast-forwards and restarts the agent; a change to this app relaunches
    /// it, which ends this process.
    func update() {
        updating = true
        updateNote = "Updating…"
        DispatchQueue.global(qos: .userInitiated).async {
            let (status, out) = peyk("update")
            DispatchQueue.main.async {
                self.updating = false
                self.version = nil
                self.updateNote = status == 0 ? "Updated" : "Update failed: \(String(decoding: out, as: UTF8.self).prefix(80))"
                self.refresh()
            }
        }
    }

    func start() { act("bootstrap", domain, plistPath) }
    func stop() { act("bootout", "\(domain)/\(agentLabel)") }
    func restart() { act("kickstart", "-k", "\(domain)/\(agentLabel)") }
}

func launchctl(_ args: [String]) -> (status: Int32, output: String) {
    let p = Process()
    p.executableURL = URL(fileURLWithPath: "/bin/launchctl")
    p.arguments = args
    let pipe = Pipe()
    p.standardOutput = pipe
    p.standardError = pipe
    do { try p.run() } catch { return (-1, "\(error)") }
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    p.waitUntilExit()
    return (p.terminationStatus, String(decoding: data, as: UTF8.self))
}

/// The Peyk mark, then the phone header's quota bars in one column: Claude
/// Code's two windows, Antigravity's two, and the month's Actions minutes,
/// each group set apart. Each bar fills with what is used. It is a template
/// image so macOS tints it for the menu bar, which leaves no amber or red:
/// a quota running low shows by its bar thinning, as on the phone, to half
/// under 10% left and to a hairline once nothing is.
func icon(_ state: AgentState, claude: [Meter], antigravity: [Meter], actions: Meter?) -> NSImage {
    let slots: [(y: CGFloat, meter: Meter?)] = [
        (1, claude.first), (4, claude.dropFirst().first),
        (8, antigravity.first), (11, antigravity.dropFirst().first),
        (15, actions),
    ]
    let bars: [(y: CGFloat, left: Int)] = slots.compactMap { slot in slot.meter.map { (slot.y, $0.left) } }
    let barX: CGFloat = 21, barWidth: CGFloat = 16
    let width: CGFloat = bars.isEmpty ? 18 : barX + barWidth
    let image = NSImage(size: NSSize(width: width, height: 18), flipped: true) { _ in
        guard let ctx = NSGraphicsContext.current else { return false }
        ctx.saveGraphicsState()
        defer {
            ctx.restoreGraphicsState()
            for bar in bars {
                let thick: CGFloat = bar.left <= 0 ? 0.5 : bar.left < 10 ? 1 : 2
                let y = bar.y + (2 - thick) / 2
                NSColor.black.withAlphaComponent(0.35).setFill()
                NSBezierPath(roundedRect: NSRect(x: barX, y: y, width: barWidth, height: thick), xRadius: thick / 2, yRadius: thick / 2).fill()
                NSColor.black.setFill()
                let used = barWidth * CGFloat(100 - bar.left) / 100
                if used > 0 {
                    NSBezierPath(roundedRect: NSRect(x: barX, y: y, width: used, height: thick), xRadius: thick / 2, yRadius: thick / 2).fill()
                }
            }
        }
        let t = NSAffineTransform()
        t.scale(by: 18.0 / 32.0)
        t.concat()
        let bubble = NSBezierPath(roundedRect: NSRect(x: 3, y: 3, width: 26, height: 21), xRadius: 8, yRadius: 8)
        let tail = NSBezierPath()
        tail.move(to: NSPoint(x: 6.6, y: 21))
        tail.line(to: NSPoint(x: 5.9, y: 27.4))
        tail.line(to: NSPoint(x: 13.4, y: 23.4))
        tail.close()
        let dots = NSBezierPath()
        for (x, y) in [(12.6, 7.9), (19.4, 7.9), (16.0, 13.9)] {
            dots.move(to: NSPoint(x: x, y: y))
            dots.line(to: NSPoint(x: x + 2.7, y: y + 2.7))
            dots.line(to: NSPoint(x: x, y: y + 5.4))
            dots.line(to: NSPoint(x: x - 2.7, y: y + 2.7))
            dots.close()
        }
        NSColor.black.setFill()
        NSColor.black.setStroke()
        bubble.fill()
        tail.fill()
        ctx.compositingOperation = .destinationOut
        switch state {
        case .running:
            dots.fill()
        case .crashed, .unreachable:
            NSBezierPath(rect: NSRect(x: 14.6, y: 7, width: 2.8, height: 8)).fill()
            NSBezierPath(rect: NSRect(x: 14.6, y: 17, width: 2.8, height: 3)).fill()
        case .starting, .stopped:
            NSBezierPath(roundedRect: NSRect(x: 5.2, y: 5.2, width: 21.6, height: 16.6), xRadius: 5.8, yRadius: 5.8).fill()
            ctx.compositingOperation = .sourceOver
            if state == .starting {
                NSColor.black.withAlphaComponent(0.5).setFill()
                dots.fill()
            } else {
                let slash = NSBezierPath()
                slash.move(to: NSPoint(x: 4, y: 3))
                slash.line(to: NSPoint(x: 28, y: 27))
                slash.lineWidth = 2.4
                slash.lineCapStyle = .round
                slash.stroke()
            }
        }
        return true
    }
    image.isTemplate = true
    image.accessibilityDescription = (["Peyk agent \(state.word)"] + (claude + antigravity + (actions.map { [$0] } ?? [])).map(\.line))
        .joined(separator: "; ")
    return image
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
    }
}

@main
struct PeykMenu: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate
    @StateObject private var model = Model()

    var body: some Scene {
        MenuBarExtra {
            Text("Peyk agent — \(model.state.word)")
            ForEach(model.detail, id: \.self) { Text($0) }
            if !model.claude.isEmpty || !model.antigravity.lines.isEmpty || model.actions != nil {
                Divider()
                ForEach(model.claude, id: \.self) { m in
                    Button(m.line) { NSWorkspace.shared.open(URL(string: "https://claude.ai/settings/usage")!) }
                }
                ForEach(model.antigravity.lines, id: \.self) { m in
                    Button(m.line) { NSWorkspace.shared.open(URL(string: "https://antigravity.google/docs/cli/commands/usage")!) }
                }
                if let m = model.actions {
                    Button(m.line) { NSWorkspace.shared.open(URL(string: "https://github.com/settings/billing")!) }
                }
            }
            Divider()
            if model.state == .stopped {
                Button("Start") { model.start() }
            } else {
                Button("Restart") { model.restart() }.keyboardShortcut("r")
                Button("Stop") { model.stop() }
            }
            Divider()
            Button("Open Peyk") {
                if let o = model.origin { NSWorkspace.shared.open(o) }
            }
            .disabled(model.origin == nil)
            Button("Show log") { NSWorkspace.shared.open(logURL) }
            Divider()
            if let v = model.version, v.behind > 0 {
                Button("Update (\(v.behind) new commit\(v.behind == 1 ? "" : "s"))") { model.update() }
                    .disabled(model.updating)
            } else {
                Button("Check for updates") { model.checkForUpdates() }
                    .disabled(repo == nil || model.updating)
            }
            if let note = model.updateNote { Text(note) }
            Divider()
            Button("Quit menu bar app") { NSApp.terminate(nil) }.keyboardShortcut("q")
        } label: {
            Image(nsImage: icon(model.state, claude: model.claude, antigravity: model.antigravity.bars, actions: model.actions))
        }
        .menuBarExtraStyle(.menu)
    }
}
