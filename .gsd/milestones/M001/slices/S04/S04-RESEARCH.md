# S04: Server Library & Handler Dispatch — Research

**Date:** 2026-03-22

## Summary

S04 builds the `netconf/server` package — the server-side mirror of `netconf/client`. Where the client sends RPCs and awaits replies, the server receives RPCs, dispatches to registered handler callbacks, and sends replies. The underlying session management, framing, transport, and XML types all exist; this slice is pure integration and dispatch logic.

The slice owns R005 exclusively. The implementation landscape is straightforward: everything needed is already in the codebase. The server package follows the same structural shape as `netconf/client` but in reverse — one accept-and-dispatch loop instead of a send-and-match loop. No new types, no new XML, no new dependencies.

The only genuine design question is the handler interface shape: what should a registered handler receive, and what should it return? The answer is clear from the existing types: a handler receives the decoded operation struct (or the raw body), and returns either nothing (ok reply) or a `DataReply`/`RPCError`. A table-driven dispatch keyed on XML element name is the natural fit, mirroring P009 (`echoServer` in client tests), which already proves the pattern works.

## Recommendation

Create `netconf/server/server.go` with:

1. **`Handler` interface** — `Handle(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error)` — receives raw RPC, returns raw body bytes to embed in `<rpc-reply>` (nil → `<ok/>`), or an `RPCError` to embed as `<rpc-error>`. Using the raw `*netconf.RPC` (with its `Body []byte` innerxml) avoids requiring the server to know all 13 operation types — callers unmarshal the body themselves.

2. **`Server` struct** — holds a `CapabilitySet`, a `map[string]Handler` keyed by operation XML element name (e.g. `"get-config"`, `"edit-config"`), and a default handler for unknown operations (returns `operation-not-supported` rpc-error).

3. **`Serve(ctx, sess)` method** — runs the per-session dispatch loop: `sess.Recv()` → unmarshal `RPC` → extract operation name from `Body` → look up handler → call handler → marshal reply → `sess.Send()`. Exits on `ctx.Done()` or transport error.

4. **`NewServer(caps, handlers)` constructor** — returns a `*Server` ready to serve.

The `echoServer` helper in `client_test.go` is a direct prototype of the dispatch loop body. The server package formalises it with a registration interface and proper error handling.

## Implementation Landscape

### Key Files

- `netconf/session.go` — `Session.Recv()` and `Session.Send()` are the only surface the server needs to read RPCs and write replies; already public since S03.
- `netconf/message.go` — `RPC` and `RPCReply` are the envelope types; `RPCReply.Ok` (`*struct{}`) and `RPCReply.Body` (`[]byte`) are used to build replies.
- `netconf/errors.go` — `RPCError` is used to construct error replies; its fields map directly to `<rpc-error>` child elements. The server marshals an `RPCError` and wraps it in the reply body.
- `netconf/operation.go` — 13 operation structs; handlers that want typed access unmarshal `RPC.Body` themselves. The server layer does not need to unmarshal operations — it passes the raw `*RPC` to the handler.
- `netconf/transport/ssh/server.go` — `Listener.Accept()` delivers a `*ServerTransport`; the server calls `netconf.ServerSession(trp, caps, id)` to establish a session, then `server.Serve(ctx, sess)` on it.
- `netconf/client/client_test.go` — `echoServer` function is the exact prototype: read `RPC`, check `firstElementName(rpc.Body)`, dispatch to data or ok reply. The server package formalises this.
- `netconf/client/client.go` — `Do`, `checkReply`, `checkDataReply` are the client-side counterparts; understanding them clarifies the reply contract the server must satisfy.

### New Files

- `netconf/server/server.go` — `Handler` interface, `Server` struct, `NewServer`, `Serve`, `RegisterHandler`, and internal `dispatchRPC` helper.
- `netconf/server/server_test.go` — tests using `transport.NewLoopback()` + `netconf.ServerSession/ClientSession` pairs; a real `client.Client` drives operations and mock handlers verify dispatch.

