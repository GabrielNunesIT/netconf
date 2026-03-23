# S02: Base Protocol Operations & Error Model

**Goal:** All 13 RFC 6241 base protocol operations have typed Go request/response structs, the full rpc-error model from §4.3 is implemented, all 8 standard capability constants are defined, and subtree/XPath filter types are usable — all proven by XML round-trip tests.
**Demo:** `go test ./netconf/... -v -count=1` passes with tests covering every operation type, the error model, filter types, and capability constants. Marshaled XML includes correct NETCONF namespace on every operation element.

## Must-Haves

- `RPCError` type with all RFC 6241 §4.3 fields (`error-type`, `error-tag`, `error-severity`, `error-app-tag`, `error-path`, `error-message`, `error-info`) that implements the `error` interface
- `ParseRPCErrors` function that decodes `<rpc-error>` elements from `RPCReply.Body` raw bytes
- All 13 RFC 6241 operation request structs with correct NETCONF base namespace in XMLName tags
- `Datastore` shared type encoding `<running/>`, `<candidate/>`, `<startup/>`, `<url>` as child elements (not attributes)
- `Filter` type supporting both subtree (`innerxml` content) and XPath (`select` attribute) modes
- `DataReply` type for decoding `<data>` response content from `RPCReply.Body`
- All 8 standard capability constants (`:candidate`, `:confirmed-commit`, `:rollback-on-error`, `:validate`, `:startup`, `:url`, `:xpath`, `:writable-running`) that pass `ValidateURN`
- XML round-trip tests for every operation and the error model — marshal then unmarshal, verify fields match and `xmlns` is present

## Verification

- `go test ./netconf/... -v -count=1` — all tests pass (existing S01 tests + new S02 tests)
- `go vet ./...` — clean
- `go test ./netconf/... -run TestRPCError -v -count=1` — error model tests pass
- `go test ./netconf/... -run TestParseRPCErrors -v -count=1` — error parsing from RPCReply.Body works
- `go test ./netconf/... -run Test.*Operation -v -count=1` — all 13 operation round-trip tests pass
- `go test ./netconf/... -run TestFilter -v -count=1` — subtree and XPath filter tests pass
- `go test ./netconf/... -run TestCapability -v -count=1` — new capability constants validated

## Observability / Diagnostics

The S02 types are pure data structures — they carry no runtime state and produce no I/O.  The primary runtime signals come through the error interface and test output:

- **`RPCError.Error()` string** — every decoded NETCONF error is visible as a structured log line via `fmt.Errorf` wrapping or direct logging; includes type, tag, severity, and message.
- **`ParseRPCErrors` failure path** — returns a wrapped `xml decode` error on malformed Body; callers should log `err.Error()` at WARN level with the raw `Body` bytes (redacted in production if they may contain credentials).
- **Go test `-v` output** — `go test ./netconf/... -v -run "TestRPCError|TestParseRPCErrors|TestCapability"` is the primary inspection surface; every test function name encodes the scenario being verified.
- **Failure visibility** — if `ParseRPCErrors` returns `(nil, err)` the Body bytes were not valid XML; if it returns `(nil, nil)` the reply was an `<ok/>` with no errors; if it returns a non-nil slice each element's `Error()` gives a human-readable summary.
- **Redaction constraint** — `RPCError.Message` and `RPCError.Info` may contain device-specific data; log them only at DEBUG level in production.

## Integration Closure

- Upstream surfaces consumed: `netconf/message.go` (`RPC`, `RPCReply`, `NetconfNS`), `netconf/capability.go` (`ValidateURN`, `BaseCap10`, `BaseCap11`)
- New wiring introduced in this slice: none — S02 defines types only; dispatch logic is S04
- What remains before the milestone is truly usable end-to-end: S03 (client API), S04 (server dispatch + handler registration), S05 (conformance suite)

## Tasks

- [x] **T01: Implement rpc-error model and standard capability constants** `est:45m`
  - Why: The error model is consumed by all operation responses and by S04's server dispatch. Capability constants are referenced by operation comments and by S03/S04 for capability gating. Both are prerequisites for T02.
  - Files: `netconf/errors.go`, `netconf/errors_test.go`, `netconf/capability.go`, `netconf/capability_test.go`
  - Do: Create `RPCError` struct with all §4.3 fields using `xml:",innerxml"` for error-info. Implement `error` interface. Write `ParseRPCErrors(reply *RPCReply)` that wraps Body in a synthetic root element to decode multiple `<rpc-error>` children. Add 8 capability constants to `capability.go`. Write tests: marshal/unmarshal round-trip for RPCError, ParseRPCErrors from realistic RPCReply.Body bytes, multi-error parsing, error-info with complex XML children, all 8 capability constants pass ValidateURN. Namespace must be in struct tag per L001. No new dependencies per K001.
  - Verify: `go test ./netconf/... -run "TestRPCError|TestParseRPCErrors|TestCapability" -v -count=1 && go vet ./...`
  - Done when: RPCError round-trips through XML correctly, ParseRPCErrors extracts errors from RPCReply.Body bytes, all 8 capability constants pass ValidateURN, all existing tests still pass

- [x] **T02: Implement all 13 operation types, filter types, and datastore model** `est:1h`
  - Why: The 13 operation types are the core deliverable of this slice — they define the XML wire format for every NETCONF RPC. Filter and Datastore are shared types used across multiple operations.
  - Files: `netconf/operation.go`, `netconf/operation_test.go`
  - Do: Create all 13 operation request structs (Get, GetConfig, EditConfig, CopyConfig, DeleteConfig, Lock, Unlock, CloseSession, KillSession, Validate, Commit, DiscardChanges, CancelCommit) each with `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 <element>"` namespace tag. Create Datastore struct with Running/Candidate/Startup as `*struct{}` and URL as string, all with omitempty. Create Filter struct with Type attr, Select attr, Content innerxml. Create DataReply for `<data>` responses. Write comprehensive tests: XML round-trip for each of the 13 operations verifying field preservation and xmlns presence, Datastore encoding as child elements (not attributes), Filter with subtree content and XPath select attr, DataReply decoding, full RPC→marshal→RPCReply.Body→decode composition test. No new dependencies.
  - Verify: `go test ./netconf/... -v -count=1 && go vet ./...`
  - Done when: All 13 operations marshal with correct NETCONF namespace, Datastore encodes as child elements, Filter handles both subtree and XPath, DataReply decodes from RPCReply.Body, all existing + new tests pass

## Files Likely Touched

- `netconf/errors.go` (new)
- `netconf/errors_test.go` (new)
- `netconf/capability.go` (append constants)
- `netconf/capability_test.go` (append tests)
- `netconf/operation.go` (new)
- `netconf/operation_test.go` (new)
