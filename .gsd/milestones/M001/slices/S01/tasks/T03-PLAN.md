---
estimated_steps: 3
estimated_files: 2
skills_used:
  - test
---

# T03: Implement session hello exchange and capability negotiation over loopback

**Slice:** S01 — Transport & Session Foundation
**Milestone:** M001

## Description

Build the Session type that orchestrates the NETCONF session lifecycle: sending/receiving hello messages, negotiating capabilities, assigning session-id, and auto-negotiating the framing mode. This task uses the loopback transport from T02 to prove the protocol interaction works without SSH complexity. The Session must work for both client-side and server-side use (the server generates session-id, the client receives it).

## Steps

1. Create `netconf/session.go` with:
   - `Session` struct fields: `transport Transport`, `sessionID uint32`, `localCaps CapabilitySet`, `remoteCaps CapabilitySet`, `framingMode` (enum: EOM or Chunked).
   - `ClientSession(transport Transport, localCaps CapabilitySet) (*Session, error)` — client-side session establishment: marshal and send local Hello (no session-id), read remote Hello from server, extract server's session-id and capabilities, determine framing mode (use base:1.1 chunked if both peers advertise `BaseCap11`, otherwise stay EOM), call transport's `Upgrade()` if switching to chunked. Return initialized Session.
   - `ServerSession(transport Transport, localCaps CapabilitySet, sessionID uint32) (*Session, error)` — server-side: marshal and send local Hello with session-id, read client Hello, extract client capabilities, determine framing, upgrade if needed. Return initialized Session.
   - Both functions must validate that remote hello contains at least `BaseCap10` (mandatory per RFC 6241).
   - Accessor methods: `SessionID()`, `LocalCapabilities()`, `RemoteCapabilities()`, `FramingMode()`.
   - `Close() error` — closes the underlying transport.

2. Write `netconf/session_test.go`:
   - **Happy path — both support 1.1**: Create loopback pair. Run `ServerSession` in a goroutine with caps `[BaseCap10, BaseCap11]` and session-id 42. Run `ClientSession` with caps `[BaseCap10, BaseCap11]`. Verify: client sees session-id 42, both see each other's capabilities, both framing modes are Chunked.
   - **Fallback to 1.0**: Server advertises only `[BaseCap10]`, client advertises `[BaseCap10, BaseCap11]`. Verify: framing stays EOM on both sides.
   - **Client only 1.0**: Client advertises `[BaseCap10]`, server advertises `[BaseCap10, BaseCap11]`. Verify: framing stays EOM.
   - **Capability intersection**: Server has extra capabilities (e.g., `:candidate`, `:validate`). Verify client's `RemoteCapabilities()` contains them.
   - **Invalid hello — missing base:1.0**: Remote sends hello without `BaseCap10`. Verify error is returned.
   - **Session-id propagation**: Verify client's `SessionID()` matches what server sent.

3. Run full test suite to confirm no regressions: `go test ./netconf/... ./netconf/transport/... -v -count=1`.

## Must-Haves

- [ ] `ClientSession()` and `ServerSession()` complete hello exchange over Transport
- [ ] Framing auto-negotiates to chunked when both peers support base:1.1
- [ ] Framing stays EOM when either peer lacks base:1.1 capability
- [ ] Server-assigned session-id propagates to client
- [ ] Remote hello without base:1.0 capability is rejected with error
- [ ] All tests pass

## Verification

- `cd /home/user/repos/netconf/.gsd/worktrees/M001 && go test ./netconf/... -v -count=1 -run TestSession` — session tests pass
- `go test ./... -v -count=1` — full suite passes (no regressions)

## Inputs

- `netconf/message.go` — Hello struct for marshal/unmarshal
- `netconf/capability.go` — CapabilitySet, BaseCap10, BaseCap11 constants
- `netconf/transport/transport.go` — Transport interface
- `netconf/transport/loopback.go` — loopback transport for testing

## Expected Output

- `netconf/session.go` — Session type with ClientSession/ServerSession, hello exchange, capability negotiation, framing auto-negotiation
- `netconf/session_test.go` — comprehensive session tests over loopback transport
