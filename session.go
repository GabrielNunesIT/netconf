// Session lifecycle: hello exchange, capability negotiation, and framing negotiation.

package netconf

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"

	"github.com/GabrielNunesIT/netconf/transport"
)

// FramingMode describes the active framing layer on a session.
type FramingMode int

const (
	// FramingEOM is base:1.0 end-of-message framing (]]>]]> delimiter).
	FramingEOM FramingMode = iota
	// FramingChunked is base:1.1 chunked framing (RFC 6242 §4.2).
	FramingChunked
)

// Session represents an established NETCONF session. It holds negotiated state
// (session-id, capabilities, framing mode) and the underlying transport.
//
// All exported methods are safe to call after a successful call to
// ClientSession or ServerSession. Concurrent calls are not safe unless the
// caller serialises them.
type Session struct {
	trp        transport.Transport
	sessionID  uint32
	localCaps  CapabilitySet
	remoteCaps CapabilitySet
	framing    FramingMode
}

// SessionID returns the server-assigned session identifier (RFC 6241 §8.1).
// For client sessions this value comes from the server's hello; for server
// sessions it is the value supplied to ServerSession.
func (s *Session) SessionID() uint32 { return s.sessionID }

// LocalCapabilities returns the capabilities advertised by this peer.
func (s *Session) LocalCapabilities() CapabilitySet { return s.localCaps }

// RemoteCapabilities returns the capabilities advertised by the remote peer.
func (s *Session) RemoteCapabilities() CapabilitySet { return s.remoteCaps }

// FramingMode returns the negotiated framing mode: FramingEOM or FramingChunked.
func (s *Session) FramingMode() FramingMode { return s.framing }

// Close closes the underlying transport, releasing all associated resources.
// After Close, the session must not be used.
func (s *Session) Close() error { return s.trp.Close() }

// Send writes a complete NETCONF message to the transport. It is NOT safe for
// concurrent use; callers that require concurrent sends must serialise access
// externally (e.g. with a sync.Mutex).
func (s *Session) Send(msg []byte) error {
	return transport.WriteMsg(s.trp, msg)
}

// Recv reads exactly one complete NETCONF message from the transport. It
// blocks until a full message is available or the transport is closed. It
// must be called from a single goroutine — the client dispatcher goroutine.
func (s *Session) Recv() ([]byte, error) {
	return transport.ReadMsg(s.trp)
}

// RecvStream returns a ReadCloser for the next complete NETCONF message.
//
// Unlike Recv, the message bytes are NOT materialised into a []byte —the
// caller reads directly from the framing-layer buffer. The caller MUST Close
// the returned reader before calling RecvStream (or Recv) again.
//
// This is the low-allocation path for callers (such as the client dispatcher)
// that feed the message bytes directly into an xml.Decoder without needing
// the raw []byte. For callers that do need the raw bytes, use Recv instead.
func (s *Session) RecvStream() (io.ReadCloser, error) {
	return s.trp.MsgReader()
}

// ─── Client-side session establishment ────────────────────────────────────────

// ClientSession performs the NETCONF hello exchange from the client side
// (RFC 6241 §8.1):
//
//  1. Sends a <hello> with localCaps (no session-id) concurrently with reading
//     the server's <hello>. Both must happen simultaneously because the
//     underlying transport may be unbuffered (e.g. io.Pipe).
//  2. Validates that the server hello contains base:1.0 and extracts the
//     session-id and server capabilities.
//  3. Negotiates framing: upgrades to chunked if both peers advertise base:1.1.
//
// Returns an initialised Session on success, or a descriptive error on failure.
// The transport is NOT closed on error; the caller is responsible for cleanup.
// On error, closing the transport will unblock any in-flight send goroutine.
func ClientSession(trp transport.Transport, localCaps CapabilitySet) (*Session, error) {
	// Send our hello in a goroutine so the receive below can unblock the peer's
	// send simultaneously. RFC 6241 §8.1 requires both sides to send hello
	// without waiting for the peer's hello; unbuffered transports require
	// concurrent send+receive to avoid deadlock.
	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- sendHello(trp, localCaps, 0)
	}()

	// Read server hello.
	remote, err := recvHello(trp)
	if err != nil {
		return nil, fmt.Errorf("session: client: receive hello: %w", err)
	}

	// Collect the send result.
	if err := <-sendErrCh; err != nil {
		return nil, fmt.Errorf("session: client: send hello: %w", err)
	}

	remoteCaps := NewCapabilitySet(remote.Capabilities)

	// RFC 6241 §8.1: server hello MUST contain base:1.0.
	if !remoteCaps.Supports10() {
		return nil, fmt.Errorf("session: client: remote hello missing required capability %q", BaseCap10)
	}

	// Session-id is assigned by the server and carried in its hello.
	if remote.SessionID == 0 {
		return nil, errors.New("session: client: server hello missing session-id")
	}

	framing, err := negotiateFraming(trp, localCaps, remoteCaps)
	if err != nil {
		return nil, fmt.Errorf("session: client: framing negotiation: %w", err)
	}

	return &Session{
		trp:        trp,
		sessionID:  remote.SessionID,
		localCaps:  localCaps,
		remoteCaps: remoteCaps,
		framing:    framing,
	}, nil
}

