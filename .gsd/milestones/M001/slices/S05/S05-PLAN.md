# S05: Conformance Test Suite & Polish

**Goal:** Comprehensive RFC conformance test suite proving all 13 operations, both framing modes, capability negotiation, error handling, session lifecycle, and filter types — end-to-end through the full client↔server stack. Module properly packaged with documentation.
**Demo:** `go test ./... -count=1` passes 180+ tests across 6 packages including the new `netconf/conformance` package; `README.md` exists at the repo root with usage and test instructions.

## Must-Haves

- All 13 RFC 6241 operations exercised end-to-end through typed `client.Client` methods against `server.Server` with mock handlers, in both base:1.0 (EOM) and base:1.1 (chunked) framing modes
- Error propagation chain proven: handler `RPCError` → wire → client `errors.As` extraction; non-RPCError handler error → generic `operation-failed`; unregistered operation → `operation-not-supported`
- Session lifecycle proven: session-id propagation, capability intersection, `CloseSession` terminates Serve, `KillSession` dispatches to handler
- Framing auto-negotiation proven: both-support-11 → chunked, client-only-11 → EOM, server-only-11 → EOM
- Subtree and XPath filter types reach the server handler on the wire
- SSH transport integration: full TCP→SSH→NETCONF stack with GetConfig and CloseSession
- Message-id monotonicity: sequential operations produce distinct, monotonically increasing IDs
- `README.md` with module path, Go version, test instructions, and package overview

## Proof Level

- This slice proves: final-assembly (full client↔server RFC conformance)
- Real runtime required: no (in-process loopback and loopback TCP for SSH)
- Human/UAT required: no

## Verification

- `go test ./netconf/conformance/... -v -count=1` — all conformance tests pass (≥30 test functions including sub-tests)
- `go test ./... -count=1` — full regression across all 6 packages, 180+ tests, 0 failures
- `go vet ./...` — clean, no warnings
- `test -f README.md` — README exists at repo root

## Integration Closure

- Upstream surfaces consumed: `netconf.Session` (Send/Recv), `netconf/client.Client` (all 13 typed methods), `netconf/server.Server` (RegisterHandler, Serve), `netconf/transport.NewLoopback`, `netconf/transport/ssh.Dial` + `ssh.NewListener`, all 13 operation structs, `RPCError`/`ParseRPCErrors`, `Filter`, `DataReply`, `Datastore`, capability constants
- New wiring introduced in this slice: none (test-only; no production code changes)
- What remains before the milestone is truly usable end-to-end: nothing — S05 is the final slice

## Observability / Diagnostics

**Runtime signals:**
- `go test ./netconf/conformance/... -v -count=1` prints one named sub-test per operation/scenario, making individual failures immediately locatable. Example failure output names the sub-test (e.g. `--- FAIL: TestConformance_AllOperations_Base10/edit-config`) so the failing operation is always in the test name, not only the error message.
- `go test ./netconf/conformance/... -run TestConformance_ErrorPropagation -v` isolates the error-propagation path and prints the full `errors.As` chain via testify's `require.True` failure output.
- `go test ./netconf/conformance/... -run TestConformance_SSHTransport -v` exercises the full TCP→SSH stack and prints SSH handshake/dial errors at the transport layer if the stack fails.

**Inspection surfaces:**
- `serverSess.FramingMode()` is asserted directly in `TestConformance_AllOperations_Base11` and `TestConformance_FramingAutoNegotiation` — a wrong framing mode produces an assertion failure naming the expected and actual values.
- `serverSess.SessionID()` is asserted in `TestConformance_SessionLifecycle/session-id-propagation` — mismatch names both expected (42) and actual values.
- Handler-captured `rpc.Body` is compared with `assert.Contains` in `TestConformance_FilterTypes` and `TestConformance_SessionLifecycle/KillSession-dispatches-to-handler` — failure output shows the full body bytes, making wire-level encoding problems visible.
- `waitServe` helper enforces a 2-second deadline; timeout failure prints which test left `Serve` hanging, pointing to a missing `CloseSession` or a deadlock.

**Failure visibility:**
- Serve goroutine errors are captured in buffered `chan error`; any non-nil value causes `require.NoError` to fail with the full error chain.
- SSH dial failures surface via `require.NoError(t, err, "Dial SSH")` — the error includes the address and the failing SSH handshake/subsystem step.
- `TestConformance_MessageIDMonotonicity` prints each bad ID in the assertion failure string (e.g. "message-id[2]=5 must be greater than message-id[1]=5").

**Redaction constraints:**
- RPC bodies in all conformance tests are synthetic stubs (`<config/>`, `<interfaces/>`) with no real device data. Handlers do not log at INFO level. Test output is safe to capture in CI logs.

## Tasks

- [x] **T01: Write the RFC conformance test suite** `est:2h`
  - Why: Directly satisfies R024 — proves RFC compliance for all operations, framing modes, capabilities, errors, and session lifecycle through end-to-end tests
  - Files: `netconf/conformance/conformance_test.go`
  - Do: Create `netconf/conformance/conformance_test.go` (package `conformance_test`) with: (1) `newLoopbackPair` helper parameterised by capability set, creating a `client.Client` ↔ `server.Server` pair over loopback; (2) `newSSHPair` helper for TCP→SSH stack; (3) table-driven `TestConformance_AllOperations_Base10` exercising all 13 ops with EOM framing; (4) `TestConformance_AllOperations_Base11` exercising all 13 ops with chunked framing; (5) `TestConformance_ErrorPropagation` for RPCError/non-RPCError/unknown-op paths; (6) `TestConformance_SessionLifecycle` for session-id, capabilities, CloseSession, KillSession; (7) `TestConformance_FramingAutoNegotiation` for three framing scenarios; (8) `TestConformance_FilterTypes` for subtree and XPath filters; (9) `TestConformance_SSHTransport` for full SSH stack; (10) `TestConformance_MessageIDMonotonicity` for sequential ID ordering. Server sessions use mock `HandlerFunc` handlers. All assertions use `testify/assert` and `testify/require`.
  - Verify: `go test ./netconf/conformance/... -v -count=1` passes all tests; `go test ./... -count=1` full regression passes
  - Done when: ≥30 test functions (including sub-tests) pass; all 13 operations covered in both framing modes; error, lifecycle, framing, filter, SSH, and message-id scenarios all green

- [x] **T02: Add README and run final regression** `est:30m`
  - Why: Module polish — provides discoverable documentation for users and completes the milestone deliverable
  - Files: `README.md`
  - Do: Create `README.md` at repo root with: module path (`github.com/GabrielNunesIT/netconf`), Go version requirement (1.22+), package overview (netconf, netconf/client, netconf/server, netconf/transport, netconf/transport/ssh), how to install, how to run tests (`go test ./...`), brief usage example showing client Dial + GetConfig + CloseSession. Run `go test ./... -count=1` and `go vet ./...` as final regression.
  - Verify: `test -f README.md && go test ./... -count=1 && go vet ./...`
  - Done when: README exists, full test suite passes with 180+ tests across 6 packages, `go vet` clean

## Files Likely Touched

- `netconf/conformance/conformance_test.go` (new)
- `README.md` (new)
