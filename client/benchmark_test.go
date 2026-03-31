package client_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/client"
	"github.com/GabrielNunesIT/netconf/server"
	"github.com/GabrielNunesIT/netconf/transport"
)

// benchCaps is a base:1.0-only capability set for benchmarks.
var benchCaps = netconf.NewCapabilitySet([]string{netconf.BaseCap10})

// ── helpers ──────────────────────────────────────────────────────────────────

// newBenchPair creates a loopback NETCONF session pair for benchmarks.
// Returns the client-side Client and server-side raw transport.
// The caller is responsible for closing both.
func newBenchPair(b *testing.B) (*client.Client, transport.Transport) {
	b.Helper()

	clientT, serverT := transport.NewLoopback()

	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	clientCh := make(chan sessResult, 1)
	serverCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, benchCaps)
		clientCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, benchCaps, 1)
		serverCh <- sessResult{s, err}
	}()

	clientRes := <-clientCh
	serverRes := <-serverCh
	if clientRes.err != nil {
		b.Fatalf("ClientSession: %v", clientRes.err)
	}
	if serverRes.err != nil {
		b.Fatalf("ServerSession: %v", serverRes.err)
	}
	_ = serverRes.sess

	c := client.NewClient(clientRes.sess)
	return c, serverT
}

// newBenchServerPair creates a loopback pair with a running Server dispatch
// loop on the server side. Returns the Client and a cleanup function.
// The server runs handlers registered via the returned *server.Server before
// calling startServe.
func newBenchServerPair(b *testing.B) (*client.Client, *server.Server, func()) {
	b.Helper()

	clientT, serverT := transport.NewLoopback()

	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	clientCh := make(chan sessResult, 1)
	serverCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, benchCaps)
		clientCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, benchCaps, 1)
		serverCh <- sessResult{s, err}
	}()

	clientRes := <-clientCh
	serverRes := <-serverCh
	if clientRes.err != nil {
		b.Fatalf("ClientSession: %v", clientRes.err)
	}
	if serverRes.err != nil {
		b.Fatalf("ServerSession: %v", serverRes.err)
	}

	srv := server.NewServer()

	c := client.NewClient(clientRes.sess)

	cleanup := func() {
		c.Close()
		clientT.Close()
		serverT.Close()
	}

	// Start server dispatch in background — the caller must register handlers
	// before calling any RPC.
	go func() {
		_ = srv.Serve(context.Background(), serverRes.sess)
	}()

	return c, srv, cleanup
}

// replyDataBody builds a <data> element containing `n` bytes of filler XML.
func replyDataBody(n int) []byte {
	// Build <data><config>AAAA...AAAA</config></data>
	filler := strings.Repeat("A", n)
	return []byte("<data><config>" + filler + "</config></data>")
}

// ── BenchmarkRecvLoop_DecodeReply ────────────────────────────────────────────
//
// Measures the client recvLoop's decode path for a typical RPCReply with a
// ~4KB data body. This exercises: transport.ReadMsg → xml peek → xml.Unmarshal
// → pending map delivery.

func BenchmarkRecvLoop_DecodeReply(b *testing.B) {
	b.ReportAllocs()

	c, serverT := newBenchPair(b)
	defer func() {
		c.Close()
		serverT.Close()
	}()

	// Pre-build a reply with ~4KB body.
	body := replyDataBody(4096)

	ctx := context.Background()

	for b.Loop() {
		// Send an RPC from client (triggers message-id allocation and send).
		errCh := make(chan error, 1)
		go func() {
			// Echo server: read the RPC, extract message-id, send a reply.
			raw, err := transport.ReadMsg(serverT)
			if err != nil {
				errCh <- err
				return
			}
			var rpc netconf.RPC
			if err := xml.Unmarshal(raw, &rpc); err != nil {
				errCh <- err
				return
			}
			reply := &netconf.RPCReply{
				MessageID: rpc.MessageID,
				Body:      body,
			}
			data, err := xml.Marshal(reply)
			if err != nil {
				errCh <- err
				return
			}
			errCh <- transport.WriteMsg(serverT, data)
		}()

		reply, err := c.Do(ctx, &netconf.GetConfig{
			Source: netconf.Datastore{Running: &struct{}{}},
		})
		if err != nil {
			b.Fatalf("Do: %v", err)
		}
		if reply == nil {
			b.Fatal("nil reply")
		}

		if err := <-errCh; err != nil {
			b.Fatalf("server echo: %v", err)
		}
	}
}

// ── BenchmarkRecvLoop_DecodeNotification ─────────────────────────────────────
//
// Measures the recvLoop decode path for a notification message. Exercises:
// transport.ReadMsg → xml peek (NotificationNS) → xml.Unmarshal → channel send.

