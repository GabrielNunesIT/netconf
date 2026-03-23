---
estimated_steps: 5
estimated_files: 2
skills_used:
  - test
---

# T02: Implement all 13 operation types, filter types, and datastore model

**Slice:** S02 — Base Protocol Operations & Error Model
**Milestone:** M001

## Description

Implement all 13 RFC 6241 base protocol operations as Go structs with XML tags, plus the shared types (Datastore, Filter, DataReply) used across operations. This is the core deliverable of S02 — defining the XML wire format for every NETCONF RPC.

Every operation struct must carry the NETCONF base namespace `urn:ietf:params:xml:ns:netconf:base:1.0` in its `XMLName` struct tag (not set at runtime — lesson L001 from S01). The `Datastore` type encodes as child elements (`<running/>`, `<candidate/>`, etc.), not attributes. The `Filter` type handles both subtree (innerxml content) and XPath (select attribute) modes via a `type` discriminator attribute.

The 13 operations:
- **Read**: `Get`, `GetConfig`
- **Write**: `EditConfig`, `CopyConfig`, `DeleteConfig`
- **Lock**: `Lock`, `Unlock`
- **Session**: `CloseSession`, `KillSession`
- **Capability-gated**: `Validate`, `Commit`, `DiscardChanges`, `CancelCommit`

## Steps

1. **Create `netconf/operation.go`** with shared types first:
   - `Datastore` struct: `Running *struct{} \`xml:"running,omitempty"\``, `Candidate *struct{} \`xml:"candidate,omitempty"\``, `Startup *struct{} \`xml:"startup,omitempty"\``, `URL string \`xml:"url,omitempty"\``. These encode as child elements within `<source>` or `<target>` wrappers.
   - `Filter` struct: `Type string \`xml:"type,attr,omitempty"\``, `Select string \`xml:"select,attr,omitempty"\``, `Content []byte \`xml:",innerxml"\``. Type is "subtree" or "xpath".
   - `DataReply` struct: `XMLName xml.Name \`xml:"data"\``, `Content []byte \`xml:",innerxml"\``. For decoding `<data>` responses from `RPCReply.Body`.

2. **Add the 13 operation structs** to `netconf/operation.go`. Each must have `XMLName xml.Name \`xml:"urn:ietf:params:xml:ns:netconf:base:1.0 <element-name>"\``. Specific structs:
   - `Get` — has optional `Filter *Filter \`xml:"filter,omitempty"\``
   - `GetConfig` — has `Source Datastore \`xml:"source"\`` and optional `Filter`
   - `EditConfig` — has `Target Datastore \`xml:"target"\``, optional `DefaultOperation string`, optional `TestOption string`, optional `ErrorOption string`, `Config []byte \`xml:",innerxml"\``
   - `CopyConfig` — has `Target Datastore \`xml:"target"\`` and `Source Datastore \`xml:"source"\``
   - `DeleteConfig` — has `Target Datastore \`xml:"target"\``
   - `Lock` — has `Target Datastore \`xml:"target"\``
   - `Unlock` — has `Target Datastore \`xml:"target"\``
   - `CloseSession` — empty body (XMLName only)
   - `KillSession` — has `SessionID uint32 \`xml:"session-id"\``
   - `Validate` — has `Source Datastore \`xml:"source"\``
   - `Commit` — has optional `Confirmed *struct{} \`xml:"confirmed,omitempty"\``, optional `ConfirmTimeout uint32 \`xml:"confirm-timeout,omitempty"\``, optional `Persist string \`xml:"persist,omitempty"\``, optional `PersistID string \`xml:"persist-id,omitempty"\``
   - `DiscardChanges` — empty body
   - `CancelCommit` — has optional `PersistID string \`xml:"persist-id,omitempty"\``

3. **Create `netconf/operation_test.go`** with XML round-trip tests for every operation. For each operation test:
   - Construct the struct with representative field values
   - `xml.Marshal` it and verify the output contains `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"` and the correct element name
   - `xml.Unmarshal` the marshaled bytes back and verify all fields match the original
   - Name tests as `TestGet_MarshalRoundTrip`, `TestGetConfig_MarshalRoundTrip`, etc.

4. **Add shared-type tests** to `netconf/operation_test.go`:
   - `TestDatastore_RunningEncoding` — verify `Datastore{Running: &struct{}{}}` marshals to XML containing `<running></running>` or `<running/>` as a child element
   - `TestDatastore_URLEncoding` — verify `Datastore{URL: "https://example.com/cfg"}` marshals with `<url>` child element
   - `TestFilter_SubtreeRoundTrip` — create Filter with Type="subtree" and Content bytes, marshal/unmarshal, verify Content preserved
   - `TestFilter_XPathRoundTrip` — create Filter with Type="xpath" and Select expression, verify select attribute in output
   - `TestDataReply_DecodeFromBody` — simulate RPCReply.Body containing `<data><config>...</config></data>`, unmarshal into DataReply, verify Content

5. **Add RPC composition test** to `netconf/operation_test.go`:
   - `TestRPC_WithGetConfig_Composition` — marshal a GetConfig into bytes, set as RPC.Body, marshal the full RPC, verify the output is a valid `<rpc><get-config>...</get-config></rpc>` structure. Then unmarshal into RPCReply, decode Body into DataReply. This proves S02 types compose correctly with S01's RPC/RPCReply types.

## Must-Haves

- [ ] All 13 operation structs have `XMLName` with `urn:ietf:params:xml:ns:netconf:base:1.0` namespace in struct tag
- [ ] `Datastore` encodes running/candidate/startup as child elements (not attributes), using `*struct{}` with omitempty
- [ ] `Filter` handles both subtree (innerxml content) and XPath (select attribute) correctly
- [ ] `EditConfig.Config` uses `[]byte \`xml:",innerxml"\`` for arbitrary config XML
- [ ] `KillSession` has `SessionID` field; `CloseSession` has no body fields
- [ ] `Commit` has all optional confirmed-commit fields with omitempty
- [ ] `DataReply` can decode `<data>` content from `RPCReply.Body`
- [ ] Every operation has a marshal/unmarshal round-trip test verifying namespace and field preservation
- [ ] No new dependencies added
- [ ] All existing S01 tests and T01 tests still pass

## Verification

- `go test ./netconf/... -v -count=1` — all tests pass (S01 + T01 + T02 tests)
- `go vet ./...` — clean
- `go test ./netconf/... -run "Test.*_MarshalRoundTrip" -v -count=1` — all 13 operation round-trips pass
- `go test ./netconf/... -run "TestFilter|TestDatastore|TestDataReply" -v -count=1` — shared type tests pass
- `go test ./netconf/... -run "TestRPC_With.*Composition" -v -count=1` — RPC composition test passes

## Inputs

- `netconf/message.go` — `RPC`, `RPCReply`, `NetconfNS` types used in composition tests
- `netconf/errors.go` — `RPCError` type (from T01) referenced in error response context
- `netconf/capability.go` — capability constants (from T01) referenced in doc comments

## Expected Output

- `netconf/operation.go` — new file with all 13 operation structs, Datastore, Filter, DataReply
- `netconf/operation_test.go` — new file with 18+ tests covering all operations and shared types
