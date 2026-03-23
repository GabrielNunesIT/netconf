---
estimated_steps: 4
estimated_files: 4
skills_used:
  - test
---

# T04: Implement SSH client and server transports with integration test

**Slice:** S01 — Transport & Session Foundation
**Milestone:** M001

## Description

Wire SSH subsystem channels as Transport implementations for both client and server sides using `golang.org/x/crypto/ssh`. The integration test proves the full NETCONF flow end-to-end: SSH connection → subsystem channel → hello exchange → capability negotiation → framing auto-negotiation. Tests use in-process `net.Pipe()` instead of real TCP to stay fast and infrastructure-free (per D008).

## Steps

1. Create `netconf/transport/ssh/client.go`:
   - `Dial(addr string, config *ssh.ClientConfig) (*Transport, error)` — dials SSH server, opens a session, requests "netconf" subsystem, wraps the channel's stdin/stdout as an `io.ReadWriter`, creates a Framer in EOM mode, returns a Transport.
   - `type Transport struct` implementing the `transport.Transport` interface — delegates `MsgReader()`/`MsgWriter()` to the underlying Framer, `Close()` closes the SSH channel and connection.
   - Also provide `NewClientTransport(channel ssh.Channel) *Transport` for testing — accepts a pre-established channel without dialing.
   - `Upgrade()` method delegates to Framer's `Upgrade()`.

2. Create `netconf/transport/ssh/server.go`:
   - `type ServerTransport struct` implementing `transport.Transport` — wraps an SSH channel.
   - `type Listener struct` — accepts SSH connections, handles new-channel requests of type "session", handles "subsystem" requests for "netconf", and for each valid request creates a `ServerTransport`.
   - `NewListener(listener net.Listener, config *ssh.ServerConfig) *Listener` — wraps a net.Listener.
   - `Accept() (*ServerTransport, error)` — blocks until a client opens a NETCONF subsystem, returns the transport.
   - Reject non-"netconf" subsystem requests with appropriate SSH reply.

3. Write `netconf/transport/ssh/ssh_test.go`:
   - **Test helper**: generate an in-memory RSA key pair for SSH server. Create `ssh.ServerConfig` with password auth (any password accepted for tests). Create `ssh.ClientConfig` with password auth. Use `net.Pipe()` for in-process connection (no TCP).
   - **Integration test — full hello with base:1.1 upgrade**: Start server listener goroutine. Client dials. Both sides run `session.ClientSession`/`session.ServerSession` with `[BaseCap10, BaseCap11]`. Verify: session-id received, capabilities exchanged, framing upgraded to chunked. After hello, write a test message through the transport in chunked mode and verify it arrives.
   - **Integration test — base:1.0 only**: Server advertises only `[BaseCap10]`. Verify framing stays EOM. Write/read a message in EOM mode.
   - **Subsystem rejection**: Client requests a non-"netconf" subsystem. Verify server rejects it.

4. Run `go mod tidy` to add `golang.org/x/crypto` dependency, then run the full test suite: `go test ./... -v -count=1`.

## Must-Haves

- [ ] SSH client transport opens "netconf" subsystem and implements Transport interface
- [ ] SSH server transport accepts connections and handles "netconf" subsystem requests
- [ ] Integration test proves hello exchange over SSH with session-id and capability negotiation
- [ ] Framing auto-negotiates to chunked over SSH when both peers support base:1.1
- [ ] Non-"netconf" subsystem requests are rejected
- [ ] Only approved dependencies used (golang.org/x/crypto/ssh per D007)
- [ ] `go test ./... -count=1` passes with zero failures

## Verification

- `cd /home/user/repos/netconf/.gsd/worktrees/M001 && go test ./... -v -count=1` — all tests pass (SSH + loopback + unit)
- `go vet ./...` — no vet warnings
- `grep -c 'golang.org/x/crypto' go.mod` — returns 1 (dependency present)

## Inputs

- `netconf/transport/transport.go` — Transport interface
- `netconf/transport/framer.go` — Framer with EOM/chunked modes and Upgrade()
- `netconf/session.go` — ClientSession/ServerSession for hello exchange
- `netconf/message.go` — Hello struct
- `netconf/capability.go` — BaseCap10, BaseCap11 constants

## Expected Output

- `netconf/transport/ssh/client.go` — SSH client transport implementation
- `netconf/transport/ssh/server.go` — SSH server transport with listener
- `netconf/transport/ssh/ssh_test.go` — SSH integration tests proving full hello flow
- `go.mod` — updated with golang.org/x/crypto dependency
- `go.sum` — updated checksums
