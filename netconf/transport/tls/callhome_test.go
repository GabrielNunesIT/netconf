// callhome_test.go — transport-level tests for TLS call home (RFC 8071).
//
// Tests verify that DialCallHome (NETCONF server dials out) and the client
// accepting the connection via cryptotls.Client + NewClientTransport work
// together to complete a NETCONF hello exchange over a loopback TCP
// connection.
//
// In call home, the NETCONF client listens first; the NETCONF server dials
// out. The TLS server/client roles are unchanged — the NETCONF server still
// runs the TLS server protocol over the outbound connection.
//
// Test inventory:
//   - TestCallHome_TLSTransport — server dials via DialCallHome; client
//     accepts and completes the TLS client handshake; both sides complete
//     hello and a message round-trips to prove transport framing is active.
package tls

import (
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/transport"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCallHome_TLSTransport verifies the full TLS call home transport path:
//
//  1. NETCONF client binds a loopback listener (mimics call-home listener).
//  2. NETCONF server dials out via DialCallHome → TLS server handshake →
//     *ServerTransport → NETCONF ServerSession (session-id 302).
//  3. NETCONF client accepts the TCP connection → TLS client handshake →
//     NewClientTransport → NETCONF ClientSession.
//  4. Client sees session-id 302; both sides see chunked framing.
//  5. A message written by the client is readable by the server (transport
//     framing is active end-to-end).
func TestCallHome_TLSTransport(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	// Build TLS configs using the shared cert helpers from tls_test.go.
	ca := generateTestCA(t)

	caPool := x509.NewCertPool()
	caPool.AddCert(ca.cert)

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(10),
		Subject:      pkix.Name{CommonName: "callhome-server.test"},
		DNSNames:     []string{"localhost", "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverBundle := generateTestCert(t, ca, serverTemplate)

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(11),
		Subject:      pkix.Name{CommonName: "callhome-client.test"},
		DNSNames:     []string{"callhome-client.test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientBundle := generateTestCert(t, ca, clientTemplate)

	serverTLSCert, err := cryptotls.X509KeyPair(serverBundle.certPEM, serverBundle.keyPEM)
	require.NoError(t, err, "server TLS key pair")

	clientTLSCert, err := cryptotls.X509KeyPair(clientBundle.certPEM, clientBundle.keyPEM)
	require.NoError(t, err, "client TLS key pair")

	serverCfg := &cryptotls.Config{
		Certificates: []cryptotls.Certificate{serverTLSCert},
		ClientAuth:   cryptotls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
	clientCfg := &cryptotls.Config{
		Certificates: []cryptotls.Certificate{clientTLSCert},
		RootCAs:      caPool,
		ServerName:   "localhost",
	}

	// Client listens first — the port is bound before the server dials.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "client listen")
	defer ln.Close()

	addr := ln.Addr().String()

	// Server goroutine: dial out (call home), run TLS server protocol, complete hello.
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
		sess, err := netconf.ServerSession(srvTrp, caps, 302) // P021: M003 uses 300+
		if err != nil {
			srvResultCh <- serverResult{err: fmt.Errorf("ServerSession: %w", err)}
			return
		}
		srvResultCh <- serverResult{trp: srvTrp, sess: sess}
	}()

	// Client foreground: accept the incoming TCP connection, run TLS client
	// protocol, complete hello.
	conn, err := ln.Accept()
	require.NoError(t, err, "client accept TCP connection")

	tlsConn := cryptotls.Client(conn, clientCfg)
	require.NoError(t, tlsConn.Handshake(), "client TLS handshake")

	clientTrp := NewClientTransport(tlsConn)
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
	assert.Equal(t, uint32(302), clientSess.SessionID(), "client sees session-id=302")
	assert.Equal(t, uint32(302), sr.sess.SessionID(), "server session-id=302")
	assert.Equal(t, netconf.FramingChunked, clientSess.FramingMode(), "client: chunked framing after hello")
	assert.Equal(t, netconf.FramingChunked, sr.sess.FramingMode(), "server: chunked framing after hello")

	// Verify the transport is active by writing a message client→server.
	testMsg := []byte("<rpc>call-home tls transport round-trip</rpc>")
	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- transport.WriteMsg(clientTrp, testMsg)
	}()

	got, err := transport.ReadMsg(sr.trp)
	require.NoError(t, err, "server read message from client")
	assert.Equal(t, testMsg, got, "message round-trip over TLS call home transport")
	require.NoError(t, <-writeErrCh, "client write message")
}
