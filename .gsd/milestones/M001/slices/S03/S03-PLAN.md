# S03: Client Library API

**Goal:** Idiomatic Go client API with context support, concurrent RPC message-id matching, and clean error types — all 13 base operations callable through typed methods.
**Demo:** `go test ./netconf/client/... -v -count=1` passes ≥15 tests covering concurrent RPCs, context cancellation, all 13 typed operation round-trips, and full SSH loopback integration.

## Must-Haves

- `Session.Send([]byte) error` and `Session.Recv() ([]byte, error)` methods expose the transport without leaking the unexported `trp` field
- `Client` struct with a background dispatcher goroutine that multiplexes `<rpc-reply>` messages to waiting callers by `message-id`
- `Do(ctx, op) (*RPCReply, error)` core primitive: marshals operation into `<rpc>`, assigns unique `message-id`, writes to transport, waits for matching reply or context cancellation
- 13 typed methods (`Get`, `GetConfig`, `EditConfig`, `CopyConfig`, `DeleteConfig`, `Lock`, `Unlock`, `CloseSession`, `KillSession`, `Validate`, `Commit`, `DiscardChanges`, `CancelCommit`) that delegate to `Do` and decode replies
- Concurrent RPC support: multiple goroutines can call `Do` simultaneously; out-of-order replies are matched correctly
- Context cancellation: `Do` returns `context.Canceled` or `context.DeadlineExceeded` when the caller's context is cancelled
- Clean shutdown via `Client.Close()` that stops the dispatcher goroutine
- No new dependencies outside the approved set (K001 / D007)

## Proof Level

- This slice proves: integration (client API over real SSH transport with loopback)
- Real runtime required: no (in-process loopback)
- Human/UAT required: no

## Verification

- `go test ./netconf/client/... -v -count=1` — all client tests pass (≥15 tests)
- `go test ./netconf/... -v -count=1` — all prior tests still pass (92 regression)
- `go vet ./...` — clean
- `go test ./netconf/client/... -run TestClient_ConcurrentRPCs -v -count=1` — proves concurrent message-id matching
- `go test ./netconf/client/... -run TestClient_ContextCancel -v -count=1` — proves context cancellation
- `go test ./netconf/client/... -run TestClient_SSHLoopback -v -count=1` — proves full SSH integration
- `go test ./netconf/client/... -run TestClient_TransportClose -v -count=1` — proves dispatcher exits and pending callers receive transport error (inspectable failure state via `Client.Err()`)

## Observability / Diagnostics

- Runtime signals: `Client.Do` errors include message-id and operation context; dispatcher errors surface via `Client.Err()` or through pending callers
- Inspection surfaces: `go test ./netconf/client/... -v` prints per-test PASS/FAIL with full error context
- Failure visibility: dispatcher goroutine exits on transport read error; all pending callers receive the transport error; `Client.Close()` is idempotent
- Redaction constraints: RPCReply.Body may contain device config — same constraint as S02

## Integration Closure

- Upstream surfaces consumed: `netconf/session.go` (Session type + Send/Recv), `netconf/message.go` (RPC/RPCReply), `netconf/operation.go` (13 op structs, Filter, DataReply), `netconf/errors.go` (ParseRPCErrors), `netconf/transport/ssh/client.go` (Dial), `netconf/transport/loopback.go` (NewLoopback)
- New wiring introduced in this slice: `netconf/client/` package — the integration layer that composes Session + operations + error handling
- What remains before the milestone is truly usable end-to-end: S04 (server handler dispatch), S05 (conformance suite)

## Tasks

