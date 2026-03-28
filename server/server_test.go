package server_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/client"
	"github.com/GabrielNunesIT/netconf/server"
	"github.com/GabrielNunesIT/netconf/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// testCaps is a minimal base:1.0-only capability set.
var testCaps = netconf.NewCapabilitySet([]string{netconf.BaseCap10})

// newTestPair establishes a NETCONF session pair over an in-process loopback
// and returns the client-side and server-side sessions.
func newTestPair(t *testing.T) (clientSess *netconf.Session, serverSess *netconf.Session) {
	t.Helper()

	clientT, serverT := transport.NewLoopback()
	t.Cleanup(func() {
		clientT.Close()
		serverT.Close()
	})

	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	cliCh := make(chan sessResult, 1)
	srvCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, testCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, testCaps, 1)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	return cliRes.sess, srvRes.sess
}

// sendRPC marshals and sends an RPC with the given message-id and operation body.
func sendRPC(t *testing.T, sess *netconf.Session, msgID string, opBody []byte) {
	t.Helper()
	rpc := &netconf.RPC{
		MessageID: msgID,
		Body:      opBody,
	}
	data, err := xml.Marshal(rpc)
	require.NoError(t, err, "marshal RPC")
	require.NoError(t, sess.Send(data), "send RPC")
}

// recvReply receives and unmarshals one RPCReply from sess.
func recvReply(t *testing.T, sess *netconf.Session) *netconf.RPCReply {
	t.Helper()
	raw, err := sess.Recv()
	require.NoError(t, err, "recv reply")
	var reply netconf.RPCReply
	require.NoError(t, xml.Unmarshal(raw, &reply), "unmarshal RPCReply")
	return &reply
}

// runServe starts srv.Serve in a goroutine and returns a channel that receives
// the Serve return value when it exits.
func runServe(t *testing.T, srv *server.Server, sess *netconf.Session) chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(context.Background(), sess)
	}()
	return done
}

// sendCloseSession sends a close-session RPC, reads the ok reply, and waits
// for Serve to return. The loopback pipes are synchronous: Serve's Send for
// the ok reply blocks until the client reads, so we must read before waiting
// on serveDone.
func sendCloseSession(t *testing.T, clientSess *netconf.Session, serveDone chan error) {
	t.Helper()
	sendRPC(t, clientSess, "close", []byte(`<close-session xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"/>`))
	// Must read the <ok/> reply before waiting for Serve — otherwise Serve's
	// sess.Send blocks indefinitely on the synchronous pipe.
	closeReply := recvReply(t, clientSess)
	assert.NotNil(t, closeReply.Ok, "close-session reply must be <ok/>")
	select {
	case err := <-serveDone:
		require.NoError(t, err, "Serve must return nil after close-session")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after close-session")
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestServer_DispatchesToRegisteredHandler verifies that an RPC for a
// registered operation is routed to the correct handler and its body is
// returned in the reply.
func TestServer_DispatchesToRegisteredHandler(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	srv := server.NewServer()
	const responseBody = `<data><config/></data>`
	srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(responseBody), nil
		},
	))

	serveDone := runServe(t, srv, serverSess)

	// Client sends a get-config RPC.
	sendRPC(t, clientSess, "42", []byte(`<get-config xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><source><running/></source></get-config>`))

	reply := recvReply(t, clientSess)
	assert.Equal(t, "42", reply.MessageID, "message-id must echo back")
	assert.Contains(t, string(reply.Body), "<data>", "reply body must contain <data>")
	assert.Contains(t, string(reply.Body), "<config/>", "reply body must contain <config/>")
	assert.Nil(t, reply.Ok, "ok must not be set when body is non-nil")

	sendCloseSession(t, clientSess, serveDone)
}

// TestServer_UnknownOperation_ReturnsError verifies that an RPC for an
// unregistered operation name produces an operation-not-supported rpc-error.
func TestServer_UnknownOperation_ReturnsError(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	srv := server.NewServer()
	// No handlers registered.
	serveDone := runServe(t, srv, serverSess)

	sendRPC(t, clientSess, "1", []byte(`<frobnicate xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"/>`))

	reply := recvReply(t, clientSess)
	assert.Equal(t, "1", reply.MessageID)
	assert.Nil(t, reply.Ok, "must not be ok for unknown operation")

	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err, "ParseRPCErrors must succeed")
	require.Len(t, errs, 1, "exactly one rpc-error expected")
	assert.Equal(t, "operation-not-supported", errs[0].Tag)
	assert.Equal(t, "protocol", errs[0].Type)
	assert.Contains(t, errs[0].Message, "frobnicate",
		"error message must name the unrecognised operation")

	sendCloseSession(t, clientSess, serveDone)
}

