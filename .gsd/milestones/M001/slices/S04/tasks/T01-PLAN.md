---
estimated_steps: 5
estimated_files: 2
skills_used:
  - test
---

# T01: Implement server package with Handler interface, Server struct, and Serve dispatch loop

**Slice:** S04 — Server Library & Handler Dispatch
**Milestone:** M001

## Description

Create the `netconf/server` package — the server-side counterpart to `netconf/client`. This package provides a `Handler` interface that implementers register per-operation, and a `Server` struct that accepts an established `*netconf.Session`, reads RPCs, dispatches to the appropriate handler, and sends replies. The Serve loop runs until context cancellation, transport error, or a `<close-session>` RPC.

The existing `echoServer` helper in `netconf/client/client_test.go` is a direct prototype of the dispatch loop body — the server package formalises it with a registration interface, proper error handling, and built-in `close-session` interception.

**Key design decisions:**
- `Handler` receives `*netconf.RPC` (raw body) — handlers unmarshal the body themselves. This avoids requiring the server to know all 13 operation types.
- Handler returns `([]byte, error)` — nil body + nil error → `<ok/>` reply; non-nil body → embedded in `<rpc-reply>`; error is `RPCError` → marshalled `<rpc-error>` reply; other error → generic rpc-error.
- `close-session` is handled as a built-in before handler lookup — sends `<ok/>` and returns nil from Serve.
- Operation name extraction uses `xml.Name.Local` (not qualified name), consistent with P009 in `client_test.go`.
- The `Serve` loop is single-threaded: handler is called synchronously, then reply is sent. No goroutines inside dispatch.

## Steps

1. **Create `netconf/server/server.go`** with:
   - `Handler` interface: `Handle(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error)`
   - `HandlerFunc` adapter type (like `http.HandlerFunc`)
   - `Server` struct: holds `handlers map[string]Handler` (keyed by operation XML local name)
   - `NewServer()` constructor returning a `*Server` with empty handler map
   - `RegisterHandler(opName string, h Handler)` method to register a handler for an operation name
   - `Serve(ctx context.Context, sess *netconf.Session) error` method — the dispatch loop:
     a. Call `sess.Recv()` — on error, return the error (transport closed or ctx done)
     b. `xml.Unmarshal` into `netconf.RPC` — on error, skip (malformed message, can't reply without message-id)
     c. Extract operation name from `rpc.Body` using `xml.NewDecoder` + scan for first `xml.StartElement` + use `.Name.Local`
     d. If operation name is `"close-session"`: marshal and send `<rpc-reply><ok/></rpc-reply>` with matching message-id, then return nil
     e. Look up handler by operation name in the map
     f. If no handler registered: build an `RPCError{Type:"protocol", Tag:"operation-not-supported", Severity:"error", Message:"operation '<name>' is not supported"}`, marshal it, embed in reply body, send
     g. Call handler. Convert result to `RPCReply`:
        - `(nil, nil)` → set `Reply.Ok = &struct{}{}`
        - `(body, nil)` → set `Reply.Body = body`
        - `(_, RPCError)` → marshal the RPCError, set as Reply.Body
        - `(_, other error)` → build a generic `RPCError{Type:"application", Tag:"operation-failed", Severity:"error", Message:err.Error()}`, marshal, set as Reply.Body
     h. Marshal reply, send via `sess.Send()`, loop back to (a)

2. **Create `netconf/server/server_test.go`** with test helpers:
   - `newTestPair(t) (*netconf.Session, *netconf.Session)` — creates a loopback transport pair, runs ClientSession/ServerSession concurrently (same pattern as `client_test.go`'s `newTestPair`), returns the client-side session and server-side session
   - `sendRPC(t, sess, msgID, opBody)` — helper to marshal and send an RPC with given body
   - `recvReply(t, sess) *netconf.RPCReply` — helper to receive and unmarshal a reply

3. **Write unit tests** in `netconf/server/server_test.go`:
   - `TestServer_DispatchesToRegisteredHandler` — register a handler for "get-config" that returns `<data><config/></data>` body; send a `<get-config>` RPC; assert reply body contains `<data>` content
   - `TestServer_UnknownOperation_ReturnsError` — send an RPC with an unregistered operation name; assert reply contains `<rpc-error>` with tag `operation-not-supported`
   - `TestServer_HandlerRPCError_PropagatesAsReply` — register a handler that returns `netconf.RPCError{Type:"application", Tag:"invalid-value", ...}`; send RPC; assert reply body contains matching `<rpc-error>` fields
   - `TestServer_CloseSession_TerminatesServeLoop` — send `<close-session>` RPC; assert Serve returns nil; assert reply is `<ok/>`
   - `TestServer_HandlerReturnsOk` — register a handler that returns `(nil, nil)`; send RPC; assert reply has `<ok/>`

4. **Run tests and verify:**
   ```bash
   go test ./netconf/server/... -v -count=1
   go test ./... -count=1
   go vet ./...
   ```

5. **Verify error messages are descriptive** — the `operation-not-supported` error message should name the operation; handler errors should include context.

## Must-Haves

- [ ] `Handler` interface defined with `Handle(ctx, *Session, *RPC) ([]byte, error)` signature
- [ ] `Server` struct with `RegisterHandler` and `Serve` methods
- [ ] Built-in `close-session` interception sends `<ok/>` and returns nil from Serve
- [ ] Unknown operations produce `operation-not-supported` rpc-error reply
- [ ] Handler returning `RPCError` produces well-formed `<rpc-error>` reply
- [ ] Handler returning `(nil, nil)` produces `<ok/>` reply
- [ ] Handler returning `(body, nil)` produces reply with embedded body
- [ ] 5+ unit tests pass covering dispatch, unknown op, rpc-error, close-session, ok reply
- [ ] Full suite regression passes (132 prior tests + new)
- [ ] `go vet ./...` clean

## Verification

- `go test ./netconf/server/... -v -count=1` — all PASS
- `go test ./... -count=1` — 132 prior + new tests all PASS
- `go vet ./...` — clean
- `go test ./netconf/server/... -run TestServer_UnknownOperation -v -count=1` — error message names the operation

## Inputs

- `netconf/session.go` — Session type with Send/Recv methods, ClientSession/ServerSession constructors
- `netconf/message.go` — RPC, RPCReply, NetconfNS types
- `netconf/errors.go` — RPCError struct (implements error interface), ParseRPCErrors
- `netconf/transport/loopback.go` — NewLoopback for test transport pairs
- `netconf/transport/transport.go` — Transport interface, WriteMsg/ReadMsg helpers
- `netconf/capability.go` — BaseCap10, NewCapabilitySet
- `netconf/client/client_test.go` — echoServer pattern (P009) and newTestPair pattern as reference

## Expected Output

- `netconf/server/server.go` — Handler interface, Server struct, NewServer, RegisterHandler, Serve
- `netconf/server/server_test.go` — 5+ unit tests proving dispatch, unknown op, rpc-error, close-session, ok reply
