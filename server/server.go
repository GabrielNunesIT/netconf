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
// # Observability Impact
//
// After T01 the following signals are available:
//   - Serve returns a descriptive error when the transport fails or context is
//     cancelled; the error wraps the underlying transport/context error so
//     callers can log it verbatim or use errors.Is/As.
//   - operation-not-supported replies include the operation name in the
//     error-message field, making them identifiable in captured traffic.
//   - handler dispatch errors include the operation name and message-id in
//     the error-message string so production logs can correlate failures.
//   - go test ./netconf/server/... -run TestServer -v shows each dispatch
//     scenario as a distinct named test case.
//   - go test ./netconf/server/... -run TestServer_UnknownOperation -v
//     prints the operation name in the captured rpc-error body.
//
// Redaction note: RPC body may contain device configuration. Do not log at
// INFO or higher in production; log at DEBUG only.
package server

import (
	"bytes"
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
//   - sess.Recv returns an error (transport failure or context cancellation)
//   - a <close-session> RPC is received (Serve sends <ok/> and returns nil)
//
// For each well-formed RPC, Serve:
//  1. Extracts the operation name from the first XML element in the RPC body.
//  2. Intercepts <close-session>: sends <ok/>, returns nil.
//  3. Looks up the handler by operation name.
//  4. If none found: sends an operation-not-supported rpc-error reply.
//  5. Calls the handler and converts its result to a reply (see Handler docs).
//  6. Sends the reply via sess.Send.
//
// Serve is single-threaded: one handler runs at a time per session.
func (s *Server) Serve(ctx context.Context, sess *netconf.Session) error {
	for {
		raw, err := sess.Recv()
		if err != nil {
			return fmt.Errorf("server: Serve: recv: %w", err)
		}

		var rpc netconf.RPC
		if err := xml.Unmarshal(raw, &rpc); err != nil {
			// Malformed message — we have no message-id to reply with, so skip.
			continue
		}

		opName := firstElementName(rpc.Body)

		// Built-in: intercept close-session before handler lookup.
		if opName == "close-session" {
			reply := &netconf.RPCReply{
				MessageID: rpc.MessageID,
				Ok:        &struct{}{},
			}
			if sendErr := sendReply(sess, reply); sendErr != nil {
				return fmt.Errorf("server: Serve: send close-session reply (message-id=%s): %w", rpc.MessageID, sendErr)
			}
			return nil
		}

		// Dispatch to registered handler or return operation-not-supported.
		h, ok := s.handlers[opName]
		if !ok {
			rpcErr := netconf.RPCError{
				Type:     "protocol",
				Tag:      "operation-not-supported",
				Severity: "error",
				Message:  fmt.Sprintf("operation %q is not supported", opName),
			}
			reply, buildErr := buildErrorReply(rpc.MessageID, rpcErr)
			if buildErr != nil {
				return fmt.Errorf("server: Serve: build operation-not-supported reply (message-id=%s, op=%s): %w", rpc.MessageID, opName, buildErr)
			}
			if sendErr := sendReply(sess, reply); sendErr != nil {
				return fmt.Errorf("server: Serve: send operation-not-supported reply (message-id=%s, op=%s): %w", rpc.MessageID, opName, sendErr)
			}
			continue
		}

		// Call handler.
		body, handlerErr := h.Handle(ctx, sess, &rpc)

		var reply *netconf.RPCReply
		switch {
		case handlerErr == nil && body == nil:
			reply = &netconf.RPCReply{
				MessageID: rpc.MessageID,
				Ok:        &struct{}{},
			}
		case handlerErr == nil:
			reply = &netconf.RPCReply{
				MessageID: rpc.MessageID,
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
					Message:  fmt.Sprintf("op=%s message-id=%s: %s", opName, rpc.MessageID, handlerErr.Error()),
				}
			}
			var buildErr error
			reply, buildErr = buildErrorReply(rpc.MessageID, rpcErr)
			if buildErr != nil {
				return fmt.Errorf("server: Serve: build handler error reply (message-id=%s, op=%s): %w", rpc.MessageID, opName, buildErr)
			}
		}

		if sendErr := sendReply(sess, reply); sendErr != nil {
			return fmt.Errorf("server: Serve: send reply (message-id=%s, op=%s): %w", rpc.MessageID, opName, sendErr)
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

// firstElementName returns the XML local name of the first start element found
// in b, or "" if b is empty or contains no start element.
func firstElementName(b []byte) string {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
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
