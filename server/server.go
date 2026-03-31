// Package server provides the NETCONF server-side dispatch library.
//
// The central type is Server. Implementers register per-operation handlers via
// RegisterHandler, then call Serve to run the dispatch loop over an established
// Session. The loop reads RPCs, routes each to the matching handler by operation
// XML local name, and sends the reply.
//
// Built-in behaviour:
//   - <close-session> is intercepted before handler lookup — Serve sends
//     <ok/> and returns nil so the caller's goroutine can clean up.
//   - Unregistered operations receive an operation-not-supported rpc-error reply;
//     the error message names the operation so callers can log it verbatim.
//   - Handler errors are converted to well-formed <rpc-error> replies:
//     RPCError values are marshalled directly; other errors become a generic
//     operation-failed rpc-error whose message field carries err.Error().
//
// # Observability
//
// Serve returns a descriptive error when the transport fails or context is
// cancelled; the error wraps the underlying transport/context error so
// callers can log it verbatim or use errors.Is/As.
// Operation-not-supported replies include the operation name in the
// error-message field, making them identifiable in captured traffic.
// Handler dispatch errors include the operation name and message-id in
// the error-message string so production logs can correlate failures.
//
// Redaction note: RPC body may contain device configuration. Do not log at
// INFO or higher in production; log at DEBUG only.
package server

import (
	"context"
	"encoding/xml"
	"fmt"

	netconf "github.com/GabrielNunesIT/netconf"
)

// Handler handles a single NETCONF operation.
//
// Implementations receive the raw Session (for session-level metadata) and the
// parsed RPC (for the message-id and raw body). They are responsible for
// unmarshalling the body themselves — this keeps the server package ignorant
// of the full set of operation types.
//
// Return semantics:
//   - (nil, nil)   → the reply will be <rpc-reply><ok/></rpc-reply>
//   - (body, nil)  → body is embedded verbatim as the inner XML of <rpc-reply>
//   - (_, RPCError) → the RPCError is marshalled and embedded in <rpc-reply>
//   - (_, other)   → a generic operation-failed rpc-error is constructed from
//     err.Error() and embedded in <rpc-reply>
type Handler interface {
	Handle(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error)
}

// StreamHandler is an optional interface that handlers may implement to receive
// the XML decoder positioned at the operation start element. Implementing
// StreamHandler avoids materialising rpc.Body as []byte — the handler decodes
// the operation body directly from the decoder stream.
//
// HandleStream is called with dec positioned such that the next DecodeElement
// call with opStart will consume the complete operation element. The handler
// MUST call dec.DecodeElement (or consume the element completely) before
// returning. The rpc parameter carries the message-id; its Body field is nil.
//
// Return semantics are identical to Handler.Handle.
type StreamHandler interface {
	HandleStream(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC, dec *xml.Decoder, opStart xml.StartElement) ([]byte, error)
}

// HandlerFunc is a function adapter that implements Handler, analogous to
// http.HandlerFunc.
type HandlerFunc func(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error)

// Handle calls f(ctx, sess, rpc).
func (f HandlerFunc) Handle(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
	return f(ctx, sess, rpc)
}

// Server dispatches incoming NETCONF RPCs to registered handlers.
type Server struct {
	handlers map[string]Handler
}

// NewServer returns a Server with an empty handler registry.
func NewServer() *Server {
	return &Server{handlers: make(map[string]Handler)}
}

// RegisterHandler registers h as the handler for operations whose XML local
// name is opName (e.g. "get-config", "edit-config"). A second registration
// for the same opName replaces the first.
func (s *Server) RegisterHandler(opName string, h Handler) {
	s.handlers[opName] = h
}

