---
estimated_steps: 4
estimated_files: 4
skills_used:
  - test
---

# T02: Implement base:1.0 and base:1.1 framers with loopback transport

**Slice:** S01 — Transport & Session Foundation
**Milestone:** M001

## Description

Implement both NETCONF framing modes required by RFC 6242: the base:1.0 end-of-message framer (`]]>]]>` delimiter) and the base:1.1 chunked framer (chunk-encoded messages). Also build the in-process loopback transport that all non-SSH tests will use. The framers must support `Upgrade()` to switch from EOM to chunked after hello exchange, matching the real protocol flow.

## Steps

1. Create `netconf/transport/framer.go` with:
   - `Framer` struct that wraps an `io.ReadWriter` and tracks the current mode (EOM or chunked).
   - `NewFramer(rw io.ReadWriter) *Framer` — starts in EOM mode (base:1.0 is default before hello).
   - `MsgReader() (io.ReadCloser, error)` — in EOM mode, reads until `]]>]]>` delimiter (must handle the delimiter appearing across read boundaries); in chunked mode, reads chunk headers `\n#<size>\n`, reads `<size>` bytes of data, repeats until `\n##\n` end-of-chunks marker.
   - `MsgWriter() (io.WriteCloser, error)` — in EOM mode, on Close writes the message bytes followed by `]]>]]>`; in chunked mode, on Close writes the message as one or more chunks with `\n#<size>\n<data>` followed by `\n##\n`.
   - `Upgrade()` — switches from EOM to chunked mode. Panics or errors if already in chunked mode.
   - Chunked framer must validate: chunk size > 0, chunk size ≤ 4294967295 (max uint32), reject malformed chunk headers.

2. Write `netconf/transport/framer_test.go`:
   - EOM mode: encode a message and verify `]]>]]>` is appended. Decode a message with `]]>]]>` delimiter. Round-trip multiple messages sequentially.
   - EOM edge case: message body containing the literal string `]]>]]>` (should be handled — this is actually illegal in base:1.0, test that reader handles it or document the limitation).
   - Chunked mode: encode a message, verify chunk header format `\n#<size>\n...\n##\n`. Decode a chunked message. Round-trip multiple messages.
   - Chunked edge cases: empty message body, very large message (verify chunk size header correctness), multi-chunk message (if implementation splits large messages), malformed chunk header (non-numeric size, zero size, negative — expect error).
   - Upgrade: start in EOM mode, send/receive a message, call Upgrade(), send/receive in chunked mode — verifying the mode switch works mid-stream.

3. Create `netconf/transport/loopback.go`:
   - `NewLoopback() (client Transport, server Transport)` — creates a pair of connected transports using `io.Pipe()`. Each side gets its own Framer wrapping the pipe ends.
   - Both transports start in EOM mode (pre-hello default).
   - Implement the `Transport` interface from T01's `transport.go`.
   - Include `Upgrade()` method that delegates to the underlying Framer.

4. Write `netconf/transport/loopback_test.go`:
   - Create a loopback pair, write a message from client side, read from server side, verify content matches.
   - Round-trip: client writes, server reads, server writes response, client reads response.
   - Test with both EOM and chunked modes (upgrade mid-test).

## Must-Haves

- [ ] EOM framer correctly delimits messages with `]]>]]>`
- [ ] Chunked framer encodes with `\n#<size>\n<data>\n##\n` format per RFC 6242 §4.2
- [ ] Chunked framer rejects malformed chunk headers (zero size, non-numeric, overflow)
- [ ] `Upgrade()` switches framing mode from EOM to chunked
- [ ] Loopback transport satisfies the `Transport` interface
- [ ] All tests pass including edge cases

## Verification

- `cd /home/user/repos/netconf/.gsd/worktrees/M001 && go test ./netconf/transport/... -v -count=1` — all tests pass
- `go vet ./netconf/transport/...` — no vet warnings

## Inputs

- `netconf/transport/transport.go` — Transport interface definition from T01

## Expected Output

- `netconf/transport/framer.go` — EOM and chunked framers with Upgrade()
- `netconf/transport/framer_test.go` — framer tests with edge cases
- `netconf/transport/loopback.go` — in-process loopback transport pair
- `netconf/transport/loopback_test.go` — loopback round-trip tests

## Observability Impact

**What signals change after this task:**

- `go test ./netconf/transport/... -v` — 27 new tests covering both framing modes. Each test name describes its invariant (e.g. `TestEOM_MsgReader_UnexpectedEOF`, `TestChunked_MsgReader_ZeroSizeChunk_Error`). A failure prints the exact framing error string from the implementation.
- `go test ./netconf/transport/... -run TestChunked_MsgReader -v` — exercises all chunked error paths and prints the exact error string for each malformed header type (zero size, non-numeric, missing delimiter bytes).
- `go test ./netconf/transport/... -run TestLoopback -v` — exercises the loopback transport including EOM→chunked upgrade mid-stream.

**Error strings visible on failure:**
- EOM: `"eom: read: <underlying error>"`, `"eom: unexpected EOF before ]]>]]> delimiter"`, `"eom: write message+delimiter: <underlying error>"`
- Chunked decode: `"chunked: expected '\\n' at chunk start, got '…'"`, `"chunked: chunk size 0 is invalid (RFC 6242 §4.2 requires size ≥ 1)"`, `"chunked: chunk size %q is not a valid uint: …"`, `"chunked: chunk size %d exceeds maximum %d"`
- Chunked encode: `"chunked: write framed message: <underlying error>"`

**What a future agent can observe:**
- All framing errors include the framing mode prefix (`eom:` or `chunked:`) and describe which byte or header was unexpected.
- A closed or errored pipe propagates as a non-nil error from `MsgReader`/`MsgWriter`; no errors are silently swallowed.
- `Upgrade()` panics immediately with `"transport.Framer.Upgrade: already in chunked mode"` if called twice — the panic is observable in `go test` output and covered by `TestUpgrade_PanicsIfCalledTwice`.

