---
estimated_steps: 5
estimated_files: 1
skills_used:
  - test
---

# T01: Write the RFC conformance test suite

**Slice:** S05 — Conformance Test Suite & Polish
**Milestone:** M001

## Description

Create `netconf/conformance/conformance_test.go` — the comprehensive RFC conformance test suite that proves all 13 RFC 6241 operations, both framing modes, capability negotiation, error handling, session lifecycle, filter types, SSH transport, and message-id monotonicity work end-to-end through the full `client.Client` ↔ `server.Server` stack.

This is the single test file that satisfies R024 ("RFC conformance test suite"). It uses external test package `conformance_test` to keep conformance tests logically separate from the unit tests in each package.

The test file needs two helper functions and 10 test functions (several table-driven with sub-tests).

## Steps

1. **Create the file with package declaration and imports.** Package `conformance_test`. Import: `context`, `crypto/rand`, `crypto/rsa`, `encoding/xml`, `errors`, `net`, `testing`, `time`, `github.com/GabrielNunesIT/netconf`, `github.com/GabrielNunesIT/netconf/client`, `github.com/GabrielNunesIT/netconf/server`, `github.com/GabrielNunesIT/netconf/transport`, `github.com/GabrielNunesIT/netconf/transport/ssh`, `golang.org/x/crypto/ssh`, `github.com/stretchr/testify/assert`, `github.com/stretchr/testify/require`.

2. **Write the `newLoopbackPair` helper** that takes `(t *testing.T, clientCaps, serverCaps netconf.CapabilitySet, sessionID uint32)` and returns `(*client.Client, *server.Server, *netconf.Session, chan error)`. It should:
   - Create a loopback transport pair via `transport.NewLoopback()`
   - Run `ClientSession` and `ServerSession` concurrently in goroutines (required for unbuffered io.Pipe — both hellos must send simultaneously, per L005)
   - Build `*client.Client` via `client.NewClient(clientSess)`
   - Build `*server.Server` via `server.NewServer()`
   - Start `srv.Serve(ctx, serverSess)` in a goroutine, returning its error via a `chan error`
   - Return the client, server (so callers can register handlers before Serve starts — actually, handlers must be registered BEFORE calling Serve, so the helper should return the server and let the caller register handlers, then the caller starts Serve). **Revised approach:** The helper returns `(cli *client.Client, srv *server.Server, serverSess *netconf.Session)`. The caller registers handlers on `srv`, then starts `srv.Serve` in their own goroutine. This gives tests full control. The helper registers `t.Cleanup` for the transports.

3. **Write the SSH helpers** inline: `generateTestSigner(t)` (RSA 2048 key → `gossh.Signer`), `testSSHConfigs(t)` (server accepts any password, client uses "test"/"test"), and `newSSHPair(t, caps)` that builds TCP listener → `ncssh.NewListener` → `ncssh.Dial` → sessions → `client.Client` + server session. These are the same patterns from `client_test.go` (cannot import `_test` packages, so must be inlined — ~30 lines total).

