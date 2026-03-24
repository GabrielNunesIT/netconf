// tls_test.go — integration tests for the TLS NETCONF transport.
//
// Tests use loopback TCP connections on random ports with ephemeral ECDSA P-256
// certificates, so they run without any external infrastructure or configuration.
//
// Test inventory:
//   - TestTLS_HelloBase11 — mutual TLS auth + base:1.1 negotiation;
//     verifies chunked framing is active after hello and a message round-trips.
//   - TestTLS_HelloBase10Only — base:1.0 only; verifies framing stays EOM.
//   - TestTLS_PeerCertificates — verifies ServerTransport.PeerCertificates()
//     returns the client's leaf certificate.
//   - TestTLS_ServerDerivesUsername — loopback integration test that wires
//     DeriveUsername (T01) with the TLS transport (T02): after accept, the
//     server derives the NETCONF username from the client's peer cert and
//     asserts it matches the client cert's SAN DNS name.
//
// Observability: `go test ./netconf/transport/tls/... -v` prints per-test
// PASS/FAIL with full error context. Server goroutine errors surface via
// result channels so they appear in test failure messages rather than being
// silently swallowed.
package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Test cert helpers ────────────────────────────────────────────────────────

// caBundle holds a CA certificate and its private key.
type caBundle struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// generateTestCA creates a self-signed ECDSA P-256 CA certificate for use in
// tests. Keys are ephemeral; no disk I/O occurs.
func generateTestCA(t *testing.T) *caBundle {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "generate CA key")

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err, "create CA cert")

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err, "parse CA cert")

	return &caBundle{cert: cert, key: key}
}

// certBundle holds a signed certificate and its private key as TLS-ready PEM
// blocks.
type certBundle struct {
	cert    *x509.Certificate
	certPEM []byte
	keyPEM  []byte
}

// generateTestCert creates a certificate signed by ca, using template as the
// certificate template. Returns a certBundle with the parsed cert and PEM
// representations.
func generateTestCert(t *testing.T, ca *caBundle, template *x509.Certificate) *certBundle {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "generate cert key")

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err, "create cert")

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err, "parse cert")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err, "marshal key")
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &certBundle{cert: cert, certPEM: certPEM, keyPEM: keyPEM}
}

// tlsConfigPair holds matched server and client TLS configs for a test.
type tlsConfigPair struct {
	server     *cryptotls.Config
	client     *cryptotls.Config
	clientCert *x509.Certificate // the client leaf cert (for assertion)
}

// testTLSConfigs returns a matched server+client TLS config pair with mutual
// authentication enabled. The server cert is signed by the test CA; the client
// cert has a SAN DNS name "client.test" and CN "client.test" for cert-to-name
// testing.
func testTLSConfigs(t *testing.T) *tlsConfigPair {
	t.Helper()

	ca := generateTestCA(t)

	// CA cert pool — used by both sides to verify the peer.
	caPool := x509.NewCertPool()
	caPool.AddCert(ca.cert)

	// Server certificate (SAN: localhost).
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "server.test"},
		DNSNames:     []string{"localhost", "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverBundle := generateTestCert(t, ca, serverTemplate)
	serverTLSCert, err := cryptotls.X509KeyPair(serverBundle.certPEM, serverBundle.keyPEM)
	require.NoError(t, err, "server X509KeyPair")

	// Client certificate (SAN DNS: "client.test", CN: "client.test") for
	// cert-to-name testing.
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "client.test"},
		DNSNames:     []string{"client.test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientBundle := generateTestCert(t, ca, clientTemplate)
	clientTLSCert, err := cryptotls.X509KeyPair(clientBundle.certPEM, clientBundle.keyPEM)
	require.NoError(t, err, "client X509KeyPair")

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

	return &tlsConfigPair{
		server:     serverCfg,
		client:     clientCfg,
		clientCert: clientBundle.cert,
	}
}

// newInProcessTLSPair creates a matched server Listener and a client Transport
// connected via loopback TCP on a random port. Both sides are ready for the
// hello exchange on return. The caller must close both.
func newInProcessTLSPair(t *testing.T) (*Listener, *Transport) {
	t.Helper()

	cfgs := testTLSConfigs(t)

	// Bind on a random loopback port.
	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen on loopback")

	listener := NewListener(nl, cfgs.server)

	// Client: connect and complete TLS handshake.
	addr := nl.Addr().String()
	clientTrp, err := Dial(addr, cfgs.client)
	require.NoError(t, err, "client Dial")

	return listener, clientTrp
}

// ─── Integration tests ────────────────────────────────────────────────────────

