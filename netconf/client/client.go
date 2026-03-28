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
//   - go test ./netconf/client/... -run TestClient_TLSLoopback -v proves
//     the full TCP→TLS→NETCONF hello→GetConfig→DataReply stack.
package client

import (
	"bytes"
	"context"
	cryptotls "crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/monitoring"
	"github.com/GabrielNunesIT/netconf/netconf/nmda"
	"github.com/GabrielNunesIT/netconf/netconf/subscriptions"
	ncssh "github.com/GabrielNunesIT/netconf/netconf/transport/ssh"
	nctls "github.com/GabrielNunesIT/netconf/netconf/transport/tls"
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

	notifCh chan *netconf.Notification // buffered channel for RFC 5277 notification events
}

// NewClient creates a Client around sess and starts the background dispatcher
// goroutine. The caller must eventually call Close to release resources.
func NewClient(sess *netconf.Session) *Client {
	c := &Client{
		sess:    sess,
		pending: make(map[string]chan rpcResult),
		done:    make(chan struct{}),
		notifCh: make(chan *netconf.Notification, 64),
	}
	go c.recvLoop()
	return c
}

// recvLoop is the background dispatcher goroutine. It reads messages from
// the session and dispatches them by type:
//   - <notification> (RFC 5277 namespace) → notifCh
//   - <rpc-reply> → pending map, matched by message-id
//
// It exits when the transport returns an error (including io.EOF on close).
// When it exits, notifCh is closed so receivers can range over it.
func (c *Client) recvLoop() {
	defer close(c.notifCh)
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

		// Peek at the first start element to determine message type.
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		var startElem xml.StartElement
		for {
			tok, tokErr := decoder.Token()
			if tokErr != nil {
				break // malformed — fall through to existing RPCReply path
			}
			if se, ok := tok.(xml.StartElement); ok {
				startElem = se
				break
			}
		}

		// Route <notification> messages to the notification channel.
		if startElem.Name.Space == netconf.NotificationNS && startElem.Name.Local == "notification" {
			var notif netconf.Notification
			if err := xml.Unmarshal(raw, &notif); err != nil {
				// Malformed notification — skip; cannot recover without event-time.
				continue
			}
			// Non-blocking send: drop notification if channel is full to prevent
			// dispatcher stall. Slow receivers lose notifications (documented trade-off).
			select {
			case c.notifCh <- &notif:
			default:
				// Channel full — drop notification to prevent dispatcher stall.
			}
			continue
		}

		// Route <rpc-reply> messages through the pending-map path (unchanged behavior).
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
	idStr := strconv.FormatUint(idNum, 10)

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

// RemoteCapabilities returns the capability URIs advertised by the remote peer
// in the NETCONF hello exchange.
func (c *Client) RemoteCapabilities() netconf.CapabilitySet {
	return c.sess.RemoteCapabilities()
}

// SessionID returns the server-assigned NETCONF session identifier from the
// hello exchange.
func (c *Client) SessionID() uint32 {
	return c.sess.SessionID()
}

// Notifications returns the read-only channel on which the dispatcher delivers
// RFC 5277 <notification> messages. The channel is buffered (capacity 64).
// It is closed when the dispatcher exits (transport error or Close).
//
// Callers should drain the channel promptly — if it fills up, excess
// notifications are dropped silently to prevent the dispatcher from stalling.
func (c *Client) Notifications() <-chan *netconf.Notification {
	return c.notifCh
}

// Subscribe sends a <create-subscription> RPC (RFC 5277 §2.1.1) and returns the
// notification channel. The channel is the same as Notifications() — it is
// created at Client construction time, not at Subscribe time, ensuring
// notifications sent before Subscribe returns are not lost.
//
// On success, the caller should read from the returned channel. On error,
// the subscription was not established and no notifications will be delivered.
func (c *Client) Subscribe(ctx context.Context, sub netconf.CreateSubscription) (<-chan *netconf.Notification, error) {
	reply, err := c.Do(ctx, &sub)
	if err != nil {
		return nil, fmt.Errorf("client: Subscribe: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return nil, fmt.Errorf("client: Subscribe: %w", err)
	}
	return c.notifCh, nil
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

// PartialLock locks a subset of the configuration identified by XPath select
// expressions (RFC 5717 §2.1). Requires CapabilityPartialLock.
//
// selects is a list of XPath 1.0 expressions that identify the configuration
// nodes to lock. On success, the device returns a lock-id and the list of
// locked node canonical XPath expressions.
//
// # Observability Impact
//
// Errors include "client: PartialLock:" prefix, so log lines identify the
// operation. A server-side <rpc-error> propagates as netconf.RPCError via
// errors.As, carrying Tag, Type, Severity, and Message fields. An XML decode
// failure on the reply body surfaces as a wrapped "decode PartialLockReply"
// error distinguishable from RPC-level errors. Inspection:
//
//	go test ./netconf/client/... -run TestClient_PartialLock -v
//	go test ./netconf/conformance/... -run TestConformance_PartialLock -v
func (c *Client) PartialLock(ctx context.Context, selects []string) (*netconf.PartialLockReply, error) {
	reply, err := c.Do(ctx, &netconf.PartialLock{Select: selects})
	if err != nil {
		return nil, fmt.Errorf("client: PartialLock: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return nil, fmt.Errorf("client: PartialLock: %w", err)
	}
	var plr netconf.PartialLockReply
	if err := xml.Unmarshal(reply.Body, &plr); err != nil {
		return nil, fmt.Errorf("client: PartialLock: decode PartialLockReply: %w", err)
	}
	return &plr, nil
}

// PartialUnlock releases a partial lock previously acquired via PartialLock
// (RFC 5717 §2.2). Requires CapabilityPartialLock.
//
// lockID must be the lock-id returned by the successful PartialLock call.
//
// # Observability Impact
//
// Errors include "client: PartialUnlock:" prefix. Server-side <rpc-error>
// propagates as netconf.RPCError via errors.As. Inspection:
//
//	go test ./netconf/client/... -run TestClient_PartialUnlock -v
//	go test ./netconf/conformance/... -run TestConformance_PartialUnlock -v
func (c *Client) PartialUnlock(ctx context.Context, lockID uint32) error {
	reply, err := c.Do(ctx, &netconf.PartialUnlock{LockID: lockID})
	if err != nil {
		return fmt.Errorf("client: PartialUnlock: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return fmt.Errorf("client: PartialUnlock: %w", err)
	}
	return nil
}

// GetSchema retrieves a schema document via the get-schema RPC (RFC 6022 §3).
//
// The server must advertise the ietf-netconf-monitoring capability
// (monitoring.CapabilityURI) in its hello. req identifies the schema by
// Identifier (required), Version (optional), and Format (optional; e.g. "yang").
//
// On success, the raw schema bytes (YANG text, YIN XML, or XSD) are returned.
// The returned []byte is the innerxml content of the <data> element in the
// server's rpc-reply — typically a YANG module or XSD document.
//
// # Observability Impact
//
// Errors include the "client: GetSchema:" prefix, so log lines identify the
// operation. A server-side <rpc-error> propagates as netconf.RPCError via
// errors.As, carrying Tag, Type, Severity, and Message fields. An XML decode
// failure on the <data> body surfaces as a wrapped
// "client: GetSchema: decode GetSchemaReply:" error, distinguishable from
// RPC-level errors. Inspection:
//
//	go test ./netconf/client/... -run TestClient_GetSchema -v
func (c *Client) GetSchema(ctx context.Context, req *monitoring.GetSchemaRequest) ([]byte, error) {
	reply, err := c.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("client: GetSchema: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return nil, fmt.Errorf("client: GetSchema: %w", err)
	}
	var gsr monitoring.GetSchemaReply
	if err := xml.Unmarshal(reply.Body, &gsr); err != nil {
		return nil, fmt.Errorf("client: GetSchema: decode GetSchemaReply: %w", err)
	}
	return gsr.Content, nil
}

// ── RFC 8526 NMDA operations ──────────────────────────────────────────────────

// GetData retrieves data from any NMDA datastore (RFC 8526 §3.1).
//
// Unlike GetConfig (which only accesses the running datastore), GetData can
// retrieve data from any NMDA datastore including operational, intended, etc.
//
// # Observability Impact
//
// Errors include "client: GetData:" prefix per P010. A server-side
// <rpc-error> propagates as netconf.RPCError via errors.As. A reply body
// decode failure surfaces as a wrapped "decode DataReply" error.
func (c *Client) GetData(ctx context.Context, req nmda.GetData) (*netconf.DataReply, error) {
	reply, err := c.Do(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("client: GetData: %w", err)
	}
	dr, err := checkDataReply(reply)
	if err != nil {
		return nil, fmt.Errorf("client: GetData: %w", err)
	}
	return dr, nil
}

// EditData applies configuration changes to a writable NMDA datastore
// (RFC 8526 §3.2).
//
// # Observability Impact
//
// Errors include "client: EditData:" prefix per P010.
func (c *Client) EditData(ctx context.Context, req nmda.EditData) error {
	reply, err := c.Do(ctx, &req)
	if err != nil {
		return fmt.Errorf("client: EditData: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return fmt.Errorf("client: EditData: %w", err)
	}
	return nil
}

// ── RFC 8639 subscribed notifications ────────────────────────────────────────

// EstablishSubscription sends an establish-subscription RPC (RFC 8639 §2.4.1)
// and returns the server-assigned subscription ID and the notification channel.
//
// The notification channel is the same as Notifications() — it is created at
// Client construction time and delivers all RFC 5277 <notification> messages,
// including subscription lifecycle notifications (subscription-started,
// subscription-modified, subscription-terminated, subscription-killed).
//
// On success, the server reply is decoded to extract the subscription ID.
// Callers should read from the returned channel to receive subscription events.
//
// # Observability Impact
//
// Errors include "client: EstablishSubscription:" prefix. A server-side
// <rpc-error> propagates as netconf.RPCError via errors.As. A reply body
// decode failure surfaces as a wrapped "decode EstablishSubscriptionReply"
// error.
func (c *Client) EstablishSubscription(ctx context.Context, req subscriptions.EstablishSubscriptionRequest) (subscriptions.SubscriptionID, <-chan *netconf.Notification, error) {
	reply, err := c.Do(ctx, &req)
	if err != nil {
		return 0, nil, fmt.Errorf("client: EstablishSubscription: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return 0, nil, fmt.Errorf("client: EstablishSubscription: %w", err)
	}
	var esr subscriptions.EstablishSubscriptionReply
	if err := xml.Unmarshal(reply.Body, &esr); err != nil {
		return 0, nil, fmt.Errorf("client: EstablishSubscription: decode EstablishSubscriptionReply: %w", err)
	}
	return esr.ID, c.notifCh, nil
}

// ModifySubscription sends a modify-subscription RPC (RFC 8639 §2.4.2).
// It changes parameters (filter, stop-time) of an existing subscription.
//
// # Observability Impact
//
// Errors include "client: ModifySubscription:" prefix per P010.
func (c *Client) ModifySubscription(ctx context.Context, req subscriptions.ModifySubscriptionRequest) error {
	reply, err := c.Do(ctx, &req)
	if err != nil {
		return fmt.Errorf("client: ModifySubscription: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return fmt.Errorf("client: ModifySubscription: %w", err)
	}
	return nil
}

// DeleteSubscription sends a delete-subscription RPC (RFC 8639 §2.4.3).
// It gracefully terminates a subscription owned by this session.
//
// # Observability Impact
//
// Errors include "client: DeleteSubscription:" prefix per P010.
func (c *Client) DeleteSubscription(ctx context.Context, id subscriptions.SubscriptionID) error {
	reply, err := c.Do(ctx, &subscriptions.DeleteSubscription{ID: id})
	if err != nil {
		return fmt.Errorf("client: DeleteSubscription: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return fmt.Errorf("client: DeleteSubscription: %w", err)
	}
	return nil
}

// KillSubscription sends a kill-subscription RPC (RFC 8639 §2.4.4).
// It forcibly terminates any subscription regardless of ownership. Typically
// used by administrators.
//
// reason is an optional human-readable reason for the termination; pass ""
// to omit it.
//
// # Observability Impact
//
// Errors include "client: KillSubscription:" prefix per P010.
func (c *Client) KillSubscription(ctx context.Context, id subscriptions.SubscriptionID, reason string) error {
	reply, err := c.Do(ctx, &subscriptions.KillSubscription{ID: id, Reason: reason})
	if err != nil {
		return fmt.Errorf("client: KillSubscription: %w", err)
	}
	if err := checkReply(reply); err != nil {
		return fmt.Errorf("client: KillSubscription: %w", err)
	}
	return nil
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

// ── Production constructors ───────────────────────────────────────────────────

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

// DialTLS opens a TCP connection to addr, performs the TLS handshake with
// mutual X.509 authentication, negotiates the NETCONF hello exchange, and
// returns a ready-to-use Client.
//
// addr must be in "host:port" form (e.g. "192.0.2.1:6513").
// config must supply the client certificate and the server CA pool for mutual
// TLS as required by RFC 7589.
// localCaps declares the capabilities this client advertises in the hello.
//
// ctx is checked for cancellation before the dial is started; TLS dialing
// itself is not context-aware (Go's stdlib crypto/tls.Dial does not accept
// a context). On any error, all partially-opened resources are cleaned up.
//
// # Observability Impact
//
// Errors from DialTLS carry a "client: DialTLS <addr>:" prefix so log lines
// identify the layer and the target address. Failures in the three dial steps
// are distinguishable:
//   - "client: DialTLS <addr>: context already done: …" — ctx was cancelled
//   - "client: DialTLS <addr>: tls client: dial <addr>: …" — TLS handshake or
//     TCP-connect failed (underlying error names cert verify failure, refused
//     connection, etc.)
//   - "client: DialTLS <addr>: NETCONF hello: …" — hello exchange failed after
//     TLS succeeded
//
// Inspection: `go test ./netconf/client/... -v -run TestClient_TLSLoopback`
// prints the full TLS→hello→GetConfig stack.
func DialTLS(ctx context.Context, addr string, config *cryptotls.Config, localCaps netconf.CapabilitySet) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("client: DialTLS %s: context already done: %w", addr, err)
	}

	trp, err := nctls.Dial(addr, config)
	if err != nil {
		return nil, fmt.Errorf("client: DialTLS %s: %w", addr, err)
	}

	sess, err := netconf.ClientSession(trp, localCaps)
	if err != nil {
		_ = trp.Close()
		return nil, fmt.Errorf("client: DialTLS %s: NETCONF hello: %w", addr, err)
	}

	return NewClient(sess), nil
}

// ─── Call Home (RFC 8071) ─────────────────────────────────────────────────────

// AcceptCallHomeSSH listens on ln for an incoming TCP connection from a
// NETCONF server performing SSH call home (RFC 8071 §3). It accepts the first
// connection, runs the SSH client protocol, negotiates the NETCONF hello
// exchange, and returns a ready-to-use Client.
//
// In call home, the NETCONF server initiates TCP to the NETCONF client. The
// SSH client/server roles are unchanged — this function runs the SSH client
// protocol over the accepted connection.
//
// ln must already be bound and listening (the caller creates net.Listen before
// calling AcceptCallHomeSSH). The caller is responsible for closing ln after
// use. For a server that accepts multiple call-home connections, call
// AcceptCallHomeSSH in a loop.
//
// localCaps declares the capabilities this client advertises in the hello.
//
// ctx is checked for cancellation before Accept is called. If ctx is already
// done, AcceptCallHomeSSH returns immediately with an error. To cancel a
// blocked Accept, the caller must close ln — the same pattern as the stdlib
// net.Listener.
func AcceptCallHomeSSH(ctx context.Context, ln net.Listener, config *gossh.ClientConfig, localCaps netconf.CapabilitySet) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("client: AcceptCallHomeSSH: context already done: %w", err)
	}

	conn, err := ln.Accept()
	if err != nil {
		return nil, fmt.Errorf("client: AcceptCallHomeSSH: accept: %w", err)
	}

	trp, err := ncssh.DialConn(conn, conn.RemoteAddr().String(), config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("client: AcceptCallHomeSSH: SSH handshake: %w", err)
	}

	sess, err := netconf.ClientSession(trp, localCaps)
	if err != nil {
		_ = trp.Close()
		return nil, fmt.Errorf("client: AcceptCallHomeSSH: NETCONF hello: %w", err)
	}

	return NewClient(sess), nil
}

// AcceptCallHomeTLS listens on ln for an incoming TCP connection from a
// NETCONF server performing TLS call home (RFC 8071 §4). It accepts the first
// connection, runs the TLS client protocol, negotiates the NETCONF hello
// exchange, and returns a ready-to-use Client.
//
// In call home, the NETCONF server initiates TCP to the NETCONF client. The
// TLS client/server roles are unchanged — this function runs the TLS client
// protocol over the accepted connection.
//
// ln must already be bound and listening. The caller is responsible for
// closing ln. For a server that accepts multiple call-home connections, call
// AcceptCallHomeTLS in a loop.
//
// localCaps declares the capabilities this client advertises in the hello.
//
// ctx is checked for cancellation before Accept is called. To cancel a
// blocked Accept, the caller must close ln.
func AcceptCallHomeTLS(ctx context.Context, ln net.Listener, config *cryptotls.Config, localCaps netconf.CapabilitySet) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("client: AcceptCallHomeTLS: context already done: %w", err)
	}

	conn, err := ln.Accept()
	if err != nil {
		return nil, fmt.Errorf("client: AcceptCallHomeTLS: accept: %w", err)
	}

	tlsConn := cryptotls.Client(conn, config)
	if err := tlsConn.Handshake(); err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("client: AcceptCallHomeTLS: TLS handshake: %w", err)
	}

	trp := nctls.NewClientTransport(tlsConn)

	sess, err := netconf.ClientSession(trp, localCaps)
	if err != nil {
		_ = trp.Close()
		return nil, fmt.Errorf("client: AcceptCallHomeTLS: NETCONF hello: %w", err)
	}

	return NewClient(sess), nil
}
