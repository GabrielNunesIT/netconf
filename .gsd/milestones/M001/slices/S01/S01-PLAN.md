# S01: Transport & Session Foundation

**Goal:** Client and server establish SSH connection, exchange hello messages with capability negotiation, and communicate using both base:1.0 and base:1.1 framing — proven by loopback and SSH tests.
**Demo:** `go test ./...` passes — tests prove hello exchange with namespace-qualified capability URNs round-trips, both framers encode/decode correctly with edge cases, and SSH client↔server transports establish a NETCONF subsystem channel with auto-negotiated framing.

## Must-Haves

- Go module initialized at `github.com/GabrielNunesIT/netconf` with approved dependencies only (D007)
- XML message types for `<hello>`, `<rpc>`, `<rpc-reply>` that marshal/unmarshal with correct NETCONF namespaces
- Capability URNs follow RFC 7803 IANA registry format (R003)
- `Transport` interface with `MsgReader()`/`MsgWriter()` returning `io.ReadCloser`/`io.WriteCloser`
- Base:1.0 end-of-message framer (`]]>]]>` delimiter)
- Base:1.1 chunked framer (RFC 6242 §4.2 ABNF)
- Framer `Upgrade()` method for post-hello framing switch
- In-process loopback transport for tests (io.Pipe-based)
- `Session` type with hello exchange, capability negotiation, session-id assignment, framing auto-negotiation
- SSH client transport — opens NETCONF subsystem channel
- SSH server transport — accepts NETCONF subsystem requests
- Integration test proving SSH client↔server hello exchange with auto-negotiated framing

## Proof Level

- This slice proves: integration
- Real runtime required: no (in-process loopback and in-process SSH)
- Human/UAT required: no

## Verification

- `cd /home/user/repos/netconf/.gsd/worktrees/M001 && go test ./... -v -count=1` — all tests pass
- `netconf/message_test.go` — hello/rpc/rpc-reply XML round-trip with namespace verification
- `netconf/capability_test.go` — RFC 7803 URN format validation, base capability constants
- `netconf/transport/framer_test.go` — both framers encode/decode, edge cases (empty message, max chunk, EOM in content), Upgrade() switches mode
- `netconf/transport/loopback_test.go` — loopback transport round-trips messages
- `netconf/session_test.go` — hello exchange, capability negotiation, session-id, framing auto-negotiation over loopback
- `netconf/transport/ssh/ssh_test.go` — SSH client↔server subsystem channel establishment with hello exchange

## Observability / Diagnostics

- **Structured errors:** All protocol-layer errors (XML decode failures, unknown namespaces, missing capabilities) are returned as typed Go errors with context strings; `errors.As` can extract them in tests and in higher-level agents.
- **Failure inspection surfaces:**
  - `go test ./... -v` prints per-test PASS/FAIL with full error values and stack if `-v` is set.
  - `go vet ./...` surfaces type-level issues in message and capability definitions.
  - Test helper `ValidateURN` is callable from diagnostic scripts to check capability lists read from live devices.
- **Transport failure visibility:** A closed or errored Transport returns non-nil errors from `MsgReader`/`MsgWriter`; callers log them before tearing down the session. No silent swallowing of I/O errors.
- **Redaction:** No secrets pass through this layer; capability URNs and session-ids are safe to log verbatim.
- **Diagnostic failure check:** `go test ./netconf/... -run TestValidateURN_RejectsMalformed -v` exercises the malformed-URN error path and prints the exact rejection message for each bad input — usable as a smoke-test for the validation logic.

## Integration Closure

- Upstream surfaces consumed: none (first slice)
- New wiring introduced in this slice: `Transport` interface, `Session` type, SSH transports, framers — all boundary contracts consumed by S02–S04
- What remains before the milestone is truly usable end-to-end: S02 (operations), S03 (client API), S04 (server API), S05 (conformance)

## Tasks

- [x] **T01: Initialize Go module and define core message and capability types** `est:45m`
  - Why: Everything depends on the module, XML message types, capability URNs, and the Transport interface. This task establishes the type foundation and proves XML namespace handling works (retired risk from research).
  - Files: `go.mod`, `netconf/message.go`, `netconf/message_test.go`, `netconf/capability.go`, `netconf/capability_test.go`, `netconf/transport/transport.go`
  - Do: Initialize go.mod at `github.com/GabrielNunesIT/netconf` with Go 1.22+. Define Hello, RPC, RPCReply XML structs with `urn:ietf:params:xml:ns:netconf:base:1.0` namespace. Define Capability type with RFC 7803 URN validation and base capability constants (`urn:ietf:params:netconf:base:1.0`, `urn:ietf:params:netconf:base:1.1`). Define Transport interface with `MsgReader()/MsgWriter()` returning `io.ReadCloser/io.WriteCloser` plus `Close()`. Write tests proving XML round-trip preserves namespaces and capability URN format is validated.
  - Verify: `go test ./netconf/... -v -count=1`
  - Done when: go.mod exists, message types marshal/unmarshal with correct namespaces, capability URNs are RFC 7803 compliant, Transport interface is defined.

