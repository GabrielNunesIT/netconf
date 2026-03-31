package server_test

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/client"
	"github.com/GabrielNunesIT/netconf/server"
	"github.com/GabrielNunesIT/netconf/transport"
)

// benchCaps is a base:1.0-only capability set for server benchmarks.
var benchServerCaps = netconf.NewCapabilitySet([]string{netconf.BaseCap10})

// newBenchServerPair creates a loopback NETCONF session pair with a running
// server.Server dispatch loop. Returns the client.Client, the registered
// server.Server, and a cleanup function.
func newBenchServerPair(b *testing.B, sessionID uint32) (*client.Client, *server.Server, func()) {
	b.Helper()

	clientT, serverT := transport.NewLoopback()

	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	clientCh := make(chan sessResult, 1)
	serverCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, benchServerCaps)
		clientCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, benchServerCaps, sessionID)
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

// serverReplyBody builds a <data> element containing n bytes of filler.
func serverReplyBody(n int) []byte {
	filler := strings.Repeat("A", n)
	return []byte("<data><config>" + filler + "</config></data>")
}

// ── BenchmarkServer_Dispatch ─────────────────────────────────────────────────
//
// Measures the server Serve dispatch path for a typical get-config RPC with a
// ~4KB reply body. Exercises: transport.ReadMsg → xml.Unmarshal(RPC) →
// firstElementName → handler → xml.Marshal(reply) → transport.WriteMsg.

func BenchmarkServer_Dispatch(b *testing.B) {
	b.ReportAllocs()

	body := serverReplyBody(4096)

	c, srv, cleanup := newBenchServerPair(b, 600)
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

// ── BenchmarkServer_DispatchLargeBody ────────────────────────────────────────
//
// Same as BenchmarkServer_Dispatch but with a ~1MB reply body. Measures
// server-side alloc overhead for large RPC payloads — the key signal for S03
// streaming transport improvement.

func BenchmarkServer_DispatchLargeBody(b *testing.B) {
	b.ReportAllocs()

	body := serverReplyBody(1024 * 1024) // 1MB

	c, srv, cleanup := newBenchServerPair(b, 601)
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

// ── BenchmarkServer_UnmarshalRPC ─────────────────────────────────────────────
//
// Measures just the xml.Unmarshal cost for decoding an incoming RPC on the
// server side (the first thing Serve does per message).

func BenchmarkServer_UnmarshalRPC(b *testing.B) {
	b.ReportAllocs()

	op := &netconf.GetConfig{
		Source: netconf.Datastore{Running: &struct{}{}},
	}
	opBytes, err := xml.Marshal(op)
	if err != nil {
		b.Fatal(err)
	}
	rpc := netconf.RPC{MessageID: "1", Body: opBytes}
	raw, err := xml.Marshal(rpc)
	if err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		var r netconf.RPC
		if err := xml.Unmarshal(raw, &r); err != nil {
			b.Fatal(err)
		}
	}
}

// ── streamGetConfigHandler ────────────────────────────────────────────────────
//
// A Handler that also implements StreamHandler. Used by stream dispatch
// benchmarks to exercise the zero-body-allocation path.

type streamGetConfigHandler struct {
	replyBody []byte
}

func (h *streamGetConfigHandler) Handle(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
	return h.replyBody, nil
}

func (h *streamGetConfigHandler) HandleStream(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC, dec *xml.Decoder, opStart xml.StartElement) ([]byte, error) {
	// Consume the operation element from the decoder without materialising body.
	if err := dec.Skip(); err != nil {
		return nil, err
	}
	return h.replyBody, nil
}

// ── BenchmarkServer_StreamDispatch ───────────────────────────────────────────
//
// Same workload as BenchmarkServer_Dispatch but the handler implements
// StreamHandler so the server skips rpc.Body materialisation. Measures the
// alloc reduction from the streaming path vs the conventional Handler path.

func BenchmarkServer_StreamDispatch(b *testing.B) {
	b.ReportAllocs()

	body := serverReplyBody(4096)
	h := &streamGetConfigHandler{replyBody: body}

	c, srv, cleanup := newBenchServerPair(b, 602)
	defer cleanup()

	srv.RegisterHandler("get-config", h)

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

// ── BenchmarkServer_StreamDispatchLargeBody ──────────────────────────────────
//
// Same workload as BenchmarkServer_DispatchLargeBody but with a StreamHandler.
// The server sends a 1MB reply without materialising the incoming op body.
// Key signal: B/op should show the reply-body cost without the op-body copy.

func BenchmarkServer_StreamDispatchLargeBody(b *testing.B) {
	b.ReportAllocs()

	body := serverReplyBody(1024 * 1024) // 1MB reply
	h := &streamGetConfigHandler{replyBody: body}

	c, srv, cleanup := newBenchServerPair(b, 603)
	defer cleanup()

	srv.RegisterHandler("get-config", h)

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

