import Foundation
import Testing
@testable import Kith

@Test func byteSizes() {
    #expect(Fmt.bytes(412) == "412 B")
    #expect(Fmt.bytes(38 * 1024) == "38 KB")
    #expect(Fmt.bytes(Int64(1.7 * 1024 * 1024)) == "1.7 MB")
}

@Test func clockFormat() {
    #expect(Fmt.clock(52) == "0:52")
    #expect(Fmt.clock(580) == "9:40")
    #expect(Fmt.clock(3723) == "1:02:03")
}

@Test func pairCode() {
    #expect(Fmt.pairCode("347X0F") == "347 X0F")
    #expect(Fmt.pairCode("347-X0F") == "347 X0F")
}

@Test func daemonTimestamps() {
    #expect(Fmt.date("2026-10-17T14:02:07.123Z") != nil)
    #expect(Fmt.date("2026-10-17T14:02:07Z") != nil)
}

@Test func ipcFinalLines() throws {
    let ok = try IPCMessage(line: Data(#"{"result":{"fp":"ab"},"final":true}"#.utf8))
    guard case .result(let d) = ok else { Issue.record("want result"); return }
    #expect(String(data: d, encoding: .utf8)!.contains("ab"))

    let bad = try IPCMessage(line: Data(#"{"error":{"code":"NOT_FOUND","message":"no task","remediation":"try"},"final":true}"#.utf8))
    guard case .failure(let e) = bad else { Issue.record("want failure"); return }
    #expect(e.code == "NOT_FOUND" && e.remediation == "try")

    let ev = try IPCMessage(line: Data(#"{"event":"code","data":{"kind":"code","code":"347X0F"}}"#.utf8))
    guard case .event(let name, let data) = ev else { Issue.record("want event"); return }
    #expect(name == "code")
    #expect(try IPC.decoder.decode(PairEvent.self, from: data).code == "347X0F")
}

@Test func taskDecoding() throws {
    let json = #"""
    {"id":"t1","role":"executor","peer_fp":"88ee","project":"p","name":"render","state":"SUBMITTED",
     "attempt":1,"pending_approval":true,"created_at":"2026-10-17T14:07:51.000Z","updated_at":"2026-10-17T14:07:51.000Z",
     "log_size":0,"peer_name":"HARI-PC","inputs":[{"path":"a.blend","size":10,"sha256":"x"}],
     "requirements":[{"req":"os = darwin","have":"darwin","status":"pass"}]}
    """#
    let t = try IPC.decoder.decode(KTask.self, from: Data(json.utf8))
    #expect(t.needsApproval)
    #expect(t.inputs?.first?.size == 10)
    #expect(t.requirements?.first?.status == "pass")
}

@Test func cliErrors() {
    let e = KithCLI.parseCLIError("PEER_UNREACHABLE: peer HARI-PC is not connected\nFix: start kith there")
    #expect(e.code == "PEER_UNREACHABLE")
    #expect(e.message == "peer HARI-PC is not connected")
    #expect(e.remediation == "start kith there")
}