func BenchmarkRecvLoop_DecodeNotification(b *testing.B) {
	b.ReportAllocs()

	c, serverT := newBenchPair(b)
	defer func() {
		c.Close()
		serverT.Close()
	}()

	// Pre-serialize a notification.
	notif := &netconf.Notification{
		EventTime: "2025-01-01T00:00:00Z",
		Body:      []byte(`<event xmlns="urn:example"><msg>hello</msg></event>`),
	}
	notifBytes, err := xml.Marshal(notif)
	if err != nil {
		b.Fatalf("marshal notification: %v", err)
	}

	notifCh := c.Notifications()

	for b.Loop() {
		if err := transport.WriteMsg(serverT, notifBytes); err != nil {
			b.Fatalf("send notification: %v", err)
		}

		select {
		case n := <-notifCh:
			if n == nil {
				b.Fatal("nil notification")
			}
		case <-time.After(5 * time.Second):
			b.Fatal("timeout waiting for notification")
		}
	}
}

// ── BenchmarkClient_DoRoundTrip ──────────────────────────────────────────────
//
// Full Do() round-trip over loopback with a server.Server running a get-config
// handler. Measures the complete marshal→send→recv→unmarshal→checkReply chain
// for a typical response (~4KB body).

func BenchmarkClient_DoRoundTrip(b *testing.B) {
	b.ReportAllocs()

	body := replyDataBody(4096)

	c, srv, cleanup := newBenchServerPair(b)
	defer cleanup()

	srv.RegisterHandler("get-config", server.HandlerFunc(
		func(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			return body, nil
		},
	))

	// Let the server goroutine start.
	// Small delay is fine for benchmark setup — it's outside the timer.
	ctx := context.Background()

	for b.Loop() {
		dr, err := c.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
		if err != nil {
			b.Fatalf("GetConfig: %v", err)
		}
		if dr == nil {
			b.Fatal("nil DataReply")
		}
	}
}

// ── BenchmarkClient_LargePayload ─────────────────────────────────────────────
//
// Same as DoRoundTrip but with a ~1MB data body to measure peak allocation.

func BenchmarkClient_LargePayload(b *testing.B) {
	b.ReportAllocs()

	body := replyDataBody(1024 * 1024) // 1MB

	c, srv, cleanup := newBenchServerPair(b)
	defer cleanup()

	srv.RegisterHandler("get-config", server.HandlerFunc(
		func(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			return body, nil
		},
	))

	ctx := context.Background()

	for b.Loop() {
		dr, err := c.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
		if err != nil {
			b.Fatalf("GetConfig: %v", err)
		}
		if dr == nil {
			b.Fatal("nil DataReply")
		}
	}
}

// ── BenchmarkMarshalRPC ──────────────────────────────────────────────────────
//
// Measures just the xml.Marshal cost for building an RPC request (the send side
// of Do). Useful for isolating marshal cost from transport+decode cost.

func BenchmarkMarshalRPC(b *testing.B) {
	b.ReportAllocs()

	op := &netconf.GetConfig{
		Source: netconf.Datastore{Running: &struct{}{}},
		Filter: &netconf.Filter{
			Type:    "subtree",
			Content: []byte(`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"/>`),
		},
	}

	var buf bytes.Buffer

	for b.Loop() {
		opBytes, err := xml.Marshal(op)
		if err != nil {
			b.Fatal(err)
		}
		rpc := netconf.RPC{
			MessageID: "1",
			Body:      opBytes,
		}
		buf.Reset()
		if err := xml.NewEncoder(&buf).Encode(rpc); err != nil {
			b.Fatal(err)
		}
	}
}

// ── BenchmarkUnmarshalRPCReply ───────────────────────────────────────────────
//
// Measures just the xml.Unmarshal cost for decoding an RPCReply (the receive
// side in recvLoop). Isolates the XML decode cost from transport overhead.

func BenchmarkUnmarshalRPCReply(b *testing.B) {
	b.ReportAllocs()

	body := replyDataBody(4096)
	reply := &netconf.RPCReply{
		MessageID: "1",
		Body:      body,
	}
	raw, err := xml.Marshal(reply)
	if err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		var r netconf.RPCReply
		if err := xml.Unmarshal(raw, &r); err != nil {
			b.Fatal(err)
		}
	}
}

// ── BenchmarkUnmarshalNotification ───────────────────────────────────────────
//
// Measures xml.Unmarshal cost for a notification (the notification path in
// recvLoop).