// Serve runs the RPC dispatch loop over sess until one of:
//   - the transport returns an error (transport failure or context cancellation)
//   - a <close-session> RPC is received (Serve sends <ok/> and returns nil)
//
// Serve uses a streaming decode path: each message is parsed in a single pass
// without materialising an intermediate []byte. Handlers that implement
// StreamHandler receive the decoder positioned at the operation start element
// and decode the body directly (zero body allocation). Plain Handler
// implementations continue to work unchanged — Serve materialises rpc.Body
// for them.
//
// Context cancellation: cancelling ctx signals handlers to stop new work, but
// does NOT by itself unblock the blocking sess.RecvStream call. To stop Serve
// promptly, close the underlying transport (e.g. sess.Close()) in addition to
// cancelling ctx.
//
// Panic handling: if a Handler or StreamHandler panics, the panic propagates
// to the caller of Serve. Use a recover wrapper around Serve if your handlers
// may panic.
//
// For each well-formed RPC, Serve:
//  1. Extracts the message-id and operation name from the streaming decoder.
//  2. Intercepts <close-session>: sends <ok/>, returns nil.
//  3. Looks up the handler by operation name.
//  4. If none found: sends an operation-not-supported rpc-error reply.
//  5. Calls the handler (StreamHandler or Handler) and converts its result to
//     a reply (see Handler docs).
//  6. Sends the reply via sess.Send.
//
// Serve is single-threaded: one handler runs at a time per session.
func (s *Server) Serve(ctx context.Context, sess *netconf.Session) error {
	for {
		rc, err := sess.RecvStream()
		if err != nil {
			return fmt.Errorf("server: Serve: recv: %w", err)
		}

		// Single-pass decode: find the <rpc> start element and extract
		// the message-id attribute, then find the operation start element.
		decoder := xml.NewDecoder(rc)

		msgID, opStart, ok := parseRPCHeader(decoder)
		if !ok {
			// Malformed message — we have no message-id to reply with, so skip.
			_ = rc.Close()
			continue
		}

		opName := opStart.Name.Local

		// Built-in: intercept close-session before handler lookup.
		if opName == "close-session" {
			_ = rc.Close()
			reply := &netconf.RPCReply{
				MessageID: msgID,
				Ok:        &struct{}{},
			}
			if sendErr := sendReply(sess, reply); sendErr != nil {
				return fmt.Errorf("server: Serve: send close-session reply (message-id=%s): %w", msgID, sendErr)
			}
			return nil
		}

		// Dispatch to registered handler or return operation-not-supported.
		h, ok := s.handlers[opName]
		if !ok {
			_ = rc.Close()
			rpcErr := netconf.RPCError{
				Type:     "protocol",
				Tag:      "operation-not-supported",
				Severity: "error",
				Message:  fmt.Sprintf("operation %q is not supported", opName),
			}
			reply, buildErr := buildErrorReply(msgID, rpcErr)
			if buildErr != nil {
				return fmt.Errorf("server: Serve: build operation-not-supported reply (message-id=%s, op=%s): %w", msgID, opName, buildErr)
			}
			if sendErr := sendReply(sess, reply); sendErr != nil {
				return fmt.Errorf("server: Serve: send operation-not-supported reply (message-id=%s, op=%s): %w", msgID, opName, sendErr)
			}
			continue
		}

		// Dispatch: StreamHandler receives the decoder directly (no body
		// materialisation); plain Handler receives a conventional RPC struct.
		rpc := &netconf.RPC{MessageID: msgID}
		var body []byte
		var handlerErr error

		if sh, ok := h.(StreamHandler); ok {
			// Fast path: handler decodes op body from the stream directly.
			body, handlerErr = sh.HandleStream(ctx, sess, rpc, decoder, opStart)
			_ = rc.Close()
		} else {
			// Conventional path: materialise the operation element as []byte
			// so Handler.Handle receives the expected rpc.Body field.
			rpc.Body, err = marshalOpElement(decoder, opStart)
			_ = rc.Close()
			if err != nil {
				// Malformed body — skip without a reply (no meaningful message).
				continue
			}
			body, handlerErr = h.Handle(ctx, sess, rpc)
		}

		var reply *netconf.RPCReply
		switch {
		case handlerErr == nil && body == nil:
			reply = &netconf.RPCReply{
				MessageID: msgID,
				Ok:        &struct{}{},
			}
		case handlerErr == nil:
			reply = &netconf.RPCReply{
				MessageID: msgID,
				Body:      body,
			}
		default:
			// Handler returned an error — produce an rpc-error reply.
			var rpcErr netconf.RPCError
			switch e := handlerErr.(type) {
			case netconf.RPCError:
				rpcErr = e
			default:
				rpcErr = netconf.RPCError{
					Type:     "application",
					Tag:      "operation-failed",
					Severity: "error",
					Message:  fmt.Sprintf("op=%s message-id=%s: %s", opName, msgID, handlerErr.Error()),
				}
			}
			var buildErr error
			reply, buildErr = buildErrorReply(msgID, rpcErr)
			if buildErr != nil {
				return fmt.Errorf("server: Serve: build handler error reply (message-id=%s, op=%s): %w", msgID, opName, buildErr)
			}
		}

		if sendErr := sendReply(sess, reply); sendErr != nil {
			return fmt.Errorf("server: Serve: send reply (message-id=%s, op=%s): %w", msgID, opName, sendErr)
		}
	}
}

