// callhome_test.go — transport-level tests for SSH call home (RFC 8071).
//
// Tests verify that DialCallHome (NETCONF server dials out) and DialConn
// (NETCONF client uses a pre-accepted net.Conn) work together to complete
// a NETCONF hello exchange over a loopback TCP connection.
//
// In call home, the NETCONF client listens first; the NETCONF server dials
// out. The SSH server/client roles are unchanged — the NETCONF server still
// runs the SSH server protocol, and the NETCONF client still runs the SSH
// client protocol.
//
// Test inventory:
//   - TestCallHome_SSHTransport — server dials via DialCallHome; client
//     accepts via DialConn; both sides complete hello and a message
//     round-trips to prove transport framing is active.
package ssh

import (
	"fmt"
	"net"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCallHome_SSHTransport verifies the full SSH call home transport path:
//
//  1. NETCONF client binds a loopback listener (mimics call-home listener).
//  2. NETCONF server dials out via DialCallHome → SSH server handshake →
//     *ServerTransport → NETCONF ServerSession (session-id 301).
//  3. NETCONF client accepts the connection → DialConn → SSH client
//     handshake → *Transport → NETCONF ClientSession.
//  4. Client sees session-id 301; both sides see chunked framing.
//  5. A message written by the client is readable by the server (transport
//     framing is active end-to-end).
func TestCallHome_SSHTransport(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	serverCfg, clientCfg := testSSHConfigs(t)

	// Client listens first — the port is bound before the server dials.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "client listen")
	defer ln.Close()

	addr := ln.Addr().String()

	// Server goroutine: dial out (call home), run SSH server protocol, complete hello.
	type serverResult struct {
		trp  *ServerTransport
		sess *netconf.Session
		err  error
	}
	srvResultCh := make(chan serverResult, 1)
	go func() {
		srvTrp, err := DialCallHome(addr, serverCfg)
		if err != nil {
			srvResultCh <- serverResult{err: fmt.Errorf("DialCallHome: %w", err)}
			return
		}
		sess, err := netconf.ServerSession(srvTrp, caps, 301) // P021: M003 uses 300+
		if err != nil {
			srvResultCh <- serverResult{err: fmt.Errorf("ServerSession: %w", err)}
			return
		}
		srvResultCh <- serverResult{trp: srvTrp, sess: sess}
	}()

	// Client foreground: accept the incoming TCP connection, run SSH client
	// protocol via DialConn, complete hello.
	conn, err := ln.Accept()
	require.NoError(t, err, "client accept TCP connection")

	clientTrp, err := DialConn(conn, conn.RemoteAddr().String(), clientCfg)
	require.NoError(t, err, "DialConn (SSH client handshake over accepted conn)")
	defer clientTrp.Close()

	clientSess, err := netconf.ClientSession(clientTrp, caps)
	require.NoError(t, err, "ClientSession")

	// Collect server result.
	var sr serverResult
	select {
	case sr = <-srvResultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server session")
	}
	require.NoError(t, sr.err)
	defer sr.trp.Close()

	// Verify session state.
	assert.Equal(t, uint32(301), clientSess.SessionID(), "client sees session-id=301")
	assert.Equal(t, uint32(301), sr.sess.SessionID(), "server session-id=301")
	assert.Equal(t, netconf.FramingChunked, clientSess.FramingMode(), "client: chunked framing after hello")
	assert.Equal(t, netconf.FramingChunked, sr.sess.FramingMode(), "server: chunked framing after hello")

	// Verify the transport is active by writing a message client→server.
	testMsg := []byte("<rpc>call-home transport round-trip</rpc>")
	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- transport.WriteMsg(clientTrp, testMsg)
	}()

	got, err := transport.ReadMsg(sr.trp)
	require.NoError(t, err, "server read message from client")
	assert.Equal(t, testMsg, got, "message round-trip over call home transport")
	require.NoError(t, <-writeErrCh, "client write message")
}