// ─── Server-side session establishment ────────────────────────────────────────

// ServerSession performs the NETCONF hello exchange from the server side:
//
//  1. Sends a <hello> with localCaps and the provided sessionID concurrently
//     with reading the client's <hello>. Both must happen simultaneously
//     because the underlying transport may be unbuffered (e.g. io.Pipe).
//  2. Validates that the client hello contains base:1.0 and extracts the
//     client capabilities.
//  3. Negotiates framing: upgrades to chunked if both peers advertise base:1.1.
//
// Returns an initialised Session on success, or a descriptive error on failure.
// The transport is NOT closed on error; the caller is responsible for cleanup.
// On error, closing the transport will unblock any in-flight send goroutine.
func ServerSession(trp transport.Transport, localCaps CapabilitySet, sessionID uint32) (*Session, error) {
	// Send our hello in a goroutine (same unbuffered-pipe rationale as
	// ClientSession — both peers must send simultaneously).
	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- sendHello(trp, localCaps, sessionID)
	}()

	// Read client hello.
	remote, err := recvHello(trp)
	if err != nil {
		return nil, fmt.Errorf("session: server: receive hello: %w", err)
	}

	// Collect the send result.
	if err := <-sendErrCh; err != nil {
		return nil, fmt.Errorf("session: server: send hello: %w", err)
	}

	remoteCaps := NewCapabilitySet(remote.Capabilities)

	// RFC 6241 §8.1: client hello MUST contain base:1.0.
	if !remoteCaps.Supports10() {
		return nil, fmt.Errorf("session: server: remote hello missing required capability %q", BaseCap10)
	}

	framing, err := negotiateFraming(trp, localCaps, remoteCaps)
	if err != nil {
		return nil, fmt.Errorf("session: server: framing negotiation: %w", err)
	}

	return &Session{
		trp:        trp,
		sessionID:  sessionID,
		localCaps:  localCaps,
		remoteCaps: remoteCaps,
		framing:    framing,
	}, nil
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// sendHello marshals a Hello message and writes it via the transport.
// sessionID == 0 is omitted (omitempty), which is correct for client hellos.
func sendHello(trp transport.Transport, caps CapabilitySet, sessionID uint32) error {
	h := &Hello{
		Capabilities: []string(caps),
		SessionID:    sessionID,
	}
	data, err := xml.Marshal(h)
	if err != nil {
		return fmt.Errorf("xml marshal: %w", err)
	}
	return transport.WriteMsg(trp, data)
}

// recvHello reads one message from the transport and unmarshals it as a Hello.
func recvHello(trp transport.Transport) (*Hello, error) {
	r, err := trp.MsgReader()
	if err != nil {
		return nil, fmt.Errorf("MsgReader: %w", err)
	}
	defer func() { _ = r.Close() }()

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var h Hello
	if err := xml.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("xml unmarshal: %w", err)
	}
	return &h, nil
}

// negotiateFraming determines the framing mode based on advertised capabilities
// and calls Upgrade on the transport if chunked framing is selected.
//
// RFC 6242 §4: use chunked framing if and only if both peers advertise
// base:1.1. Otherwise remain in EOM mode.
func negotiateFraming(trp transport.Transport, local, remote CapabilitySet) (FramingMode, error) {
	if local.Supports11() && remote.Supports11() {
		// Both peers support base:1.1 — switch to chunked framing.
		if u, ok := trp.(transport.Upgrader); ok {
			u.Upgrade()
		} else {
			return FramingEOM, errors.New("framing upgrade required but transport does not implement Upgrader")
		}
		return FramingChunked, nil
	}
	return FramingEOM, nil
}