- [x] **T02: Implement base:1.0 and base:1.1 framers with loopback transport** `est:1h`
  - Why: Both framing modes are required for RFC 6242 compliance (R002). The loopback transport enables all subsequent testing without SSH.
  - Files: `netconf/transport/framer.go`, `netconf/transport/framer_test.go`, `netconf/transport/loopback.go`, `netconf/transport/loopback_test.go`
  - Do: Implement EOM framer (write `]]>]]>` after each message, detect delimiter on read). Implement chunked framer (RFC 6242 §4.2: `\n#<size>\n<data>\n##\n` encoding, parse chunk headers on read with size validation). Add `Upgrade()` method to switch from EOM→chunked post-hello. Build loopback transport using `io.Pipe` that satisfies the `Transport` interface with configurable framing mode. Test edge cases: empty message, message containing `]]>]]>` literal, maximum chunk size boundary, multi-chunk messages, zero-length chunks (illegal), malformed chunk headers.
  - Verify: `go test ./netconf/transport/... -v -count=1`
  - Done when: Both framers pass encoding/decoding tests including edge cases, loopback transport round-trips messages through both framing modes.

- [x] **T03: Implement session hello exchange and capability negotiation over loopback** `est:45m`
  - Why: Session lifecycle is the foundation all operations build on (R006). Hello exchange with capability negotiation and framing auto-negotiation is the first real protocol interaction.
  - Files: `netconf/session.go`, `netconf/session_test.go`
  - Do: Implement Session type holding session-id, local/remote capabilities, and current framing mode. On session open: send local hello with capabilities, receive remote hello, validate capabilities, extract session-id (server assigns, client receives), determine framing mode (base:1.1 if both sides support it, otherwise base:1.0), call `Upgrade()` if switching to chunked. Session struct must be usable from both client and server perspectives. Tests use loopback transport: verify hello round-trip, session-id propagation, capability intersection logic, framing auto-negotiation (both 1.1, only 1.0, mixed), rejection of hello without base:1.0 capability.
  - Verify: `go test ./netconf/... -v -count=1 -run TestSession`
  - Done when: Session correctly exchanges hello messages, negotiates capabilities, assigns session-id, and auto-selects framing mode — all proven over loopback.

- [x] **T04: Implement SSH client and server transports with integration test** `est:1h`
  - Why: SSH is the mandatory transport (R002). This task wires SSH subsystem channels as Transport implementations and proves the full protocol flow end-to-end.
  - Files: `netconf/transport/ssh/client.go`, `netconf/transport/ssh/server.go`, `netconf/transport/ssh/ssh_test.go`, `go.mod`, `go.sum`
  - Do: Implement SSH client transport: dial SSH server, open "netconf" subsystem channel, wrap channel as Transport with EOM framer (pre-hello). Implement SSH server transport: accept SSH connections, handle "netconf" subsystem requests, wrap channel as Transport. Use `golang.org/x/crypto/ssh` with in-process `net.Pipe()` for tests (no real TCP). Integration test: start SSH server goroutine, client connects, both sides run Session hello exchange, verify capabilities negotiated and framing upgraded. Test both scenarios: both peers support base:1.1 (should upgrade to chunked) and server only supports base:1.0 (should stay EOM).
  - Verify: `go test ./netconf/transport/ssh/... -v -count=1`
  - Done when: SSH client and server transports work, integration test proves full hello exchange with auto-negotiated framing over SSH subsystem channels.

## Files Likely Touched

- `go.mod`
- `go.sum`
- `netconf/message.go`
- `netconf/message_test.go`
- `netconf/capability.go`
- `netconf/capability_test.go`
- `netconf/transport/transport.go`
- `netconf/transport/framer.go`
- `netconf/transport/framer_test.go`
- `netconf/transport/loopback.go`
- `netconf/transport/loopback_test.go`
- `netconf/session.go`
- `netconf/session_test.go`
- `netconf/transport/ssh/client.go`
- `netconf/transport/ssh/server.go`
- `netconf/transport/ssh/ssh_test.go`
