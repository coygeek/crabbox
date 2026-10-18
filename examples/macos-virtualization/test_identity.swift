// Pure identity records only: this executable makes no application or AppleEvent calls.
import Foundation

@main struct IdentityChecks {
    static func rejected(_ body: () throws -> Void) {
        do {
            try body()
            fatalError("An invalid process identity was accepted")
        } catch { /* Expected fail-closed result. */ }
    }

    static func main() throws {
        let app = URL(fileURLWithPath: "/tmp/fixture-built/UTM.app")
        let executable = app.appendingPathComponent("Contents/MacOS/UTM").resolvingSymlinksInPath()
        let installed = URL(fileURLWithPath: "/Applications/UTM.app/Contents/MacOS/UTM")
        func record(_ pid: pid_t = 1234, _ time: Double? = 100.25,
                    _ path: URL? = executable, _ terminated: Bool = false) -> ProcessIdentity {
            ProcessIdentity(pid: pid, launchTime: time, executable: path,
                            bundleIdentifier: "com.utmapp.UTM", terminated: terminated)
        }
        let identity = try Identity.discover(app: app, processes: [record()])
        precondition(identity.pid == 1234 && identity.launchTime == 100.25)
        try identity.verify(processes: [record()])
        rejected { try identity.verify(processes: []) }
        rejected { try identity.verify(processes: [record(1234, 100.25, executable, true)]) }
        rejected { try identity.verify(processes: [record(1234, 100.25, installed)]) }
        rejected { try identity.verify(processes: [record(1234, 999.0)]) }
        rejected { try identity.verify(processes: [record(9999)]) }
        rejected { try identity.verify(processes: [record(1234, nil)]) }
        rejected { _ = try Identity.discover(app: app, processes: [record(1234, 100.25, installed)]) }
        rejected { _ = try Identity.discover(app: app, processes: [record(), record(5678)]) }
        let competing = [record(), record(5678, 200.0, installed)]
        rejected { try identity.verify(processes: competing) }
        // Cleanup may address the same verified built PID despite a competing copy;
        // it must still reject a reused PID, changed launch time or wrong executable.
        try identity.verify(processes: competing, exclusive: false)
        rejected { try identity.verify(processes: [record(1234, 100.25, installed)], exclusive: false) }
        rejected { try identity.verify(processes: [record(1234, 999.0)], exclusive: false) }
        print("invalid process identities rejected; exact PID cleanup identity preserved")
    }
}