- [x] **T01: Build Client with concurrent RPC dispatcher and Do primitive** `est:1h`
  - Why: The dispatcher is the core concurrency mechanism — one reader goroutine multiplexes replies to callers by message-id. All typed methods depend on `Do`. Session needs `Send`/`Recv` first.
  - Files: `netconf/session.go`, `netconf/client/client.go`, `netconf/client/client_test.go`
  - Do: (1) Add `Send([]byte) error` and `Recv() ([]byte, error)` to Session, wrapping `transport.WriteMsg`/`ReadMsg`. (2) Create `netconf/client/` package with `Client` struct: `*Session`, `atomic.Uint64` for message-id, `sync.Mutex`-guarded `map[string]chan rpcResult`, done channel, background dispatcher goroutine. (3) Implement `NewClient(session)` (test constructor), `Do(ctx, any) (*RPCReply, error)`, `Close()`, `Err()`. (4) The dispatcher goroutine calls `session.Recv()` in a loop, unmarshals `RPCReply`, looks up pending by `message-id`, sends result on buffered channel. On transport error, drains all pending callers with the error. (5) `Do` marshals the operation via `xml.Marshal`, embeds it in `RPC{MessageID, Body}`, marshals the RPC, calls `session.Send`, registers a buffered `chan rpcResult`, then selects on reply or context cancellation. On cancel, removes pending entry but uses buffered channel so dispatcher doesn't block. (6) Test concurrency: two goroutines call `Do`, server-side echoes replies out of order, both get correct replies. (7) Test context cancel: caller cancels before reply arrives, `Do` returns `context.Canceled`. (8) Test transport close: dispatcher exits, pending callers get error. (9) No new dependencies.
  - Verify: `go test ./netconf/client/... -v -count=1 && go test ./netconf/... -v -count=1 && go vet ./...`
  - Done when: `Do` works with concurrent callers, context cancellation, and clean shutdown — ≥6 tests pass in `client_test.go`, 92 prior tests still pass

- [x] **T02: Add 13 typed operation methods and SSH loopback integration tests** `est:1h`
  - Why: Typed methods are the user-facing API (R004). SSH integration proves the full stack works end-to-end.
  - Files: `netconf/client/client.go`, `netconf/client/client_test.go`
  - Do: (1) Add 13 typed methods to `Client`: `Get(ctx, *Filter)`, `GetConfig(ctx, Datastore, *Filter)`, `EditConfig(ctx, EditConfig)`, `CopyConfig(ctx, CopyConfig)`, `DeleteConfig(ctx, DeleteConfig)`, `Lock(ctx, Datastore)`, `Unlock(ctx, Datastore)`, `CloseSession(ctx)`, `KillSession(ctx, uint32)`, `Validate(ctx, Datastore)`, `Commit(ctx, *Commit)`, `DiscardChanges(ctx)`, `CancelCommit(ctx, string)`. Each marshals its operation struct, calls `Do`, then checks for `<ok/>` or decodes `DataReply`/`ParseRPCErrors` as appropriate. Data-returning methods (`Get`, `GetConfig`) return `(*DataReply, error)`. Ok-returning methods return `error`. (2) Add a `Dial(ctx, addr, sshConfig, localCaps)` production constructor that calls `ssh.Dial` + `ClientSession` + `NewClient`. (3) Write a lightweight echo-server test helper that: accepts SSH connection via `ssh.Listener`, runs `ServerSession`, then loops reading RPCs, parsing the operation element from `RPC.Body`, and echoing back an `RPCReply` with `<ok/>` or `<data>` content depending on the operation. (4) Test each typed method: `TestClient_GetConfig`, `TestClient_EditConfig`, `TestClient_Lock`, etc. (5) `TestClient_RPCError` — server returns `<rpc-error>`, client returns `RPCErrors`. (6) `TestClient_SSHLoopback` — full Dial → typed operation → close over SSH. (7) No new dependencies.
  - Verify: `go test ./netconf/client/... -v -count=1 && go test ./... -v -count=1 && go vet ./...`
  - Done when: all 13 typed methods work, RPCError propagation proven, SSH loopback integration passes — total ≥15 tests in `client_test.go`

## Files Likely Touched

- `netconf/session.go` — add `Send`/`Recv` methods
- `netconf/client/client.go` — new: Client struct, Do, 13 typed methods, Dial
- `netconf/client/client_test.go` — new: all client tests