func BenchmarkUnmarshalNotification(b *testing.B) {
	b.ReportAllocs()

	notif := &netconf.Notification{
		EventTime: "2025-01-01T00:00:00Z",
		Body:      []byte(`<event xmlns="urn:example"><msg>hello world</msg></event>`),
	}
	raw, err := xml.Marshal(notif)
	if err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		var n netconf.Notification
		if err := xml.Unmarshal(raw, &n); err != nil {
			b.Fatal(err)
		}
	}
}

// ── Chunked framing benchmarks ────────────────────────────────────────────────

// benchCaps11 is a base:1.0+1.1 capability set — triggers chunked framing
// negotiation after the hello exchange.
var benchCaps11 = netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

// newBenchPairChunked creates a loopback pair that negotiates chunked (base:1.1)
// framing. Returns the client.Client and the server-side raw transport.
func newBenchPairChunked(b *testing.B) (*client.Client, transport.Transport) {
	b.Helper()

	clientT, serverT := transport.NewLoopback()

	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	clientCh := make(chan sessResult, 1)
	serverCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, benchCaps11)
		clientCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, benchCaps11, 700)
		serverCh <- sessResult{s, err}
	}()

	clientRes := <-clientCh
	serverRes := <-serverCh
	if clientRes.err != nil {
		b.Fatalf("ClientSession: %v", clientRes.err)
	}
	if serverRes.err != nil {
		b.Fatalf("ServerSession: %v", serverRes.err)
	}
	_ = serverRes.sess

	c := client.NewClient(clientRes.sess)
	return c, serverT
}

// newBenchServerPairChunked creates a loopback pair with chunked framing and a
// running server.Server dispatch loop. Returns the Client, Server, and cleanup.
func newBenchServerPairChunked(b *testing.B) (*client.Client, *server.Server, func()) {
	b.Helper()

	clientT, serverT := transport.NewLoopback()

	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	clientCh := make(chan sessResult, 1)
	serverCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, benchCaps11)
		clientCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, benchCaps11, 701)
		serverCh <- sessResult{s, err}
	}()

	clientRes := <-clientCh
	serverRes := <-serverCh
	if clientRes.err != nil {
		b.Fatalf("ClientSession: %v", clientRes.err)
	}
	if serverRes.err != nil {
		b.Fatalf("ServerSession: %v", serverRes.err)
	}

	srv := server.NewServer()
	c := client.NewClient(clientRes.sess)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = srv.Serve(ctx, serverRes.sess)
	}()

	cleanup := func() {
		cancel()
		c.Close()
		clientT.Close()
		serverT.Close()
	}

	return c, srv, cleanup
}

// BenchmarkClient_LargePayload_Chunked measures the full client round-trip for
// a 1MB reply over base:1.1 chunked framing. Compare against
// BenchmarkClient_LargePayload (EOM framing) to isolate the chunked streaming
// reader improvement.
func BenchmarkClient_LargePayload_Chunked(b *testing.B) {
	b.ReportAllocs()

	body := replyDataBody(1024 * 1024) // 1MB

	c, srv, cleanup := newBenchServerPairChunked(b)
	defer cleanup()

	srv.RegisterHandler("get-config", server.HandlerFunc(
		func(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			return body, nil
		},
	))

	ctx := context.Background()

	for b.Loop() {
		dr, err := c.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
		if err != nil {
			b.Fatalf("GetConfig: %v", err)
		}
		if dr == nil {
			b.Fatal("nil DataReply")
		}
	}
}

// BenchmarkRecvLoop_DecodeReply_Chunked measures the client recvLoop decode
// path for a 4KB reply over chunked framing — shows alloc reduction vs EOM.
func BenchmarkRecvLoop_DecodeReply_Chunked(b *testing.B) {
	b.ReportAllocs()

	c, serverT := newBenchPairChunked(b)
	defer func() {
		c.Close()
		serverT.Close()
	}()

	body := replyDataBody(4096)
	ctx := context.Background()

	for b.Loop() {
		errCh := make(chan error, 1)
		go func() {
			raw, err := transport.ReadMsg(serverT)
			if err != nil {
				errCh <- err
				return
			}
			var rpc netconf.RPC
			if err := xml.Unmarshal(raw, &rpc); err != nil {
				errCh <- err
				return
			}
			reply := &netconf.RPCReply{
				MessageID: rpc.MessageID,
				Body:      body,
			}
			data, err := xml.Marshal(reply)
			if err != nil {
				errCh <- err
				return
			}
			errCh <- transport.WriteMsg(serverT, data)
		}()

		reply, err := c.Do(ctx, &netconf.GetConfig{
			Source: netconf.Datastore{Running: &struct{}{}},
		})
		if err != nil {
			b.Fatalf("Do: %v", err)
		}
		if reply == nil {
			b.Fatal("nil reply")
		}

		if err := <-errCh; err != nil {
			b.Fatalf("server echo: %v", err)
		}
	}
}