// TestServer_HandlerRPCError_PropagatesAsReply verifies that a handler
// returning an RPCError produces a well-formed <rpc-error> reply with the
// same fields.
func TestServer_HandlerRPCError_PropagatesAsReply(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	srv := server.NewServer()
	handlerErr := netconf.RPCError{
		Type:     "application",
		Tag:      "invalid-value",
		Severity: "error",
		Message:  "the value 'x' is not valid",
	}
	srv.RegisterHandler("edit-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, handlerErr
		},
	))

	serveDone := runServe(t, srv, serverSess)

	sendRPC(t, clientSess, "7", []byte(`<edit-config xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><target><running/></target><config/></edit-config>`))

	reply := recvReply(t, clientSess)
	assert.Equal(t, "7", reply.MessageID)
	assert.Nil(t, reply.Ok, "must not be ok on error")

	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	require.Len(t, errs, 1)
	assert.Equal(t, "application", errs[0].Type)
	assert.Equal(t, "invalid-value", errs[0].Tag)
	assert.Equal(t, "error", errs[0].Severity)
	assert.Equal(t, "the value 'x' is not valid", errs[0].Message)

	sendCloseSession(t, clientSess, serveDone)
}

// TestServer_CloseSession_TerminatesServeLoop verifies that a <close-session>
// RPC causes Serve to return nil after sending an <ok/> reply.
func TestServer_CloseSession_TerminatesServeLoop(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	srv := server.NewServer()
	serveDone := runServe(t, srv, serverSess)

	sendRPC(t, clientSess, "5", []byte(`<close-session xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"/>`))

	// Client must receive an <ok/> reply — read it before waiting on Serve
	// (synchronous pipe: Serve's Send blocks until client reads).
	reply := recvReply(t, clientSess)
	assert.Equal(t, "5", reply.MessageID, "message-id must echo back")
	assert.NotNil(t, reply.Ok, "close-session reply must be <ok/>")

	// Serve must return nil.
	select {
	case err := <-serveDone:
		require.NoError(t, err, "Serve must return nil after close-session")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after close-session")
	}
}

// TestServer_HandlerReturnsOk verifies that a handler returning (nil, nil)
// causes the server to send an <ok/> reply.
func TestServer_HandlerReturnsOk(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	srv := server.NewServer()
	srv.RegisterHandler("commit", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil
		},
	))

	serveDone := runServe(t, srv, serverSess)

	sendRPC(t, clientSess, "3", []byte(`<commit xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"/>`))

	reply := recvReply(t, clientSess)
	assert.Equal(t, "3", reply.MessageID)
	assert.NotNil(t, reply.Ok, "handler returning (nil, nil) must produce <ok/>")

	sendCloseSession(t, clientSess, serveDone)
}

// TestServer_HandlerNonRPCError_ProducesOperationFailed verifies that a
// handler returning a plain error produces an operation-failed rpc-error.
func TestServer_HandlerNonRPCError_ProducesOperationFailed(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	srv := server.NewServer()
	srv.RegisterHandler("get", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, plainError("something went wrong internally")
		},
	))

	serveDone := runServe(t, srv, serverSess)

	sendRPC(t, clientSess, "8", []byte(`<get xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"/>`))

	reply := recvReply(t, clientSess)
	assert.Equal(t, "8", reply.MessageID)
	assert.Nil(t, reply.Ok)

	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	require.Len(t, errs, 1)
	assert.Equal(t, "operation-failed", errs[0].Tag)
	assert.Equal(t, "application", errs[0].Type)
	assert.Contains(t, errs[0].Message, "something went wrong internally")

	sendCloseSession(t, clientSess, serveDone)
}

// ── observability diagnostic ──────────────────────────────────────────────────

// TestServer_UnknownOperation_ErrorMessageNamesOperation is the observability
// diagnostic check: the operation name must appear in the rpc-error message.
func TestServer_UnknownOperation_ErrorMessageNamesOperation(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	srv := server.NewServer()
	serveDone := runServe(t, srv, serverSess)

	const opName = "no-such-operation"
	sendRPC(t, clientSess, "11", wrapOp(opName))

	reply := recvReply(t, clientSess)
	errs, err := netconf.ParseRPCErrors(reply)
	require.NoError(t, err)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, opName,
		"error-message must contain the unrecognised operation name %q", opName)

	sendCloseSession(t, clientSess, serveDone)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// plainError is a minimal error type for non-RPCError handler error tests.
