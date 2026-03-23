---
estimated_steps: 4
estimated_files: 2
skills_used:
  - test
  - review
---

# T02: Add 13 typed operation methods and SSH loopback integration tests

**Slice:** S03 — Client Library API
**Milestone:** M001

## Description

Add the 13 typed operation methods to `Client` that make up the user-facing API (R004). Each method constructs the appropriate operation struct from S02, calls `Do`, and decodes the reply — returning either a `*DataReply` (for Get/GetConfig) or `error` (for everything else). Errors from the server (`<rpc-error>` in the reply body) are decoded via `ParseRPCErrors` and returned as `RPCErrors`.

Also add a `Dial(ctx, addr, sshConfig, localCaps)` production constructor and an SSH loopback integration test proving the full stack: TCP → SSH → NETCONF hello → typed operation → reply.

## Steps

1. **Add typed methods to `Client`** in `netconf/client/client.go`:
   - Data-returning methods (return `(*netconf.DataReply, error)`):
     - `Get(ctx context.Context, filter *netconf.Filter) (*netconf.DataReply, error)` — constructs `netconf.Get{Filter: filter}`, calls `Do`, decodes `DataReply` from `reply.Body`.
     - `GetConfig(ctx context.Context, source netconf.Datastore, filter *netconf.Filter) (*netconf.DataReply, error)` — constructs `netconf.GetConfig{Source: source, Filter: filter}`, calls `Do`, decodes `DataReply`.
   - Ok-returning methods (return `error`):
     - `EditConfig(ctx context.Context, cfg netconf.EditConfig) error` — calls `Do(ctx, &cfg)`, checks for errors.
     - `CopyConfig(ctx context.Context, cfg netconf.CopyConfig) error`
     - `DeleteConfig(ctx context.Context, cfg netconf.DeleteConfig) error`
     - `Lock(ctx context.Context, target netconf.Datastore) error` — constructs `netconf.Lock{Target: target}`.
     - `Unlock(ctx context.Context, target netconf.Datastore) error` — constructs `netconf.Unlock{Target: target}`.
     - `CloseSession(ctx context.Context) error` — constructs `netconf.CloseSession{}`.
     - `KillSession(ctx context.Context, sessionID uint32) error` — constructs `netconf.KillSession{SessionID: sessionID}`.
     - `Validate(ctx context.Context, source netconf.Datastore) error` — constructs `netconf.Validate{Source: source}`.
     - `Commit(ctx context.Context, opts *netconf.Commit) error` — if `opts` is nil, uses `&netconf.Commit{}`; otherwise uses `opts`.
     - `DiscardChanges(ctx context.Context) error` — constructs `netconf.DiscardChanges{}`.
     - `CancelCommit(ctx context.Context, persistID string) error` — constructs `netconf.CancelCommit{PersistID: persistID}`.
   - Internal helper `checkReply(reply *RPCReply) error`:
     - If `reply.Ok != nil`, return nil (success).
     - Call `ParseRPCErrors(reply)` — if errors found, return them (as `error` since `RPCErrors` implements `error`).
     - If ParseRPCErrors returns `(nil, nil)` and reply has body content, return nil (some servers return empty ok).
     - If ParseRPCErrors returns `(nil, err)`, return the parse error.
   - Internal helper `checkDataReply(reply *RPCReply) (*DataReply, error)`:
     - First call `ParseRPCErrors` — if errors, return them.
     - Unmarshal `reply.Body` into `DataReply`. If unmarshal fails, return error.
     - Return the DataReply.

2. **Add `Dial` constructor**:
   ```go
   func Dial(ctx context.Context, addr string, config *gossh.ClientConfig, localCaps netconf.CapabilitySet) (*Client, error)
   ```
   - Calls `ssh.Dial(addr, config)` to get the SSH transport.
   - Calls `netconf.ClientSession(trp, localCaps)` for hello exchange.
   - Wraps in `NewClient(session)`.
   - On any error, cleans up (close transport).
   - Note: `ctx` is passed for future timeout support but `ssh.Dial` doesn't take a context. For now, use `config.Timeout` for SSH-level timeout. The ctx parameter is reserved for future use and checked with `ctx.Err()` before starting.

