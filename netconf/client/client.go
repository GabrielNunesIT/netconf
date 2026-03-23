// Package client provides the NETCONF client API.
//
// The central type is Client, which wraps a Session and manages a background
// dispatcher goroutine. The dispatcher reads <rpc-reply> messages from the
// transport and delivers them to the matching waiting caller by message-id.
// This design allows multiple goroutines to issue concurrent RPCs over a
// single NETCONF session without external coordination.
//
// Observability: errors returned by Do include the message-id. When the
// dispatcher exits due to a transport error all pending callers receive that
// error immediately. Client.Err() exposes the dispatcher exit error for
// post-mortem inspection.
//
// Typed methods (Get, GetConfig, EditConfig, …) call Do, then check the
// reply via checkReply or checkDataReply. Server-side <rpc-error> elements
// are decoded by ParseRPCErrors and returned as the first RPCError value so
// callers can use errors.As to extract structured error details.
//
// Error messages returned by methods on a closed Client name the method so
// callers can tell whether the session was already closing or an operation
// was in flight when the transport died.
//
// # Observability Impact
//
// After T02 the following signals are added/changed:
//   - Typed method errors always include the operation name in the error chain
//     (e.g. "client: GetConfig: rpc-error: type=… tag=…") so log lines
//     identify both the caller-facing method and the server-side error.
//   - errors.As(err, &netconf.RPCError{}) succeeds whenever a server replied
//     with <rpc-error> — callers can extract Tag, Message, Severity etc.
//   - checkDataReply surfaces XML decode failures as wrapped errors with the
//     text "decode DataReply" — distinguishable from RPC-level errors.
//   - Dial errors chain ssh.Dial / ClientSession / NewClient steps, so the
//     failing layer is always named in the error string.
//   - go test ./netconf/client/... -run TestClient_RPCError -v shows the
//     full rpc-error propagation path through the typed method layer.
//   - go test ./netconf/client/... -run TestClient_SSHLoopback -v proves
//     the full TCP→SSH→NETCONF hello→GetConfig→DataReply stack.
package client

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	ncssh "github.com/GabrielNunesIT/netconf/netconf/transport/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// rpcResult is the value delivered on a per-RPC result channel.
type rpcResult struct {
	reply *netconf.RPCReply
	err   error
}

// Client is a NETCONF client that multiplexes concurrent RPCs over a single
// Session using a background dispatcher goroutine.
//
// All exported methods are safe for concurrent use from multiple goroutines.
type Client struct {
	sess   *netconf.Session
	nextID atomic.Uint64

	mu          sync.Mutex
	pending     map[string]chan rpcResult
	done        chan struct{}
	dispatchErr error // set once when the dispatcher exits with an error

	sendMu    sync.Mutex // serialises Session.Send calls
	closeOnce sync.Once
}

// NewClient creates a Client around sess and starts the background dispatcher
// goroutine. The caller must eventually call Close to release resources.
func NewClient(sess *netconf.Session) *Client {
	c := &Client{
		sess:    sess,
		pending: make(map[string]chan rpcResult),
		done:    make(chan struct{}),
	}
	go c.recvLoop()
	return c
}