4. **Write the 10 test functions:**

   a. **`TestConformance_AllOperations_Base10`** — Table-driven with 13 sub-tests (one per operation). Capability set: `{BaseCap10}` only (EOM framing). For each operation:
      - Register a mock `HandlerFunc` on the server: data-returning ops (`get`, `get-config`) return `[]byte("<data><config/></data>")`; ok-returning ops return `(nil, nil)`.
      - Call the corresponding typed `client.Client` method.
      - Assert: no error; for data ops, `DataReply` is non-nil and content contains `"config"`.
      - After all sub-tests, call `CloseSession` to terminate Serve cleanly.
      
      The table type:
      ```go
      type opCase struct {
          name    string
          handler server.HandlerFunc
          call    func(ctx context.Context, cli *client.Client) error
      }
      ```
      For data-returning ops, use a wrapper that calls `Get`/`GetConfig`, asserts non-nil `DataReply`, and returns error.

   b. **`TestConformance_AllOperations_Base11`** — Same table as Base10, but capability set includes `{BaseCap10, BaseCap11}`. After session establishment, assert `clientSess.FramingMode() == FramingChunked` (need to expose session — add a `Session()` accessor or check framing indirectly). **Correction:** The `client.Client` does not expose `Session`. We can verify chunked framing by checking the server session's `FramingMode()` which IS accessible from `newLoopbackPair`. The helper returns `serverSess` — assert `serverSess.FramingMode() == netconf.FramingChunked`. Then run the same 13-operation table to prove operations work over chunked framing.

   c. **`TestConformance_ErrorPropagation`** — Three sub-tests:
      - *RPCError from handler:* Register handler returning `netconf.RPCError{Type:"application", Tag:"invalid-value", Severity:"error", Message:"test"}`. Call `GetConfig`. Assert `errors.As(err, &netconf.RPCError{})` succeeds with matching fields.
      - *Non-RPCError from handler:* Register handler returning `fmt.Errorf("boom")`. Call `GetConfig`. Assert `errors.As(err, &netconf.RPCError{})` succeeds and Tag is `"operation-failed"`.
      - *Unregistered operation:* Don't register a handler for `get`. Call `Get`. Assert `errors.As` succeeds and Tag is `"operation-not-supported"`.

   d. **`TestConformance_SessionLifecycle`** — Verify:
      - Server session's `SessionID()` matches what was passed to `ServerSession` (e.g. 42)
      - Client session gets the same session-id (need access — check via `serverSess.SessionID()` and note: client's session-id is verified implicitly since it comes from the server hello)
      - `CloseSession` causes Serve to return nil
      - `KillSession` dispatches to the registered `kill-session` handler and the handler receives the correct session-id value in the RPC body

   e. **`TestConformance_FramingAutoNegotiation`** — Three sub-tests using raw sessions (no client/server — just `ClientSession`/`ServerSession` over loopback):
      - Both support 1.1 → `FramingChunked`
      - Client supports 1.1, server only 1.0 → `FramingEOM`
      - Client only 1.0, server supports 1.1 → `FramingEOM`

   f. **`TestConformance_FilterTypes`** — Two sub-tests:
      - *Subtree:* Register `get-config` handler that inspects `rpc.Body` for `type="subtree"` and `<interfaces/>` content. Call `GetConfig` with subtree filter. Assert handler saw the filter.
      - *XPath:* Register handler that inspects for `type="xpath"` and `select="/interfaces"`. Call `GetConfig` with XPath filter. Assert handler saw the filter.

   g. **`TestConformance_SSHTransport`** — Full TCP→SSH→NETCONF stack: create SSH listener, dial, establish sessions with `{BaseCap10, BaseCap11}` caps, run a `server.Server` on the server session, call `GetConfig` + `CloseSession` via `client.Client`. Assert `DataReply` non-nil and Serve returns nil.

   h. **`TestConformance_MessageIDMonotonicity`** — Drive 5 sequential operations against a server. In the handler, capture the message-id from each RPC. After all 5, assert: all IDs are distinct, all are parseable as integers, and they are monotonically increasing.

5. **Run `go test ./netconf/conformance/... -v -count=1` and `go test ./... -count=1`** to verify all tests pass. Fix any issues. Run `go vet ./...` to confirm no warnings.

## Must-Haves

- [ ] All 13 RFC 6241 operations pass end-to-end in base:1.0 (EOM) mode
- [ ] All 13 RFC 6241 operations pass end-to-end in base:1.1 (chunked) mode
- [ ] Error propagation: RPCError, non-RPCError, and operation-not-supported all verified
- [ ] Session lifecycle: session-id, CloseSession termination, KillSession dispatch verified
- [ ] Framing auto-negotiation: 3 scenarios (both-1.1, client-only-1.1, server-only-1.1) verified
- [ ] Subtree and XPath filter types reach server handler on the wire
- [ ] SSH transport: full stack GetConfig + CloseSession pass
- [ ] Message-id monotonicity: 5 sequential ops produce increasing IDs
- [ ] `go test ./... -count=1` full regression passes (0 failures)
- [ ] `go vet ./...` clean

## Observability Impact

**Signals added by this task:**
- `go test ./netconf/conformance/... -v -count=1` emits one named line per test function and sub-test. The sub-test name is the RFC 6241 operation XML local name (e.g. `TestConformance_AllOperations_Base10/get-config`), so a failure immediately names the failing operation without reading the assertion body.
- `go test ./netconf/conformance/... -run TestConformance_FramingAutoNegotiation -v` prints the framing mode value (EOM=0, Chunked=1) for all three negotiation scenarios — useful for diagnosing capability negotiation regressions.
- `TestConformance_MessageIDMonotonicity` prints the full captured-ID slice when the monotonicity assertion fails, enabling diagnosis of counter-reset or collision bugs.
- `TestConformance_FilterTypes` captures the raw RPC body from the handler and checks it with `assert.Contains` — failure output shows the entire body bytes, making wire-encoding bugs visible without a packet capture.
- `waitServe` enforces a 2-second hard deadline; a timeout failure prints the test name, pointing to a missing CloseSession or a Serve deadlock.

**How a future agent inspects this task:**
- Run `go test ./netconf/conformance/... -v -count=1` — all 50 test runs must show `PASS`.
- Run `go test ./... -count=1` — 6 packages, 0 failures.
- Run `go vet ./...` — no output.
- Count test functions: `grep -c '^func Test' netconf/conformance/conformance_test.go` must return ≥ 10.

**Failure state visibility:**
- If a server handler panics, the panic propagates through `Serve` into the `serveDone` channel; `waitServe` calls `require.NoError`, which prints the panic string in the test failure output.
- If `CloseSession` never unblocks `Serve`, `waitServe` times out after 2 seconds with "Serve did not return within 2 s — possible deadlock or missing close-session".
- SSH dial failures include the address and the failing layer (TCP dial, SSH handshake, subsystem request) in the error string.

## Verification

- `go test ./netconf/conformance/... -v -count=1` — all conformance tests pass
- `go test ./... -count=1` — full regression, all packages pass, 180+ tests
- `go vet ./...` — clean

## Inputs

- `netconf/client/client.go` — Client type with all 13 typed methods (Get, GetConfig, EditConfig, etc.)
- `netconf/server/server.go` — Server type with RegisterHandler, Serve, HandlerFunc
- `netconf/session.go` — Session type with SessionID(), FramingMode(), ClientSession, ServerSession
- `netconf/capability.go` — BaseCap10, BaseCap11, CapabilitySet, NewCapabilitySet
- `netconf/operation.go` — All 13 operation structs, Datastore, Filter, DataReply
- `netconf/errors.go` — RPCError, ParseRPCErrors
- `netconf/transport/loopback.go` — NewLoopback() for in-process transport pairs
- `netconf/transport/ssh/client.go` — Dial for SSH client transport
- `netconf/transport/ssh/server.go` — NewListener, Listener.Accept for SSH server transport
- `netconf/message.go` — RPC, RPCReply, Hello types

## Expected Output

- `netconf/conformance/conformance_test.go` — the complete RFC conformance test suite with ≥30 test functions
