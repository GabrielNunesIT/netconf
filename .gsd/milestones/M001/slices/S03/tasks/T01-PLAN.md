---
estimated_steps: 5
estimated_files: 3
skills_used:
  - test
  - review
---

# T01: Build Client with concurrent RPC dispatcher and Do primitive

**Slice:** S03 — Client Library API
**Milestone:** M001

## Description

Create the `netconf/client` package with the `Client` type and its core `Do(ctx, op)` method. The key engineering challenge is the concurrent dispatcher: a background goroutine that reads `<rpc-reply>` messages from the session, matches them to waiting callers by `message-id`, and delivers results via per-RPC buffered channels.

Before the client can work, `Session` needs narrow `Send`/`Recv` methods — the `trp` field is unexported, and we should not export it or add a `Transport()` accessor. `Send([]byte) error` and `Recv() ([]byte, error)` delegate to `transport.WriteMsg`/`ReadMsg` on the internal transport.

The dispatcher uses a `map[string]chan rpcResult` guarded by `sync.Mutex`. Each pending RPC registers a `make(chan rpcResult, 1)` (buffered to 1 so the dispatcher never blocks even if the caller abandoned the wait due to context cancellation). On transport read error, the dispatcher drains all pending callers with the error and exits.

Message-ids are decimal strings from an `atomic.Uint64` counter, starting at 1.

## Steps

1. **Add `Send` and `Recv` to `Session`** in `netconf/session.go`:
   - `func (s *Session) Send(msg []byte) error` — calls `transport.WriteMsg(s.trp, msg)`.
   - `func (s *Session) Recv() ([]byte, error)` — calls `transport.ReadMsg(s.trp)`.
   - Add import for the transport package if not already present (it is — `github.com/GabrielNunesIT/netconf/transport`).
   - These methods do NOT make Session safe for concurrent use — the client serialises access (Send under a mutex, Recv in a single goroutine).

2. **Create `netconf/client/client.go`** with the `Client` struct:
   ```go
   package client

   type rpcResult struct {
       reply *netconf.RPCReply
       err   error
   }

   type Client struct {
       sess    *netconf.Session
       nextID  atomic.Uint64
       mu      sync.Mutex
       pending map[string]chan rpcResult
       done    chan struct{}
       sendMu  sync.Mutex  // serialises Send calls
       closeOnce sync.Once
       dispatchErr error    // set when dispatcher exits with error
   }
   ```
   - `NewClient(sess *netconf.Session) *Client` — stores session, initialises pending map and done channel, starts the dispatcher goroutine. Returns the Client.
   - The dispatcher goroutine runs `s.recvLoop()` — calls `s.sess.Recv()` in a loop, `xml.Unmarshal` into `RPCReply`, looks up `pending[reply.MessageID]`, sends result on the buffered channel, deletes the entry. On any error from `Recv()`, locks mutex, iterates all pending entries sending the error, clears the map, stores the error in `dispatchErr`, and returns.
   - `Close() error` — closes `done` channel via `sync.Once`, calls `sess.Close()` to unblock the dispatcher's `Recv` call. Returns `sess.Close()` error.
   - `Err() error` — returns `dispatchErr` (the error that caused the dispatcher to exit, or nil).

3. **Implement `Do(ctx context.Context, op any) (*netconf.RPCReply, error)`**:
   - Increment `nextID` (start from 1: `id := s.nextID.Add(1)`), format as decimal string.
   - Marshal `op` via `xml.Marshal` → `opBytes`.
   - Build `netconf.RPC{MessageID: idStr, Body: opBytes}`, marshal → `rpcBytes`.
   - Create `ch := make(chan rpcResult, 1)`.
   - Lock `mu`, check if `done` is closed (return error if so), store `pending[idStr] = ch`, unlock.
   - Lock `sendMu`, call `sess.Send(rpcBytes)`, unlock `sendMu`. On error, remove pending entry, return error.
   - `select { case res := <-ch: return res.reply, res.err; case <-ctx.Done(): }` — on context done, lock `mu`, delete `pending[idStr]`, unlock, return `nil, ctx.Err()`.

4. **Write tests** in `netconf/client/client_test.go`:
   - **Test helper: `newTestPair(t)`** — creates a `transport.NewLoopback()` pair, runs `ClientSession` + `ServerSession` in parallel (using goroutines as established in S01), returns `(clientSession, serverTransport)`. The server transport is used by tests to read RPCs and write replies manually.
   - **`TestDo_SimpleRoundTrip`** — call `Do` with a `CloseSession{}`, server reads the RPC, verifies `message-id` is present, writes back an `RPCReply` with `<ok/>` and matching `message-id`. Client receives the reply.
   - **`TestClient_ConcurrentRPCs`** — two goroutines call `Do` simultaneously. Server reads both RPCs, replies in reverse order. Both callers receive the correct reply matched by `message-id`.
   - **`TestClient_ContextCancel`** — caller cancels context before server replies. `Do` returns `context.Canceled`. (Server can just not reply within a short deadline.)
   - **`TestClient_TransportClose`** — close the server side of the transport. The dispatcher exits with an error. A pending `Do` call receives the transport error.
   - **`TestClient_Close`** — call `Client.Close()`, verify the dispatcher exits cleanly, subsequent `Do` returns an error.
   - **`TestSession_SendRecv`** — unit test: create a loopback pair, send bytes via `Send`, read them with `Recv` on the other end.

5. **Verify**: run `go test ./netconf/client/... -v -count=1`, confirm ≥6 tests pass. Run `go test ./netconf/... -v -count=1` to confirm 92 prior tests still pass. Run `go vet ./...` for clean output.

## Must-Haves

- [ ] `Session.Send([]byte) error` and `Session.Recv() ([]byte, error)` exist and work
- [ ] `Client` struct with dispatcher goroutine that multiplexes replies by message-id
- [ ] `Do(ctx, op)` correctly assigns unique message-ids, marshals RPC, waits for reply
- [ ] Concurrent RPCs with out-of-order replies are matched correctly (proven by test)
- [ ] Context cancellation returns `context.Canceled` (proven by test)
- [ ] Dispatcher exits on transport error, draining all pending callers (proven by test)
- [ ] `Client.Close()` shuts down cleanly (proven by test)
- [ ] No new dependencies

## Verification

- `go test ./netconf/client/... -v -count=1` — ≥6 tests PASS
- `go test ./netconf/... -v -count=1` — 92 tests PASS (regression)
- `go vet ./...` — clean

## Observability Impact

- Signals added: `Do` errors include message-id; dispatcher errors propagate to all pending callers
- How a future agent inspects this: `go test ./netconf/client/... -run "TestClient_ConcurrentRPCs|TestClient_ContextCancel" -v`
- Failure state exposed: `Client.Err()` returns the dispatcher exit error; closed-client errors name the method

## Inputs

- `netconf/session.go` — Session struct with unexported `trp` field; needs Send/Recv methods added
- `netconf/message.go` — RPC and RPCReply types for marshaling/unmarshaling
- `netconf/transport/transport.go` — Transport interface, WriteMsg/ReadMsg helpers
- `netconf/transport/loopback.go` — NewLoopback() for test infrastructure

## Expected Output

- `netconf/session.go` — modified: Send and Recv methods added
- `netconf/client/client.go` — new: Client struct, NewClient, Do, Close, Err, dispatcher
- `netconf/client/client_test.go` — new: ≥6 tests covering Do, concurrency, cancellation, shutdown