type plainError string

func (e plainError) Error() string { return string(e) }

// wrapOp wraps an operation name as a minimal NETCONF element for use in
// sendRPC. The resulting body is `<opName xmlns="…"/>`.
func wrapOp(opName string) []byte {
	var buf bytes.Buffer
	buf.WriteString(`<`)
	buf.WriteString(opName)
	buf.WriteString(` xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"/>`)
	return buf.Bytes()
}

// ── SendNotification tests ────────────────────────────────────────────────────

// TestSendNotification verifies that SendNotification marshals a Notification
// and delivers it over the session transport. The client side receives the raw
// bytes, unmarshals them as a netconf.Notification, and asserts the EventTime
// and Body fields match.
//
// The send runs in a goroutine because the loopback transport is synchronous:
// sess.Send blocks until the other end reads, so client Recv and server Send
// must happen concurrently.
func TestSendNotification(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	const eventTime = "2026-01-01T00:00:00Z"
	notif := &netconf.Notification{
		EventTime: eventTime,
		Body:      []byte(`<test-event/>`),
	}

	// Send from a goroutine — synchronous pipe: Send blocks until client reads.
	sendErr := make(chan error, 1)
	go func() {
		sendErr <- server.SendNotification(serverSess, notif)
	}()

	// Receive and unmarshal on the client side.
	raw, err := clientSess.Recv()
	require.NoError(t, err, "client Recv must succeed")

	// Wait for send to complete.
	require.NoError(t, <-sendErr, "SendNotification must succeed")

	var got netconf.Notification
	require.NoError(t, xml.Unmarshal(raw, &got), "unmarshal Notification must succeed")

	assert.Equal(t, eventTime, got.EventTime, "EventTime must round-trip")
	assert.Contains(t, string(got.Body), "test-event", "Body must contain the event element")
}

// TestSendNotification_SendError verifies that SendNotification wraps transport
// errors with the expected "server: SendNotification: send:" prefix.
func TestSendNotification_SendError(t *testing.T) {
	t.Parallel()
	clientSess, serverSess := newTestPair(t)

	// Close both sides so the next Send fails.
	require.NoError(t, clientSess.Close())
	require.NoError(t, serverSess.Close())

	notif := &netconf.Notification{
		EventTime: "2026-01-01T00:00:00Z",
		Body:      []byte(`<test-event/>`),
	}

	err := server.SendNotification(serverSess, notif)
	require.Error(t, err, "SendNotification must return an error when transport is closed")
	assert.Contains(t, err.Error(), "server: SendNotification: send:",
		"error must include the expected prefix")
}

// ── client↔server integration tests ──────────────────────────────────────────

// newClientServerPair establishes a NETCONF loopback session pair, wraps the
// client side in a *client.Client, and returns it together with the raw
// server-side Session.  The caller is responsible for running server.Serve on
// serverSess and for calling cli.Close() when done.
//
// Concurrently establishing both sessions is required because the loopback
// io.Pipe is synchronous and each side must send its hello before the other
// can complete the hello exchange.
func newClientServerPair(t *testing.T) (cli *client.Client, serverSess *netconf.Session) {
	t.Helper()

	clientT, serverT := transport.NewLoopback()
	t.Cleanup(func() {
		clientT.Close()
		serverT.Close()
	})

	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	cliCh := make(chan sessResult, 1)
	srvCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, testCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, testCaps, 1)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	// NewClient starts the background recvLoop goroutine which drains replies
	// automatically — no loopback deadlock risk for the client side.
	cli = client.NewClient(cliRes.sess)
	return cli, srvRes.sess
}

