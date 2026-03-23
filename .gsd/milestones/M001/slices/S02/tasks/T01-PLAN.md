---
estimated_steps: 5
estimated_files: 4
skills_used:
  - test
---

# T01: Implement rpc-error model and standard capability constants

**Slice:** S02 — Base Protocol Operations & Error Model
**Milestone:** M001

## Description

Build the RFC 6241 §4.3 rpc-error model and add the 8 standard capability constants. The `RPCError` type is consumed by all operation responses and by S04's server dispatch layer, so it must be built first. The capability constants are referenced by operation types and by S03/S04 for capability gating.

The error model consists of:
- `RPCError` struct with all §4.3 fields, implementing the `error` interface
- `RPCErrors` type alias (`[]RPCError`) for multi-error responses
- `ParseRPCErrors(reply *RPCReply) ([]RPCError, error)` to extract errors from `RPCReply.Body`

The capability constants are 8 `const` entries appended to the existing `capability.go` file.

## Steps

1. **Create `netconf/errors.go`** with `RPCError` struct. Fields: `XMLName xml.Name \`xml:"rpc-error"\``, `Type string \`xml:"error-type"\``, `Tag string \`xml:"error-tag"\``, `Severity string \`xml:"error-severity"\``, `AppTag string \`xml:"error-app-tag,omitempty"\``, `Path string \`xml:"error-path,omitempty"\``, `Message string \`xml:"error-message,omitempty"\``, `Info []byte \`xml:",innerxml"\``. Implement `Error() string` method returning a formatted string with type, tag, severity, and message. Define `RPCErrors = []RPCError` type.

2. **Implement `ParseRPCErrors`** in `netconf/errors.go`. This function takes a `*RPCReply` and decodes `<rpc-error>` elements from `RPCReply.Body`. The Body field is raw innerxml without a wrapping root element, so wrap it in a synthetic `<wrapper>` element before decoding: `xml.Unmarshal([]byte("<wrapper>"+string(reply.Body)+"</wrapper>"), &wrapper)` where wrapper has an `Errors []RPCError` field. Return nil slice (no error) if Body is empty or contains no rpc-error elements. Import from the existing `netconf` package — use `RPCReply` directly.

3. **Add 8 capability constants to `netconf/capability.go`**. Append a new `const` block after the existing `BaseCap10`/`BaseCap11` block:
   - `CapabilityCandidate = "urn:ietf:params:netconf:capability:candidate:1.0"`
   - `CapabilityConfirmedCommit = "urn:ietf:params:netconf:capability:confirmed-commit:1.1"`
   - `CapabilityRollbackOnError = "urn:ietf:params:netconf:capability:rollback-on-error:1.0"`
   - `CapabilityValidate = "urn:ietf:params:netconf:capability:validate:1.1"`
   - `CapabilityStartup = "urn:ietf:params:netconf:capability:startup:1.0"`
   - `CapabilityURL = "urn:ietf:params:netconf:capability:url:1.0"`
   - `CapabilityXPath = "urn:ietf:params:netconf:capability:xpath:1.0"`
   - `CapabilityWritableRunning = "urn:ietf:params:netconf:capability:writable-running:1.0"`

4. **Create `netconf/errors_test.go`** with tests:
   - `TestRPCError_MarshalUnmarshal` — round-trip a fully populated RPCError through xml.Marshal → xml.Unmarshal, verify all fields preserved
   - `TestRPCError_Error` — verify the Error() string format includes type, tag, severity, message
   - `TestParseRPCErrors_SingleError` — construct RPCReply with Body containing one `<rpc-error>` element, parse it, verify fields
   - `TestParseRPCErrors_MultipleErrors` — RPCReply.Body with two `<rpc-error>` elements, verify both parsed
   - `TestParseRPCErrors_OkReply` — RPCReply with `Ok: &struct{}{}` and empty Body, verify nil slice returned
   - `TestParseRPCErrors_ComplexErrorInfo` — RPCReply.Body with `<rpc-error>` containing multi-element `<error-info>` XML children, verify Info field captures raw XML
   - `TestRPCError_OptionalFieldsOmitted` — marshal RPCError with only required fields set, verify optional fields (AppTag, Path, Message) are absent from XML output

5. **Add capability constant tests to `netconf/capability_test.go`**:
   - `TestStandardCapabilities_ValidURN` — loop over all 8 new constants, call `ValidateURN`, assert no error
   - `TestCapabilitySet_ContainsStandardCaps` — create a CapabilitySet containing the new constants, verify `Contains` works for each

## Must-Haves

- [ ] `RPCError` struct has all 7 RFC 6241 §4.3 fields with correct xml tags
- [ ] `RPCError` implements the `error` interface
- [ ] `RPCError.Info` uses `[]byte \`xml:",innerxml"\`` to preserve arbitrary error-info XML
- [ ] `ParseRPCErrors` correctly decodes single and multiple `<rpc-error>` elements from `RPCReply.Body`
- [ ] `ParseRPCErrors` returns nil slice for ok replies with no errors
- [ ] All 8 standard capability constants pass `ValidateURN`
- [ ] No new dependencies added (stdlib `encoding/xml` and `fmt` only)
- [ ] All existing S01 tests still pass

## Verification

- `go test ./netconf/... -run "TestRPCError|TestParseRPCErrors" -v -count=1` — all error model tests pass
- `go test ./netconf/... -run "TestStandardCapabilities|TestCapabilitySet_ContainsStandard" -v -count=1` — capability tests pass
- `go test ./netconf/... -count=1` — all tests pass (existing + new)
- `go vet ./...` — clean

## Inputs

- `netconf/message.go` — `RPCReply` type with `Body []byte` field that ParseRPCErrors decodes from
- `netconf/capability.go` — existing file to append 8 capability constants to
- `netconf/capability_test.go` — existing test file to append capability constant tests to

## Expected Output

- `netconf/errors.go` — new file with RPCError, RPCErrors, ParseRPCErrors
- `netconf/errors_test.go` — new file with 7+ error model tests
- `netconf/capability.go` — modified with 8 new capability constants
- `netconf/capability_test.go` — modified with capability constant validation tests

## Observability Impact

This task adds no background goroutines, I/O, or state — observability is via the type system and error values:

- **`RPCError.Error() string`** is the primary runtime signal; any caller that returns or logs an `RPCError` automatically emits `rpc-error: type=… tag=… severity=… message=…`.
- **`ParseRPCErrors` error path** wraps malformed-XML failures with `ParseRPCErrors: xml decode: …` so callers can distinguish protocol errors from decode failures.
- **Inspection surface**: `go test ./netconf/... -run "TestRPCError|TestParseRPCErrors|TestCapability" -v` exercises every code path; test names encode the scenario.
- **No new failure states** are introduced beyond what encoding/xml already surfaces; `ParseRPCErrors` returning `(nil, nil)` is a first-class observable state (ok reply, no errors).
- **Redaction**: `RPCError.Message` and `RPCError.Info` fields may contain device config fragments; future logging should redact them at levels above DEBUG.