### Build Order

1. **Define `Handler` interface and `Server` struct** — no dependencies except `netconf` package types. This unblocks the handler test scaffolding.
2. **Implement `Serve` loop** — reads from `sess.Recv()`, dispatches via `dispatchRPC`, writes via `sess.Send()`. Use `firstElementName` pattern from P009 to extract operation name from `RPC.Body`.
3. **Implement `dispatchRPC`** — looks up handler by operation name, calls it, converts `([]byte, error)` return to an `RPCReply`. When error is `RPCError`, marshal it as the reply body; when nil, set `Reply.Ok`; otherwise use a generic `rpc-error`.
4. **Write tests** — mock `Handler` implementations; use `newTestPair` pattern from client tests (loopback transport + concurrent `ClientSession`/`ServerSession`); exercise: dispatch to registered handler, unknown operation error, handler returning RPCError, close-session termination.

### Verification Approach

```bash
go test ./netconf/server/... -v -count=1   # server package: all PASS
go test ./...                              # full suite regression: all 132 prior tests PASS
go vet ./...                               # clean
```

Specific test names to prove R005:
- `TestServer_DispatchesToRegisteredHandler` — operation name routes to correct handler
- `TestServer_UnknownOperation_ReturnsError` — unregistered operation gets `operation-not-supported`
- `TestServer_HandlerRPCError_PropagatesAsRPCReply` — handler returning `RPCError` produces well-formed `<rpc-error>` reply
- `TestServer_CloseSession_TerminatesServeLoop` — `<close-session>` causes `Serve` to return cleanly
- `TestServer_SessionWithClient` — full loopback: `client.Client` calls typed methods against a `Server` with mock handlers, replies match

## Constraints

- Only approved dependencies: Go stdlib, `golang.org/x/crypto`, `github.com/stretchr/testify`. No new imports (K001).
- Session-id assignment is the caller's responsibility (whoever calls `ServerSession`), not the `Server` struct. The server accepts an already-established `*Session`.
- The server does not implement any datastore — handlers receive the raw RPC body and return raw reply bytes. Actual data storage is out of scope (M001 context).
- `close-session` has prescribed RFC 6241 behavior: the server MUST gracefully terminate the session. The `Serve` loop should handle `close-session` as a built-in (not delegated to a registered handler) that causes `Serve` to return after sending `<ok/>`.
- `kill-session` dispatch follows RFC 6241 §7.9: in this library scope, it kills another session by session-id. For M001, there is no session registry, so a mock handler is sufficient; a real implementation is future scope.

## Common Pitfalls

- **Reply body namespace** — `RPCReply` does not wrap inner body content in a namespace element; handlers should return raw XML bytes without adding a namespace wrapper (the `<rpc-reply>` wrapper carries the namespace). See how `echoServer` in `client_test.go` returns `dataBody` with the namespace on `<data>` itself.
- **`<ok/>` vs empty body** — to return an `<ok/>` reply, set `RPCReply.Ok = &struct{}{}` and leave `Body` nil. An empty `Body` with `Ok == nil` produces an empty reply that `checkReply` on the client side may mis-handle. Follow the exact `okReply` pattern from `client_test.go`.
- **`close-session` built-in handling** — if `close-session` is dispatched to a user handler that returns an error, the session may not terminate cleanly. The `Serve` loop should intercept `close-session` before handler lookup and handle it directly (send `<ok/>`, then return).
- **Concurrent `Serve` and `Send`** — `Session.Send` is not goroutine-safe; the `Serve` loop is single-threaded by design. Do not call `sess.Send` from the handler goroutine concurrently with the `Serve` loop. The loop calls the handler synchronously and then sends the reply — no goroutines inside `dispatchRPC`.
- **`firstElementName` on namespaced bodies** — RPC bodies include `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`. Use `xml.Name.Local` (not the full qualified name) when keying the handler map, consistent with P009.
