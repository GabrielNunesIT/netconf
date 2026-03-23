---
estimated_steps: 4
estimated_files: 1
skills_used:
  - test
---

# T02: Add client↔server integration tests with typed Client methods

**Slice:** S04 — Server Library & Handler Dispatch
**Milestone:** M001

## Description

Add integration tests to `netconf/server/server_test.go` that prove the server works end-to-end with the real `client.Client` from the `netconf/client` package. This is the integration closure for R005: typed client methods (GetConfig, EditConfig, CloseSession, etc.) driving real RPCs against a Server with mock handlers, over loopback transport.

These tests prove that the server's dispatch loop, reply marshalling, and error propagation are compatible with the client's dispatcher, reply parsing, and error unwrapping — the two packages compose correctly at runtime.

## Steps

1. **Add `TestServer_WithClient`** to `netconf/server/server_test.go`:
   - Create a loopback transport pair with `transport.NewLoopback()`
   - Run `ClientSession` and `ServerSession` concurrently (both need base:1.0 caps; the pair is unbuffered so hellos must be concurrent — see L005)
   - Create a `Server` and register mock handlers:
     - `"get-config"` handler: returns `[]byte(`<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><config/></data>`)` as body, nil error
     - `"edit-config"` handler: returns `(nil, nil)` for ok reply
   - Start `server.Serve(ctx, serverSess)` in a goroutine
   - Create a `client.NewClient(clientSess)`
   - Call `client.GetConfig(ctx, running, nil)` — assert no error, DataReply not nil
   - Call `client.EditConfig(ctx, editCfg)` — assert no error
   - Call `client.CloseSession(ctx)` — assert no error (triggers close-session built-in, Serve returns)
   - Wait for Serve goroutine to return, assert nil error
   - Clean up: `client.Close()`

2. **Add `TestServer_WithClient_RPCError`** to `netconf/server/server_test.go`:
   - Same setup but register a handler for `"get-config"` that returns `netconf.RPCError{Type:"application", Tag:"invalid-value", Severity:"error", Message:"test error from server"}`
   - Call `client.GetConfig(ctx, running, nil)` — assert error
   - Use `errors.As(err, &netconf.RPCError{})` to extract the RPCError
   - Assert RPCError fields match: Type="application", Tag="invalid-value", Severity="error", Message="test error from server"
   - This proves the full error propagation chain: server handler → RPCError → marshal → transport → client dispatcher → ParseRPCErrors → errors.As

3. **Add `TestServer_ContextCancel`** to `netconf/server/server_test.go`:
   - Create loopback pair, establish sessions
   - Start Serve with a cancellable context
   - Cancel the context
   - Assert Serve returns (may return context error or transport error depending on timing)
   - This proves graceful shutdown via context cancellation

4. **Run full verification:**
   ```bash
   go test ./netconf/server/... -v -count=1
   go test ./... -count=1
   go vet ./...
   ```

## Must-Haves

- [ ] `TestServer_WithClient` passes: GetConfig returns DataReply, EditConfig returns ok, CloseSession terminates Serve cleanly
- [ ] `TestServer_WithClient_RPCError` passes: handler RPCError propagates through client as structured `netconf.RPCError` via `errors.As`
- [ ] `TestServer_ContextCancel` passes: context cancellation terminates Serve
- [ ] Full suite regression passes (132 prior + all new server tests)
- [ ] `go vet ./...` clean

## Verification

- `go test ./netconf/server/... -v -count=1` — all PASS including integration tests
- `go test ./... -count=1` — all prior tests + all server tests PASS
- `go vet ./...` — clean
- `go test ./netconf/server/... -run TestServer_WithClient -v -count=1` — full client↔server round-trip proven

## Inputs

- `netconf/server/server.go` — Server struct, Handler interface, Serve loop (from T01)
- `netconf/server/server_test.go` — existing test helpers from T01 (newTestPair, etc.)
- `netconf/client/client.go` — Client struct, NewClient, typed methods (Get, GetConfig, EditConfig, CloseSession)
- `netconf/session.go` — ClientSession, ServerSession
- `netconf/message.go` — RPC, RPCReply types
- `netconf/errors.go` — RPCError, ParseRPCErrors
- `netconf/operation.go` — Datastore, EditConfig, GetConfig, Filter, DataReply types
- `netconf/transport/loopback.go` — NewLoopback
- `netconf/capability.go` — BaseCap10, NewCapabilitySet

## Expected Output

- `netconf/server/server_test.go` — 3 additional integration tests appended (TestServer_WithClient, TestServer_WithClient_RPCError, TestServer_ContextCancel)