// recvLoop is the background dispatcher goroutine. It reads RPCReply messages
// from the session and delivers them to waiting callers by message-id.
// It exits when the transport returns an error (including io.EOF on close).
func (c *Client) recvLoop() {
	for {
		raw, err := c.sess.Recv()
		if err != nil {
			// Transport error or close — drain all pending callers.
			c.mu.Lock()
			c.dispatchErr = err
			for id, ch := range c.pending {
				ch <- rpcResult{err: fmt.Errorf("client: dispatcher exit (message-id %s): %w", id, err)}
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}

		var reply netconf.RPCReply
		if err := xml.Unmarshal(raw, &reply); err != nil {
			// Malformed reply — skip; no caller to notify without a message-id.
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[reply.MessageID]
		if ok {
			delete(c.pending, reply.MessageID)
		}
		c.mu.Unlock()

		if ok {
			// Buffered channel (cap 1): this send never blocks even if the
			// caller abandoned the wait due to context cancellation.
			ch <- rpcResult{reply: &reply}
		}
	}
}

// Do sends the given NETCONF operation to the server and waits for the
// matching reply, honoring ctx for cancellation.
//
// op must be a value that xml.Marshal can serialise into a valid NETCONF
// operation element (e.g. netconf.GetConfig{}, netconf.CloseSession{}).
//
// The returned *RPCReply is non-nil on success. The caller is responsible for
// checking RPCReply.Ok and RPCReply.Body for operation-level errors.
//
// Errors:
//   - context.Canceled / context.DeadlineExceeded — caller cancelled the context
//   - transport errors wrapped with the message-id for diagnosis
//   - "client: closed" if the client was already closed when Do was called
func (c *Client) Do(ctx context.Context, op any) (*netconf.RPCReply, error) {
	// Assign a unique message-id (decimal string, starts at 1).
	idNum := c.nextID.Add(1)
	idStr := fmt.Sprintf("%d", idNum)

	// Marshal the operation element.
	opBytes, err := xml.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("client: Do message-id=%s: marshal operation: %w", idStr, err)
	}

	// Wrap in <rpc message-id="…"> … </rpc>.
	rpcMsg := netconf.RPC{
		MessageID: idStr,
		Body:      opBytes,
	}
	rpcBytes, err := xml.Marshal(rpcMsg)
	if err != nil {
		return nil, fmt.Errorf("client: Do message-id=%s: marshal rpc: %w", idStr, err)
	}

	// Register a buffered reply channel before sending, so the dispatcher
	// can never deliver a reply before we are ready to receive it.
	ch := make(chan rpcResult, 1)

	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, errors.New("client: closed")
	default:
	}
	c.pending[idStr] = ch
	c.mu.Unlock()

	// Send the RPC. Serialise all sends so the transport is used from a
	// single goroutine at a time in the write direction.
	c.sendMu.Lock()
	sendErr := c.sess.Send(rpcBytes)
	c.sendMu.Unlock()

	if sendErr != nil {
		// Remove the pending entry so the dispatcher doesn't try to deliver
		// to a channel that no goroutine is reading.
		c.mu.Lock()
		delete(c.pending, idStr)
		c.mu.Unlock()
		return nil, fmt.Errorf("client: Do message-id=%s: send: %w", idStr, sendErr)
	}

	// Wait for the reply or context cancellation.
	select {
	case res := <-ch:
		return res.reply, res.err
	case <-ctx.Done():
		// Remove the pending entry. The channel is buffered (cap 1), so if the
		// dispatcher has already sent (or sends later), it will not block.
		c.mu.Lock()
		delete(c.pending, idStr)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Close shuts down the client. It closes the done channel (so subsequent Do
// calls return immediately) and then closes the underlying session, which
// unblocks the dispatcher's Recv call.
//
// Close is idempotent; subsequent calls are no-ops. The first call returns the
// error from sess.Close(); subsequent calls return nil.
func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.done)
		closeErr = c.sess.Close()
	})
	return closeErr
}

// Err returns the error that caused the dispatcher goroutine to exit, or nil
// if the dispatcher is still running or exited cleanly via Close.
//
// This method is useful for post-mortem inspection when a Do call returns an
// unexpected transport error and the caller wants to distinguish a network
// failure from a protocol error.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dispatchErr
}

// ── Reply helpers ─────────────────────────────────────────────────────────────