3. **Write a lightweight echo-server test helper** in `netconf/client/client_test.go`:
   - `echoServer(t, serverTransport)` — runs a loop: reads a message via `transport.ReadMsg`, unmarshals as `netconf.RPC`, inspects the inner operation (by decoding the XML element name from `rpc.Body`), and replies with:
     - For `get` or `get-config`: an `RPCReply` with `<data><config/></data>` in Body.
     - For all other operations: an `RPCReply` with `<ok/>` set.
     - Message-id is copied from the request to the reply.
   - `echoServerWithError(t, serverTransport)` — same but replies with an `<rpc-error>` instead of `<ok/>`.
   - `newSSHTestPair(t)` — creates SSH Listener + Dial pair (reusing the pattern from `ssh_test.go`), runs `ServerSession` in a goroutine, returns `(*Client, serverTransport, cleanup)`.

4. **Write tests** (appending to `netconf/client/client_test.go`):
   - **`TestClient_GetConfig`** — calls `GetConfig` with `Datastore{Running: &struct{}{}}` and nil filter. Echo server returns `<data>` reply. Client decodes `DataReply` successfully.
   - **`TestClient_Get`** — calls `Get` with a subtree filter. Echo server returns `<data>` reply.
   - **`TestClient_EditConfig`** — calls `EditConfig`. Echo server returns `<ok/>`. Client returns nil error.
   - **`TestClient_Lock_Unlock`** — calls `Lock`, then `Unlock`. Both return nil.
   - **`TestClient_CloseSession`** — calls `CloseSession`. Returns nil.
   - **`TestClient_KillSession`** — calls `KillSession(42)`. Returns nil.
   - **`TestClient_Commit`** — calls `Commit(nil)` (plain commit). Returns nil.
   - **`TestClient_DiscardChanges`** — calls `DiscardChanges`. Returns nil.
   - **`TestClient_CancelCommit`** — calls `CancelCommit("")`. Returns nil.
   - **`TestClient_Validate`** — calls `Validate`. Returns nil.
   - **`TestClient_CopyConfig`** — calls `CopyConfig`. Returns nil.
   - **`TestClient_DeleteConfig`** — calls `DeleteConfig`. Returns nil.
   - **`TestClient_RPCError`** — echo server returns `<rpc-error>` in the reply. Client returns `RPCErrors`. Assert the error contains expected fields.
   - **`TestClient_SSHLoopback`** — full integration: `newSSHTestPair` → `GetConfig` → verify `DataReply` content → `CloseSession` → client.Close(). This test proves R004 end-to-end over SSH.
   - The echo server helper and loopback test helper created in T01 will be reused/extended.

## Must-Haves

- [ ] All 13 typed methods exist on `Client` with correct signatures
- [ ] Data-returning methods (`Get`, `GetConfig`) return `(*DataReply, error)`
- [ ] Ok-returning methods return `error`
- [ ] `ParseRPCErrors` is called on every reply; server errors propagate as `RPCErrors`
- [ ] `Dial` constructor composes SSH transport + ClientSession + NewClient
- [ ] SSH loopback integration test proves full stack (R004 proof)
- [ ] RPCError propagation test proves error model works through the client
- [ ] No new dependencies

## Verification

- `go test ./netconf/client/... -v -count=1` — ≥15 tests PASS (T01 tests + T02 tests)
- `go test ./... -v -count=1` — all tests pass (92 prior + new client tests)
- `go vet ./...` — clean
- `go test ./netconf/client/... -run TestClient_SSHLoopback -v -count=1` — SSH integration PASS

## Inputs

- `netconf/client/client.go` — Client struct with Do, NewClient, Close from T01
- `netconf/client/client_test.go` — test infrastructure (newTestPair helper) from T01
- `netconf/operation.go` — all 13 operation structs, Filter, Datastore, DataReply
- `netconf/errors.go` — ParseRPCErrors, RPCErrors
- `netconf/message.go` — RPC, RPCReply types
- `netconf/transport/ssh/client.go` — ssh.Dial for production constructor
- `netconf/transport/ssh/server.go` — ssh.Listener, ssh.NewListener for SSH loopback tests

## Expected Output

- `netconf/client/client.go` — modified: 13 typed methods, Dial constructor, checkReply/checkDataReply helpers added
- `netconf/client/client_test.go` — modified: ≥9 new tests for typed methods, RPCError propagation, SSH loopback