// SendNotification marshals n and sends it to the client via sess.
//
// This function is NOT safe for concurrent use with other sends on the same
// session. If notifications are sent from a goroutine other than the Serve
// loop goroutine, the caller must serialize access to sess (e.g. via a
// sync.Mutex). The Serve loop itself sends replies from a single goroutine,
// so calling SendNotification from a separate goroutine requires external
// synchronization.
//
// Error messages include the "server: SendNotification:" prefix for log
// correlation.
func SendNotification(sess *netconf.Session, n *netconf.Notification) error {
	data, err := xml.Marshal(n)
	if err != nil {
		return fmt.Errorf("server: SendNotification: marshal: %w", err)
	}
	if err := sess.Send(data); err != nil {
		return fmt.Errorf("server: SendNotification: send: %w", err)
	}
	return nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// parseRPCHeader reads tokens from dec to find the <rpc> start element
// (extracting the message-id attribute) and then the first child start
// element (the operation). Returns (msgID, opStart, true) on success, or
// ("", zero, false) if the message is malformed.
func parseRPCHeader(dec *xml.Decoder) (msgID string, opStart xml.StartElement, ok bool) {
	// Find the <rpc> start element.
	var rpcStart xml.StartElement
	var foundRPC bool
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", xml.StartElement{}, false
		}
		if se, isStart := tok.(xml.StartElement); isStart {
			rpcStart = se
			foundRPC = true
			break
		}
	}
	if !foundRPC {
		return "", xml.StartElement{}, false
	}

	// Extract message-id from <rpc> attributes.
	for _, attr := range rpcStart.Attr {
		if attr.Name.Local == "message-id" {
			msgID = attr.Value
			break
		}
	}
	if msgID == "" {
		return "", xml.StartElement{}, false
	}

	// Find the first child start element — the operation element.
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", xml.StartElement{}, false
		}
		if se, isStart := tok.(xml.StartElement); isStart {
			return msgID, se, true
		}
		// Skip CharData, ProcInst, etc.
	}
}

// marshalOpElement reads the operation element from dec (starting at opStart)
// and returns it as a complete []byte suitable for rpc.Body.
// The returned bytes include the element's opening tag, body, and closing tag.
func marshalOpElement(dec *xml.Decoder, opStart xml.StartElement) ([]byte, error) {
	// opBody captures the operation element's innerxml. We then wrap it with
	// the start/end tags to reconstruct the full element for rpc.Body.
	type opBody struct {
		Inner []byte `xml:",innerxml"`
	}
	var ob opBody
	if err := dec.DecodeElement(&ob, &opStart); err != nil {
		return nil, err
	}

	// Reconstruct the full element: <opName xmlns="...">innerxml</opName>
	// Use xml.Marshal on a struct with the same XMLName and innerxml.
	type fullOp struct {
		XMLName xml.Name
		Inner   []byte `xml:",innerxml"`
	}
	return xml.Marshal(fullOp{XMLName: opStart.Name, Inner: ob.Inner})
}

// buildErrorReply marshals rpcErr and wraps it as the body of an RPCReply.
func buildErrorReply(msgID string, rpcErr netconf.RPCError) (*netconf.RPCReply, error) {
	errBytes, err := xml.Marshal(rpcErr)
	if err != nil {
		return nil, err
	}
	return &netconf.RPCReply{
		MessageID: msgID,
		Body:      errBytes,
	}, nil
}

// sendReply marshals reply and sends it via sess.Send.
func sendReply(sess *netconf.Session, reply *netconf.RPCReply) error {
	data, err := xml.Marshal(reply)
	if err != nil {
		return fmt.Errorf("marshal reply: %w", err)
	}
	return sess.Send(data)
}