// checkReply inspects a plain <ok/> reply for server-side errors.
//
// Decision flow:
//  1. If reply.Ok is set, return nil — the server acknowledged success.
//  2. Call ParseRPCErrors to scan reply.Body for <rpc-error> elements.
//     If any are found, return the first RPCError as an error (RPCError
//     implements the error interface). Callers can use errors.As to extract
//     the structured RPCError value.
//  3. If parsing failed, return the parse error.
//  4. If Body is non-empty but contains no rpc-error elements, return nil —
//     some servers include informational content in a successful reply.
func checkReply(reply *netconf.RPCReply) error {
	if reply.Ok != nil {
		return nil
	}
	errs, parseErr := netconf.ParseRPCErrors(reply)
	if parseErr != nil {
		return parseErr
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// checkDataReply extracts the <data> element from a get / get-config reply.
//
// It checks for <rpc-error> elements before attempting to unmarshal — this
// ensures server errors propagate as RPCError rather than as XML decode
// failures (which would be misleading).
func checkDataReply(reply *netconf.RPCReply) (*netconf.DataReply, error) {
	errs, parseErr := netconf.ParseRPCErrors(reply)
	if parseErr != nil {
		return nil, parseErr
	}
	if len(errs) > 0 {
		return nil, errs[0]
	}
	var dr netconf.DataReply
	if err := xml.Unmarshal(reply.Body, &dr); err != nil {
		return nil, fmt.Errorf("decode DataReply: %w", err)
	}
	return &dr, nil
}

// ── Typed operation methods ───────────────────────────────────────────────────

// Get retrieves running configuration and state data (RFC 6241 §7.7).
// filter is optional; pass nil to request all data.
func (c *Client) Get(ctx context.Context, filter *netconf.Filter) (*netconf.DataReply, error) {
	reply, err := c.Do(ctx, &netconf.Get{Filter: filter})
	if err != nil {
		return nil, err
	}
	return checkDataReply(reply)
}

// GetConfig retrieves configuration from the specified datastore (RFC 6241 §7.1).
// filter is optional; pass nil to request the full datastore.
func (c *Client) GetConfig(ctx context.Context, source netconf.Datastore, filter *netconf.Filter) (*netconf.DataReply, error) {
	reply, err := c.Do(ctx, &netconf.GetConfig{Source: source, Filter: filter})
	if err != nil {
		return nil, err
	}
	return checkDataReply(reply)
}

// EditConfig loads configuration into the target datastore (RFC 6241 §7.2).
func (c *Client) EditConfig(ctx context.Context, cfg netconf.EditConfig) error {
	reply, err := c.Do(ctx, &cfg)
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// CopyConfig copies an entire configuration datastore (RFC 6241 §7.3).
func (c *Client) CopyConfig(ctx context.Context, cfg netconf.CopyConfig) error {
	reply, err := c.Do(ctx, &cfg)
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// DeleteConfig deletes a configuration datastore (RFC 6241 §7.4).
func (c *Client) DeleteConfig(ctx context.Context, cfg netconf.DeleteConfig) error {
	reply, err := c.Do(ctx, &cfg)
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// Lock locks the target configuration datastore (RFC 6241 §7.5).
func (c *Client) Lock(ctx context.Context, target netconf.Datastore) error {
	reply, err := c.Do(ctx, &netconf.Lock{Target: target})
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// Unlock releases a lock on the target datastore (RFC 6241 §7.6).
func (c *Client) Unlock(ctx context.Context, target netconf.Datastore) error {
	reply, err := c.Do(ctx, &netconf.Unlock{Target: target})
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// CloseSession requests graceful termination of the session (RFC 6241 §7.8).
func (c *Client) CloseSession(ctx context.Context) error {
	reply, err := c.Do(ctx, &netconf.CloseSession{})
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// KillSession forces termination of another session (RFC 6241 §7.9).
func (c *Client) KillSession(ctx context.Context, sessionID uint32) error {
	reply, err := c.Do(ctx, &netconf.KillSession{SessionID: sessionID})
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// Validate validates the specified configuration datastore (RFC 6241 §8.6).
// Requires CapabilityValidate to be advertised by the server.
func (c *Client) Validate(ctx context.Context, source netconf.Datastore) error {
	reply, err := c.Do(ctx, &netconf.Validate{Source: source})
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// Commit commits the candidate configuration (RFC 6241 §8.3).
// If opts is nil a plain <commit/> is issued; otherwise opts is used verbatim.
// Requires CapabilityCandidate.
func (c *Client) Commit(ctx context.Context, opts *netconf.Commit) error {
	if opts == nil {
		opts = &netconf.Commit{}
	}
	reply, err := c.Do(ctx, opts)
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// DiscardChanges reverts the candidate to the running configuration (RFC 6241 §8.3.4).
// Requires CapabilityCandidate.
func (c *Client) DiscardChanges(ctx context.Context) error {
	reply, err := c.Do(ctx, &netconf.DiscardChanges{})
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// CancelCommit cancels an ongoing confirmed commit (RFC 6241 §8.4.9).
// persistID identifies the persistent confirmed commit to cancel; pass ""
// for a non-persistent cancel-commit.
// Requires CapabilityConfirmedCommit.
func (c *Client) CancelCommit(ctx context.Context, persistID string) error {
	reply, err := c.Do(ctx, &netconf.CancelCommit{PersistID: persistID})
	if err != nil {
		return err
	}
	return checkReply(reply)
}

// ── Production constructor ────────────────────────────────────────────────────

// Dial opens a TCP connection to addr, performs the SSH handshake, negotiates
// the NETCONF hello exchange, and returns a ready-to-use Client.
//
// addr must be in "host:port" form (e.g. "192.0.2.1:830").
// config carries SSH authentication credentials and timeouts.
// localCaps declares the capabilities this client advertises in the hello.
//
// ctx is checked for cancellation before the dial is started. The SSH dial
// itself uses config.Timeout for its timeout; ctx is not propagated into the
// SSH layer (golang.org/x/crypto/ssh.Dial does not accept a context).
// This will be improved in a future version when the SSH library gains context
// support.
//
// On any error, all partially-opened resources are cleaned up before returning.
func Dial(ctx context.Context, addr string, config *gossh.ClientConfig, localCaps netconf.CapabilitySet) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("client: Dial %s: context already done: %w", addr, err)
	}

	trp, err := ncssh.Dial(addr, config)
	if err != nil {
		return nil, fmt.Errorf("client: Dial %s: %w", addr, err)
	}

	sess, err := netconf.ClientSession(trp, localCaps)
	if err != nil {
		_ = trp.Close()
		return nil, fmt.Errorf("client: Dial %s: NETCONF hello: %w", addr, err)
	}

	return NewClient(sess), nil
}

