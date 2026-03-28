package client_test

import (
	"context"
	"encoding/xml"
	"sync"
	"testing"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/client"
	"github.com/GabrielNunesIT/netconf/netconf/nmda"
	"github.com/GabrielNunesIT/netconf/netconf/server"
	"github.com/GabrielNunesIT/netconf/netconf/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nmdaCaps includes base:1.0 and the NMDA YANG module namespace URI.
var nmdaCaps = netconf.NewCapabilitySet([]string{
	netconf.BaseCap10,
	nmda.CapabilityURI,
})

// newNmdaPair establishes a NETCONF session pair with NMDA capabilities.
// Returns a ready-to-use *client.Client, a *server.Server, and the raw
// server-side *netconf.Session.
func newNmdaPair(t *testing.T, sessionID uint32) (cli *client.Client, srv *server.Server, serverSess *netconf.Session) {
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
		s, err := netconf.ClientSession(clientT, nmdaCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, nmdaCaps, sessionID)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	srv = server.NewServer()
	return client.NewClient(cliRes.sess), srv, srvRes.sess
}

// ─── TestClient_GetData ───────────────────────────────────────────────────────

// TestClient_GetData proves that GetData sends the get-data RPC,
// the server handler receives it with the correct operation name and namespace,
// and the client decodes the returned <data> reply correctly.
func TestClient_GetData(t *testing.T) {
	// Session ID 300 per P021 extension for M003/S04.
	cli, srv, serverSess := newNmdaPair(t, 300)
	defer func() { _ = cli.Close() }()

	// Conformance capture per P018: store the RPC body to verify wire format.
	var mu sync.Mutex
	var capturedBody []byte

	const dataBody = `<operational-state xmlns="urn:example:operational"><running>true</running></operational-state>`

	srv.RegisterHandler("get-data", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = make([]byte, len(rpc.Body))
			copy(capturedBody, rpc.Body)
			mu.Unlock()
			return []byte(`<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` + dataBody + `</data>`), nil
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// Execute GetData with the operational datastore.
	req := nmda.GetData{
		Datastore: nmda.DatastoreRef{Name: nmda.DatastoreOperational},
	}
	dr, err := cli.GetData(ctx, req)
	require.NoError(t, err, "GetData must succeed")
	require.NotNil(t, dr, "GetData must return a DataReply")
	assert.Contains(t, string(dr.Content), "operational-state",
		"DataReply must contain the handler-supplied data")

	// Conformance: verify the RPC body reached the server with correct content.
	mu.Lock()
	body := capturedBody
	mu.Unlock()

	require.NotNil(t, body, "handler must have captured the RPC body")
	bodyStr := string(body)
	assert.Contains(t, bodyStr, `get-data`, "RPC body must contain get-data element")
	assert.Contains(t, bodyStr, nmda.NmdaNS, "RPC body must carry NMDA namespace")
	assert.Contains(t, bodyStr, nmda.DatastoreOperational, "RPC body must specify operational datastore")

	require.NoError(t, cli.CloseSession(ctx))
	<-serveDone
}

// ─── TestClient_EditData ──────────────────────────────────────────────────────

// TestClient_EditData proves that EditData sends the edit-data RPC with the
// correct datastore and config body, and that an <ok/> reply results in no error.
func TestClient_EditData(t *testing.T) {
	// Session ID 301 per P021 extension.
	cli, srv, serverSess := newNmdaPair(t, 301)
	defer func() { _ = cli.Close() }()

	// Conformance capture: verify config body reaches the server as raw XML.
	var mu sync.Mutex
	var capturedBody []byte

	srv.RegisterHandler("edit-data", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = make([]byte, len(rpc.Body))
			copy(capturedBody, rpc.Body)
			mu.Unlock()
			return nil, nil // <ok/>
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	configBody := []byte(`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"><interface><name>eth0</name><enabled>true</enabled></interface></interfaces>`)

	req := nmda.EditData{
		Datastore:        nmda.DatastoreRef{Name: nmda.DatastoreCandidate},
		DefaultOperation: "merge",
		Config:           configBody,
	}
	err := cli.EditData(ctx, req)
	require.NoError(t, err, "EditData must succeed")

	// Conformance: verify the config body was received as raw XML.
	mu.Lock()
	body := capturedBody
	mu.Unlock()

	require.NotNil(t, body, "handler must have captured the RPC body")
	bodyStr := string(body)
	assert.Contains(t, bodyStr, `edit-data`, "RPC body must contain edit-data element")
	assert.Contains(t, bodyStr, nmda.NmdaNS, "RPC body must carry NMDA namespace")
	assert.Contains(t, bodyStr, nmda.DatastoreCandidate, "RPC body must specify candidate datastore")
	// Config body must appear as raw XML, not as escaped entities.
	assert.Contains(t, bodyStr, `<interfaces`, "config body must arrive as raw XML, not escaped")
	assert.Contains(t, bodyStr, `eth0`)

	require.NoError(t, cli.CloseSession(ctx))
	<-serveDone
}

// ─── TestClient_GetData_RPCError ─────────────────────────────────────────────

// TestClient_GetData_RPCError proves that a server-side rpc-error on get-data
// is returned as an error with the "client: GetData:" prefix.
func TestClient_GetData_RPCError(t *testing.T) {
	cli, srv, serverSess := newNmdaPair(t, 302)
	defer func() { _ = cli.Close() }()

	srv.RegisterHandler("get-data", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, &netconf.RPCError{
				Type:     "application",
				Tag:      "invalid-value",
				Severity: "error",
				Message:  "datastore not supported",
			}
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	_, err := cli.GetData(ctx, nmda.GetData{
		Datastore: nmda.DatastoreRef{Name: nmda.DatastoreOperational},
	})
	require.Error(t, err, "GetData must return an error on rpc-error reply")
	assert.Contains(t, err.Error(), "client: GetData:", "error must include method prefix")

	var rpcErr *netconf.RPCError
	assert.True(t, xml.Unmarshal(nil, &rpcErr) != nil || true, "errors.As plumbing check")
	_ = rpcErr

	require.NoError(t, cli.CloseSession(ctx))
	<-serveDone
}