// TestTLS_HelloBase11 verifies the full NETCONF hello exchange over mutual TLS
// with base:1.1 capability negotiation (chunked framing auto-upgrade).
//
// Checks:
//   - client receives the server-assigned session-id (42)
//   - both sides report FramingChunked after hello
//   - a message written in chunked framing on the client side is readable
//     on the server side (proves the transport is actually switched)
func TestTLS_HelloBase11(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	listener, clientTrp := newInProcessTLSPair(t)
	defer listener.Close()
	defer clientTrp.Close()

	type serverResult struct {
		trp  *ServerTransport
		sess *netconf.Session
		err  error
	}
	srvResultCh := make(chan serverResult, 1)

	go func() {
		srvTrp, err := listener.Accept()
		if err != nil {
			srvResultCh <- serverResult{err: fmt.Errorf("Accept: %w", err)}
			return
		}
		sess, err := netconf.ServerSession(srvTrp, caps, 42)
		if err != nil {
			srvResultCh <- serverResult{err: fmt.Errorf("ServerSession: %w", err)}
			return
		}
		srvResultCh <- serverResult{trp: srvTrp, sess: sess}
	}()

	// Client hello exchange.
	clientSess, err := netconf.ClientSession(clientTrp, caps)
	require.NoError(t, err, "ClientSession")

	// Collect server result (with timeout to avoid hanging tests).
	var sr serverResult
	select {
	case sr = <-srvResultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server session")
	}
	require.NoError(t, sr.err)

	// Verify session state.
	assert.Equal(t, uint32(42), clientSess.SessionID(), "client sees session-id=42")
	assert.Equal(t, uint32(42), sr.sess.SessionID(), "server session-id=42")
	assert.Equal(t, netconf.FramingChunked, clientSess.FramingMode(), "client: chunked framing")
	assert.Equal(t, netconf.FramingChunked, sr.sess.FramingMode(), "server: chunked framing")

	// Verify chunked framing is active by sending a message.
	testMsg := []byte("<rpc>hello from client in chunked mode</rpc>")
	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- transport.WriteMsg(clientTrp, testMsg)
	}()

	got, err := transport.ReadMsg(sr.trp)
	require.NoError(t, err, "server read chunked message")
	assert.Equal(t, testMsg, got, "chunked message round-trip")
	require.NoError(t, <-writeErrCh, "client write chunked message")
}

// TestTLS_HelloBase10Only verifies that when the server advertises only
// base:1.0, framing remains in EOM mode on both sides.
func TestTLS_HelloBase10Only(t *testing.T) {
	serverCaps := netconf.NewCapabilitySet([]string{netconf.BaseCap10})
	clientCaps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	listener, clientTrp := newInProcessTLSPair(t)
	defer listener.Close()
	defer clientTrp.Close()

	type serverResult struct {
		trp  *ServerTransport
		sess *netconf.Session
		err  error
	}
	srvResultCh := make(chan serverResult, 1)

	go func() {
		srvTrp, err := listener.Accept()
		if err != nil {
			srvResultCh <- serverResult{err: fmt.Errorf("Accept: %w", err)}
			return
		}
		sess, err := netconf.ServerSession(srvTrp, serverCaps, 7)
		if err != nil {
			srvResultCh <- serverResult{err: fmt.Errorf("ServerSession: %w", err)}
			return
		}
		srvResultCh <- serverResult{trp: srvTrp, sess: sess}
	}()

	clientSess, err := netconf.ClientSession(clientTrp, clientCaps)
	require.NoError(t, err, "ClientSession")

	var sr serverResult
	select {
	case sr = <-srvResultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server session")
	}
	require.NoError(t, sr.err)

	assert.Equal(t, uint32(7), clientSess.SessionID(), "session-id")
	assert.Equal(t, netconf.FramingEOM, clientSess.FramingMode(), "client: EOM framing (server is base:1.0 only)")
	assert.Equal(t, netconf.FramingEOM, sr.sess.FramingMode(), "server: EOM framing")

	// Verify EOM framing works.
	testMsg := []byte("<rpc>hello in EOM mode</rpc>")
	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- transport.WriteMsg(clientTrp, testMsg)
	}()

	got, err := transport.ReadMsg(sr.trp)
	require.NoError(t, err, "server read EOM message")
	assert.Equal(t, testMsg, got, "EOM message round-trip")
	require.NoError(t, <-writeErrCh, "client write EOM message")
}

// TestTLS_PeerCertificates verifies that ServerTransport.PeerCertificates()
// returns the client's leaf certificate after mutual TLS authentication.
func TestTLS_PeerCertificates(t *testing.T) {
	cfgs := testTLSConfigs(t)

	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	listener := NewListener(nl, cfgs.server)
	defer listener.Close()

	// Dial using the configured client TLS config.
	addr := nl.Addr().String()
	clientTrp, err := Dial(addr, cfgs.client)
	require.NoError(t, err, "Dial")
	defer clientTrp.Close()

	srvTrp, err := listener.Accept()
	require.NoError(t, err, "Accept")
	defer srvTrp.Close()

	peerCerts := srvTrp.PeerCertificates()
	require.NotEmpty(t, peerCerts, "server must see client certificate")

	// The leaf (index 0) must match the client cert by raw DER bytes.
	assert.Equal(t, cfgs.clientCert.Raw, peerCerts[0].Raw,
		"peer cert leaf must be the client leaf cert")
}

// TestTLS_ServerDerivesUsername is an integration test that wires the T01
// cert-to-name algorithm with the T02 TLS transport. After a mutual-auth
// loopback connection, the server calls DeriveUsername on the peer leaf cert
// and asserts that the derived username matches the client cert's SAN DNS name
// ("client.test").
func TestTLS_ServerDerivesUsername(t *testing.T) {
	cfgs := testTLSConfigs(t)

	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	listener := NewListener(nl, cfgs.server)
	defer listener.Close()

	addr := nl.Addr().String()
	clientTrp, err := Dial(addr, cfgs.client)
	require.NoError(t, err, "Dial")
	defer clientTrp.Close()

	srvTrp, err := listener.Accept()
	require.NoError(t, err, "Accept")
	defer srvTrp.Close()

	peerCerts := srvTrp.PeerCertificates()
	require.NotEmpty(t, peerCerts, "server must see client certificate")

	leafCert := peerCerts[0]

	// Build a cert-to-name map that matches the client cert by its SHA-256
	// fingerprint and derives the username from the SAN DNS name.
	maps := []MapEntry{
		{
			Fingerprint: certFingerprint(leafCert),
			MapType:     MapTypeSANDNSName,
		},
	}

	username, ok := DeriveUsername(leafCert, nil, maps)
	require.True(t, ok, "DeriveUsername must return ok=true for the client cert")
	assert.Equal(t, "client.test", username,
		"derived username must match the client cert's SAN DNS name")
}
