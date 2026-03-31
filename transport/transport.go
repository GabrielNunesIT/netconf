// Transport interface and Upgrader interface.

package transport

import "io"

// Transport is the interface that wraps a NETCONF message channel.
//
// A Transport manages a single bidirectional NETCONF session channel. It
// presents an asymmetric pair of streaming primitives: one message at a time
// in each direction. Each call to MsgWriter returns a writer whose Close
// commits (frames and flushes) the complete message. Each call to MsgReader
// returns a reader for exactly one complete NETCONF message.
//
// Framing (EOM `]]>]]>` for base:1.0; RFC 6242 chunked encoding for base:1.1)
// is handled inside the Transport implementation and is invisible to callers.
//
// Implementations must be safe to call from a single goroutine; concurrent
// MsgWriter or concurrent MsgReader calls are not required to be safe.
type Transport interface {
	// MsgReader returns a ReadCloser that delivers exactly one complete
	// NETCONF message. The caller must Close the reader before calling
	// MsgReader again. A non-nil error indicates a permanent transport
	// failure; callers should call Close and discard the transport.
	MsgReader() (io.ReadCloser, error)

	// MsgWriter returns a WriteCloser. The caller writes the complete
	// NETCONF message body and then calls Close to commit (frame and
	// flush) it to the peer. A non-nil error from Write or Close
	// indicates a permanent transport failure.
	MsgWriter() (io.WriteCloser, error)

	// Close tears down the underlying connection and releases all
	// associated resources. After Close, all further calls to MsgReader
	// and MsgWriter must return errors.
	Close() error
}

// Upgrader is an optional interface implemented by transports whose framing
// mode can be switched after the hello exchange.
//
// After both peers have advertised base:1.1 in their hello messages, the
// session layer calls Upgrade() once to switch the transport from EOM framing
// to RFC 6242 chunked framing. Subsequent MsgReader/MsgWriter calls use the
// new framing mode.
type Upgrader interface {
	// Upgrade switches the transport from base:1.0 (EOM) framing to
	// base:1.1 (chunked) framing. It must be called at most once and
	// only after the hello exchange completes.
	Upgrade()
}
