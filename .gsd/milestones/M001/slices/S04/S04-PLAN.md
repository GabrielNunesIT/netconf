# S04: Server Library & Handler Dispatch

**Goal:** A `netconf/server` package where implementers register per-operation handler callbacks. The library handles session lifecycle, framing, capability exchange, and operation dispatch — proven by loopback tests with mock handlers and client↔server integration tests.
**Demo:** `go test ./netconf/server/... -v -count=1` shows dispatch to registered handlers, unknown-operation error replies, rpc-error propagation, close-session termination, and a full client↔server round-trip using typed `client.Client` methods against the server.

## Must-Haves

- `Handler` interface with `Handle(ctx, *Session, *RPC) ([]byte, error)` signature
- `Server` struct with handler registration keyed by operation XML element name
- `Serve(ctx, *Session)` dispatch loop: Recv → unmarshal RPC → extract operation name → dispatch → marshal reply → Send
- Built-in `close-session` interception: sends `<ok/>` and returns (does not delegate to user handlers)
- Default `operation-not-supported` rpc-error for unregistered operations
- Handler returning `RPCError` produces a well-formed `<rpc-error>` reply
- Handler returning `nil` body produces `<ok/>` reply
- Handler returning non-nil body embeds it as `<rpc-reply>` inner content
- Client↔server integration test proving typed `client.Client` methods work against the server

## Proof Level

- This slice proves: integration (server dispatches real RPCs from a real client)
- Real runtime required: no (in-process loopback)
- Human/UAT required: no

## Verification

```bash
go test ./netconf/server/... -v -count=1   # all server tests PASS
go test ./... -count=1                      # full suite regression: all 132 prior + new tests PASS
go vet ./...                                # clean
```

Specific test functions that prove R005:
- `TestServer_DispatchesToRegisteredHandler` — operation name routes to correct handler
- `TestServer_UnknownOperation_ReturnsError` — unregistered operation gets `operation-not-supported`
- `TestServer_HandlerRPCError_PropagatesAsReply` — handler returning RPCError produces well-formed `<rpc-error>` reply
- `TestServer_CloseSession_TerminatesServeLoop` — `<close-session>` causes Serve to return cleanly after sending `<ok/>`
- `TestServer_WithClient` — full loopback: `client.Client` calls typed methods against a `Server` with mock handlers, replies match

Observability diagnostic check:
```bash
go test ./netconf/server/... -run TestServer_UnknownOperation -v -count=1  # error message names the operation
```

## Observability / Diagnostics

- Runtime signals: `Serve` returns a descriptive error when the transport fails or ctx is cancelled; handler errors wrap the operation name and message-id
- Inspection surfaces: `go test ./netconf/server/... -run TestServer -v` — test names encode the exact scenario
- Failure visibility: error messages include operation name, message-id, and handler identity so callers can log them verbatim
- Redaction constraints: RPC body may contain device configuration; log at DEBUG only in production

## Integration Closure

- Upstream surfaces consumed: `netconf.Session` (Send/Recv), `netconf.RPC` / `netconf.RPCReply` (message types), `netconf.RPCError` (error model), `transport.NewLoopback` (test transport), `netconf.ClientSession` / `netconf.ServerSession` (session establishment), `netconf/client.Client` (integration test driver)
- New wiring introduced in this slice: `netconf/server` package with Handler interface and Server dispatch loop
- What remains before the milestone is truly usable end-to-end: S05 (conformance test suite & polish) — integration of all packages into a comprehensive test suite

## Tasks

- [x] **T01: Implement server package with Handler interface, Server struct, and Serve dispatch loop** `est:1h`
  - Why: This is the core deliverable of S04 — the server library that R005 requires. Implements the Handler interface, Server struct with handler registration, Serve dispatch loop, built-in close-session handling, and default operation-not-supported error replies.
  - Files: `netconf/server/server.go`, `netconf/server/server_test.go`
  - Do: Create `netconf/server/server.go` with Handler interface, Server struct, NewServer constructor, RegisterHandler, and Serve loop. Serve reads RPCs via Session.Recv, extracts the operation name from RPC.Body using xml.Decoder (Local name only, not qualified — consistent with P009), intercepts close-session (sends ok, returns nil), looks up handler by operation name, calls handler, converts result to RPCReply (nil body → ok; RPCError → marshalled rpc-error body; []byte → body content), sends reply via Session.Send. Write unit tests proving dispatch, unknown-operation error, RPCError propagation, close-session termination, and ok/data reply paths. Use loopback transport + ClientSession/ServerSession pair pattern from client_test.go.
  - Verify: `go test ./netconf/server/... -v -count=1 && go test ./... -count=1 && go vet ./...`
  - Done when: All server unit tests pass, full suite regression passes (132 prior + new), go vet clean

- [x] **T02: Add client↔server integration tests with typed Client methods** `est:45m`
  - Why: Proves the server works end-to-end with the real client library — typed `client.Client` methods driving RPCs against the server with mock handlers. This is the integration closure that validates R005 fully.
  - Files: `netconf/server/server_test.go`
  - Do: Add TestServer_WithClient that creates a loopback pair, establishes client/server sessions concurrently, starts a Server with mock handlers for get-config (returns DataReply body) and edit-config (returns ok), runs Serve in a goroutine, creates a client.Client, calls GetConfig/EditConfig/CloseSession typed methods, asserts replies match. Add TestServer_WithClient_RPCError that registers a handler returning RPCError, calls a typed method, asserts errors.As extracts the RPCError fields. Optionally add TestServer_ContextCancel proving ctx cancellation terminates Serve.
  - Verify: `go test ./netconf/server/... -v -count=1 && go test ./... -count=1 && go vet ./...`
  - Done when: Integration tests pass proving client↔server round-trip with typed methods, full suite regression passes, go vet clean

## Files Likely Touched

- `netconf/server/server.go` (new)
- `netconf/server/server_test.go` (new)
