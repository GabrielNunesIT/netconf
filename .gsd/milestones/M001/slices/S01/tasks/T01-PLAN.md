---
estimated_steps: 5
estimated_files: 6
skills_used:
  - test
  - lint
---

# T01: Initialize Go module and define core message and capability types

**Slice:** S01 — Transport & Session Foundation
**Milestone:** M001

## Description

Bootstrap the Go module and define the foundational types that every other task and slice depends on: XML message structs for NETCONF hello/rpc/rpc-reply with correct namespace handling, capability URN types with RFC 7803 format validation, and the Transport interface that decouples framing from protocol logic. This task retires the XML namespace handling risk identified in research by proving round-trip marshaling works.

## Steps

1. Initialize `go.mod` with module path `github.com/GabrielNunesIT/netconf`, Go 1.22+, and add `github.com/stretchr/testify` as a test dependency. Run `go mod tidy`.

2. Create `netconf/message.go` with XML struct types:
   - `Hello` struct: `XMLName xml.Name` with namespace `urn:ietf:params:xml:ns:netconf:base:1.0` and local name `hello`, `Capabilities []string` mapped to `<capabilities><capability>` elements, `SessionID uint32` (optional, server-sent only).
   - `RPC` struct: `XMLName` with namespace `urn:ietf:params:xml:ns:netconf:base:1.0` and local name `rpc`, `MessageID string` attribute, `Body []byte` as inner XML for the operation payload.
   - `RPCReply` struct: similar to RPC with `message-id` attribute, inner XML body for response data, and `Ok` field for `<ok/>` responses.
   - Include `xml.Name` constants for easy comparison.

3. Create `netconf/capability.go` with:
   - `Capability` type (string alias or struct) representing a URN.
   - Base capability constants: `BaseCap10 = "urn:ietf:params:netconf:base:1.0"`, `BaseCap11 = "urn:ietf:params:netconf:base:1.1"`.
   - `ValidateURN(s string) error` function that checks the string follows RFC 7803 `urn:ietf:params:netconf:capability:<name>:<version>` or `urn:ietf:params:netconf:base:<version>` format.
   - `CapabilitySet` type (a set/slice of capabilities) with `Contains()` and `Supports11()` helper methods.

4. Create `netconf/transport/transport.go` with the `Transport` interface:
   - `MsgReader() (io.ReadCloser, error)` — returns a reader for the next complete NETCONF message.
   - `MsgWriter() (io.WriteCloser, error)` — returns a writer that will frame and send a complete message when closed.
   - `Close() error` — closes the underlying connection.
   - Document that framers handle the EOM/chunked encoding transparently.

5. Write `netconf/message_test.go` and `netconf/capability_test.go`:
   - **message_test.go**: Marshal Hello with capabilities, unmarshal it back, verify namespace URI is preserved, verify capabilities round-trip, verify SessionID round-trip. Test RPC and RPCReply marshal/unmarshal with message-id attribute. Test that encoding/xml produces `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">`.
   - **capability_test.go**: Test ValidateURN accepts valid URNs and rejects malformed ones. Test CapabilitySet.Contains() and Supports11(). Test base capability constants match expected strings.

## Must-Haves

- [ ] `go.mod` exists at module path `github.com/GabrielNunesIT/netconf` with Go 1.22+
- [ ] `testify` is the only non-stdlib test dependency
- [ ] Hello XML marshals with `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`
- [ ] Hello XML round-trips through marshal/unmarshal preserving all fields
- [ ] Capability URN validation rejects malformed URNs
- [ ] Transport interface is defined with MsgReader/MsgWriter/Close
- [ ] All tests pass

## Verification

- `cd /home/user/repos/netconf/.gsd/worktrees/M001 && go test ./netconf/... -v -count=1` — all tests pass
- `go vet ./...` — no vet warnings
- `grep -q 'github.com/GabrielNunesIT/netconf' go.mod` — correct module path

## Observability Impact

- **What changes:** `go test ./netconf/... -v -count=1` becomes the primary signal for this task. On failure, the test output names the exact check (e.g. `TestHello_MarshalNamespace`) and the diff between expected and actual XML/string values.
- **How a future agent inspects this:** Run `go test ./netconf/... -v -run TestHello` or `go test ./netconf/... -v -run TestValidateURN` to exercise individual subsystems. `go vet ./...` surfaces type-level regressions.
- **Failure state visibility:** `ValidateURN` returns a human-readable error string that names the bad URN and the format it expected — visible in test output and loggable by callers. XML unmarshal failures include the element name and byte offset from `encoding/xml`.
- **No runtime process:** This task produces only types and pure-Go logic; there is no long-running process or network surface to monitor at this stage.

## Inputs

- `S01-RESEARCH.md` findings (inlined in slice plan) — architecture decisions on Transport interface pattern and XML namespace handling approach

## Expected Output

- `go.mod` — Go module definition with correct path and dependencies
- `go.sum` — dependency checksums
- `netconf/message.go` — Hello, RPC, RPCReply XML struct types
- `netconf/message_test.go` — XML round-trip and namespace tests
- `netconf/capability.go` — Capability type, URN validation, base constants
- `netconf/capability_test.go` — URN validation and capability set tests
- `netconf/transport/transport.go` — Transport interface definition