// TestServer_WithClient is the integration closure for R005: typed
// client.Client methods drive real NETCONF RPCs against a Server with mock
// handlers over an in-process loopback transport.
//
// Sequence:
//  1. GetConfig → handler returns <data><config/></data> body → DataReply non-nil
//  2. EditConfig → handler returns (nil, nil) → ok reply → no error
//  3. CloseSession → server built-in intercepts → <ok/> → Serve returns nil
func TestServer_WithClient(t *testing.T) {
	t.Parallel()
	cli, serverSess := newClientServerPair(t)

	srv := server.NewServer()

	// get-config handler: returns a minimal <data> body.
	const getConfigBody = `<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><config/></data>`
	srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(getConfigBody), nil
		},
	))

	// edit-config handler: returns (nil, nil) → <ok/> reply.
	srv.RegisterHandler("edit-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil
		},
	))

	// Serve runs in a goroutine; we collect its return value.
	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// 1. GetConfig — DataReply must be non-nil and contain <config/>.
	running := netconf.Datastore{Running: &struct{}{}}
	dr, err := cli.GetConfig(ctx, running, nil)
	require.NoError(t, err, "GetConfig must succeed")
	require.NotNil(t, dr, "GetConfig must return a DataReply")
	assert.Contains(t, string(dr.Content), "config",
		"DataReply content must include the handler-supplied config element")

	// 2. EditConfig — plain <ok/> expected.
	editCfg := netconf.EditConfig{
		Target: netconf.Datastore{Running: &struct{}{}},
		Config: []byte(`<config/>`),
	}
	require.NoError(t, cli.EditConfig(ctx, editCfg), "EditConfig must succeed")

	// 3. CloseSession — built-in intercept sends <ok/>, Serve returns nil.
	// client.Client's recvLoop drains the reply, so no manual read is needed
	// before waiting on serveDone.
	require.NoError(t, cli.CloseSession(ctx), "CloseSession must succeed")

	select {
	case err := <-serveDone:
		assert.NoError(t, err, "Serve must return nil after CloseSession")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession")
	}

	// Close the client — this shuts down the recvLoop goroutine cleanly.
	_ = cli.Close()
}

// TestServer_WithClient_RPCError proves the full error propagation chain:
// server handler → RPCError → marshal → transport → client dispatcher →
// ParseRPCErrors → checkDataReply → errors.As.
//
// A handler registered for "get-config" returns an RPCError.  The typed
// client method GetConfig must surface it as a netconf.RPCError that
// errors.As can extract with matching fields.
func TestServer_WithClient_RPCError(t *testing.T) {
	t.Parallel()
	cli, serverSess := newClientServerPair(t)

	srv := server.NewServer()

	// get-config handler that returns an application-layer RPCError.
	handlerErr := netconf.RPCError{
		Type:     "application",
		Tag:      "invalid-value",
		Severity: "error",
		Message:  "test error from server",
	}
	srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, handlerErr
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	running := netconf.Datastore{Running: &struct{}{}}
	_, err := cli.GetConfig(ctx, running, nil)
	require.Error(t, err, "GetConfig must return an error when the handler fails")

	// errors.As must be able to extract the structured RPCError.
	var rpcErr netconf.RPCError
	require.True(t, errors.As(err, &rpcErr),
		"error must be (or wrap) a netconf.RPCError; got: %v", err)
	assert.Equal(t, "application", rpcErr.Type)
	assert.Equal(t, "invalid-value", rpcErr.Tag)
	assert.Equal(t, "error", rpcErr.Severity)
	assert.Equal(t, "test error from server", rpcErr.Message)

	// Terminate the server cleanly so the test goroutine exits.
	require.NoError(t, cli.CloseSession(ctx), "CloseSession must succeed after error reply")
	select {
	case err := <-serveDone:
		assert.NoError(t, err, "Serve must return nil after CloseSession")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession")
	}
	_ = cli.Close()
}

// TestServer_ContextCancel proves that cancelling the context passed to
// server.Serve — followed by closing the underlying transport — causes Serve
// to return.  The return value may be the context error or a transport error,
// depending on timing — either is acceptable.
//
// Note: Serve's inner sess.Recv() call is synchronous and cannot select on
// ctx.Done() directly.  Closing the transport (via cli.Close()) is the
// standard way to unblock a blocking Recv, consistent with real-world
// graceful-shutdown patterns.  Context cancellation signals "stop taking new
// work" while transport close actually unblocks the I/O.
func TestServer_ContextCancel(t *testing.T) {
	t.Parallel()
	cli, serverSess := newClientServerPair(t)

	srv := server.NewServer()

	ctx, cancel := context.WithCancel(context.Background())

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// Cancel the context, then close the client to unblock sess.Recv on the
	// server side (loopback io.Pipe is synchronous; closing either end
	// returns io.ErrClosedPipe / io.EOF to the other).
	cancel()
	_ = cli.Close()

	select {
	case <-serveDone:
		// Serve returned — either context.Canceled, io.EOF, or a transport
		// error.  All are acceptable: what matters is that Serve did not hang.
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation and transport close")
	}
}
