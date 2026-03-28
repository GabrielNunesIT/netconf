// Package conformance_test is the RFC 6241 conformance test suite for the
// netconf library.
//
// These tests exercise the full client.Client ↔ server.Server stack
// end-to-end, proving:
//   - All 13 RFC 6241 operations in both EOM (base:1.0) and chunked
//     (base:1.1) framing modes
//   - Error propagation: RPCError, non-RPCError, and operation-not-supported
//   - Session lifecycle: session-id propagation, CloseSession, KillSession
//   - Framing auto-negotiation: three scenarios (both-1.1, client-1.1-only,
//     server-1.1-only)
//   - Subtree and XPath filter types reaching the server handler on the wire
//   - Full TCP→SSH→NETCONF stack integration
//   - Message-id monotonicity across sequential operations
//
// # Observability Impact
//
// All test failures print the specific operation name and the full error chain
// (testify's require/assert include the assertion context). The sub-test
// naming mirrors operation XML names so -run filters like:
//
//	go test ./netconf/conformance/... -run TestConformance_AllOperations_Base10/get-config -v
//
// isolate a single operation. Server-side handler panics propagate to the
// Serve goroutine and appear in the serveDone channel error, making them
// visible in test output even when the client-side assertion passes.
//
// Redaction: RPC bodies may contain device configuration in production. In
// tests, bodies are synthetic stubs with no real data. Do not add real
// credentials or configuration to this file.
package conformance_test

import (
	"bytes"
	"encoding/xml"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/client"
	"github.com/GabrielNunesIT/netconf/netconf/server"
	"github.com/GabrielNunesIT/netconf/netconf/transport"
	ncssh "github.com/GabrielNunesIT/netconf/netconf/transport/ssh"
	nctls "github.com/GabrielNunesIT/netconf/netconf/transport/tls"
	"github.com/GabrielNunesIT/netconf/netconf/nacm"
	"github.com/GabrielNunesIT/netconf/netconf/nmda"
	"github.com/GabrielNunesIT/netconf/netconf/subscriptions"
	"github.com/GabrielNunesIT/netconf/netconf/yanglibrary"
	"github.com/GabrielNunesIT/netconf/netconf/yangpush"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

// ── constants ─────────────────────────────────────────────────────────────────

// dataBody is the <data><config/></data> payload returned by mock get/get-config handlers.
const dataBody = `<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><config/></data>`

// caps10 is the base:1.0-only capability set (EOM framing).
var caps10 = netconf.NewCapabilitySet([]string{netconf.BaseCap10})

// caps11 is the base:1.0+1.1 capability set (chunked framing negotiated).
var caps11 = netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

// ── loopback pair helper ──────────────────────────────────────────────────────

// loopbackPair holds the state for a single in-process client↔server session.
type loopbackPair struct {
	cli       *client.Client
	srv       *server.Server
	serverSess *netconf.Session
}

// newLoopbackPair creates an in-process NETCONF session pair over a loopback
// transport and returns a loopbackPair.  clientCaps and serverCaps control
// capability advertisement (and therefore framing negotiation).  sessionID is
// the value assigned to the server session.
//
// The helper registers t.Cleanup to close both transport ends.
//
// The caller must start srv.Serve in its own goroutine after registering
// handlers — this design gives tests full control over the dispatch loop.
func newLoopbackPair(
	t *testing.T,
	clientCaps, serverCaps netconf.CapabilitySet,
	sessionID uint32,
) *loopbackPair {
	t.Helper()

	clientT, serverT := transport.NewLoopback()
	t.Cleanup(func() {
		clientT.Close()
		serverT.Close()
	})

	// ClientSession and ServerSession must run concurrently: the loopback
	// io.Pipe is synchronous and both hellos must flow simultaneously
	// (L005 from the project knowledge base).
	type sessResult struct {
		sess *netconf.Session
		err  error
	}
	cliCh := make(chan sessResult, 1)
	srvCh := make(chan sessResult, 1)

	go func() {
		s, err := netconf.ClientSession(clientT, clientCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, serverCaps, sessionID)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	cli := client.NewClient(cliRes.sess)
	t.Cleanup(func() { cli.Close() })

	return &loopbackPair{
		cli:        cli,
		srv:        server.NewServer(),
		serverSess: srvRes.sess,
	}
}

// startServe starts p.srv.Serve in a goroutine and returns a channel that
// receives the Serve return value when the loop exits.
func (p *loopbackPair) startServe(ctx context.Context) chan error {
	ch := make(chan error, 1)
	go func() { ch <- p.srv.Serve(ctx, p.serverSess) }()
	return ch
}

// waitServe waits up to 2 s for Serve to return, then fails the test.
func waitServe(t *testing.T, ch chan error) {
	t.Helper()
	select {
	case err := <-ch:
		require.NoError(t, err, "Serve must return nil after session terminates cleanly")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2 s — possible deadlock or missing close-session")
	}
}

// ── SSH pair helper ───────────────────────────────────────────────────────────

// generateTestSigner creates an ephemeral 2048-bit RSA signer for SSH tests.
func generateTestSigner(t *testing.T) gossh.Signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "generate RSA key")
	signer, err := gossh.NewSignerFromKey(priv)
	require.NoError(t, err, "create SSH signer")
	return signer
}

// testSSHConfigs returns a matched server + client SSH config pair.
// The server accepts any password; the client authenticates with "test"/"test".
func testSSHConfigs(t *testing.T) (*gossh.ServerConfig, *gossh.ClientConfig) {
	t.Helper()
	signer := generateTestSigner(t)
	serverCfg := &gossh.ServerConfig{
		PasswordCallback: func(_ gossh.ConnMetadata, _ []byte) (*gossh.Permissions, error) {
			return &gossh.Permissions{}, nil
		},
	}
	serverCfg.AddHostKey(signer)
	clientCfg := &gossh.ClientConfig{
		User:            "test",
		Auth:            []gossh.AuthMethod{gossh.Password("test")},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	return serverCfg, clientCfg
}

// sshPair holds the state for a full TCP→SSH→NETCONF stack pair.
type sshPair struct {
	cli        *client.Client
	srv        *server.Server
	serverSess *netconf.Session
	listener   *ncssh.Listener
}

// newSSHPair builds a full TCP→SSH→NETCONF loopback and returns an sshPair.
// caps is applied to both the client and server session.
func newSSHPair(t *testing.T, caps netconf.CapabilitySet) *sshPair {
	t.Helper()

	serverCfg, clientCfg := testSSHConfigs(t)

	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen on loopback")

	listener := ncssh.NewListener(nl, serverCfg)
	t.Cleanup(func() { listener.Close() })

	type srvResult struct {
		sess *netconf.Session
		trp  *ncssh.ServerTransport
		err  error
	}
	srvCh := make(chan srvResult, 1)
	go func() {
		trp, err := listener.Accept()
		if err != nil {
			srvCh <- srvResult{err: err}
			return
		}
		sess, err := netconf.ServerSession(trp, caps, 1)
		srvCh <- srvResult{sess: sess, trp: trp, err: err}
	}()

	addr := nl.Addr().String()
	clientTrp, err := ncssh.Dial(addr, clientCfg)
	require.NoError(t, err, "Dial SSH")
	t.Cleanup(func() { clientTrp.Close() })

	clientSess, err := netconf.ClientSession(clientTrp, caps)
	require.NoError(t, err, "ClientSession over SSH")

	sr := <-srvCh
	require.NoError(t, sr.err, "ServerSession over SSH")
	t.Cleanup(func() { sr.trp.Close() })

	cli := client.NewClient(clientSess)
	t.Cleanup(func() { cli.Close() })

	return &sshPair{
		cli:        cli,
		srv:        server.NewServer(),
		serverSess: sr.sess,
		listener:   listener,
	}
}

// startServe starts p.srv.Serve in a goroutine.
func (p *sshPair) startServe(ctx context.Context) chan error {
	ch := make(chan error, 1)
	go func() { ch <- p.srv.Serve(ctx, p.serverSess) }()
	return ch
}

// ── TLS pair helper ───────────────────────────────────────────────────────────

// caBundle holds a CA certificate and its private key.
// Duplicated from netconf/transport/tls/tls_test.go (unexported helpers cannot
// be imported cross-package — same pattern as generateTestSigner for SSH).
type caBundle struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// certBundle holds a signed certificate and its private key as TLS-ready PEM blocks.
type certBundle struct {
	cert    *x509.Certificate
	certPEM []byte
	keyPEM  []byte
}

// tlsConfigPair holds matched server and client TLS configs for a test.
type tlsConfigPair struct {
	server     *cryptotls.Config
	client     *cryptotls.Config
	clientCert *x509.Certificate
}

// generateTestCA creates a self-signed ECDSA P-256 CA certificate for use in tests.
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

// generateTestCert creates a certificate signed by ca using template.
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

// testTLSConfigs returns a matched server+client TLS config pair with mutual
// authentication enabled.
func testTLSConfigs(t *testing.T) *tlsConfigPair {
	t.Helper()

	ca := generateTestCA(t)

	caPool := x509.NewCertPool()
	caPool.AddCert(ca.cert)

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

// tlsPair holds the state for a full TCP→TLS→NETCONF stack pair.
type tlsPair struct {
	cli        *client.Client
	srv        *server.Server
	serverSess *netconf.Session
	listener   *nctls.Listener
}

// newTLSPair builds a full TCP→TLS→NETCONF loopback and returns a tlsPair.
// caps is applied to both the client and server session. sessionID is assigned
// to the server-side NETCONF session.
func newTLSPair(t *testing.T, caps netconf.CapabilitySet, sessionID uint32) *tlsPair {
	t.Helper()

	cfgs := testTLSConfigs(t)

	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen on loopback")

	listener := nctls.NewListener(nl, cfgs.server)
	t.Cleanup(func() { listener.Close() })

	type srvResult struct {
		sess *netconf.Session
		trp  *nctls.ServerTransport
		err  error
	}
	srvCh := make(chan srvResult, 1)
	go func() {
		trp, err := listener.Accept()
		if err != nil {
			srvCh <- srvResult{err: err}
			return
		}
		sess, err := netconf.ServerSession(trp, caps, sessionID)
		srvCh <- srvResult{sess: sess, trp: trp, err: err}
	}()

	addr := nl.Addr().String()
	ctx := context.Background()
	cli, err := client.DialTLS(ctx, addr, cfgs.client, caps)
	require.NoError(t, err, "DialTLS")
	t.Cleanup(func() { cli.Close() })

	sr := <-srvCh
	require.NoError(t, sr.err, "ServerSession over TLS")
	t.Cleanup(func() { sr.trp.Close() })

	return &tlsPair{
		cli:        cli,
		srv:        server.NewServer(),
		serverSess: sr.sess,
		listener:   listener,
	}
}

// startServe starts p.srv.Serve in a goroutine.
func (p *tlsPair) startServe(ctx context.Context) chan error {
	ch := make(chan error, 1)
	go func() { ch <- p.srv.Serve(ctx, p.serverSess) }()
	return ch
}

// ── operation table type ──────────────────────────────────────────────────────

// opCase is one row in the all-operations table.
type opCase struct {
	name    string
	handler server.HandlerFunc
	call    func(ctx context.Context, cli *client.Client) error
}

// allOperationCases returns the 13-entry table of RFC 6241 operations.
// Data-returning operations (get, get-config) assert DataReply non-nil.
func allOperationCases() []opCase {
	dataHandler := server.HandlerFunc(func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
		return []byte(dataBody), nil
	})
	okHandler := server.HandlerFunc(func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
		return nil, nil
	})

	running := netconf.Datastore{Running: &struct{}{}}
	candidate := netconf.Datastore{Candidate: &struct{}{}}

	return []opCase{
		{
			name:    "get",
			handler: dataHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				dr, err := cli.Get(ctx, nil)
				if err != nil {
					return err
				}
				if dr == nil {
					return fmt.Errorf("get: DataReply must not be nil")
				}
				if !strings.Contains(string(dr.Content), "config") {
					return fmt.Errorf("get: DataReply content must contain 'config', got: %s", dr.Content)
				}
				return nil
			},
		},
		{
			name:    "get-config",
			handler: dataHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				dr, err := cli.GetConfig(ctx, running, nil)
				if err != nil {
					return err
				}
				if dr == nil {
					return fmt.Errorf("get-config: DataReply must not be nil")
				}
				if !strings.Contains(string(dr.Content), "config") {
					return fmt.Errorf("get-config: DataReply content must contain 'config', got: %s", dr.Content)
				}
				return nil
			},
		},
		{
			name:    "edit-config",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.EditConfig(ctx, netconf.EditConfig{
					Target: running,
					Config: []byte(`<config/>`),
				})
			},
		},
		{
			name:    "copy-config",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.CopyConfig(ctx, netconf.CopyConfig{
					Target: running,
					Source: candidate,
				})
			},
		},
		{
			name:    "delete-config",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.DeleteConfig(ctx, netconf.DeleteConfig{
					Target: netconf.Datastore{Startup: &struct{}{}},
				})
			},
		},
		{
			name:    "lock",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.Lock(ctx, running)
			},
		},
		{
			name:    "unlock",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.Unlock(ctx, running)
			},
		},
		{
			name:    "kill-session",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.KillSession(ctx, 99)
			},
		},
		{
			name:    "validate",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.Validate(ctx, running)
			},
		},
		{
			name:    "commit",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.Commit(ctx, nil)
			},
		},
		{
			name:    "discard-changes",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.DiscardChanges(ctx)
			},
		},
		{
			name:    "cancel-commit",
			handler: okHandler,
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.CancelCommit(ctx, "")
			},
		},
		// close-session is the 13th operation and is exercised at the end of
		// each test to cleanly terminate the Serve loop. It is listed here for
		// completeness but called explicitly outside the table loop.
		{
			name:    "close-session",
			handler: nil, // built-in: no handler registration needed
			call: func(ctx context.Context, cli *client.Client) error {
				return cli.CloseSession(ctx)
			},
		},
	}
}

// runOperationTable registers each case's handler on srv, calls each
// operation via the client, and asserts no error.  close-session is skipped
// inside the loop (it terminates Serve); the caller must call CloseSession
// after the table to shut down Serve cleanly.
func runOperationTable(t *testing.T, ctx context.Context, p *loopbackPair, cases []opCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		if tc.name == "close-session" {
			continue // handled explicitly by the caller
		}
		if tc.handler != nil {
			p.srv.RegisterHandler(tc.name, tc.handler)
		}
	}

	serveDone := p.startServe(ctx)

	for _, tc := range cases {
		tc := tc
		if tc.name == "close-session" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.call(ctx, p.cli),
				"operation %q must succeed end-to-end", tc.name)
		})
	}

	// Terminate Serve cleanly.
	require.NoError(t, p.cli.CloseSession(ctx), "CloseSession must succeed")
	waitServe(t, serveDone)
}

// ── TestConformance_AllOperations_Base10 ──────────────────────────────────────

// TestConformance_AllOperations_Base10 exercises all 13 RFC 6241 operations
// end-to-end with base:1.0 (EOM) framing.
func TestConformance_AllOperations_Base10(t *testing.T) {
	p := newLoopbackPair(t, caps10, caps10, 1)
	ctx := context.Background()

	// Verify EOM framing.
	assert.Equal(t, netconf.FramingEOM, p.serverSess.FramingMode(),
		"base:1.0-only session must use EOM framing")

	cases := allOperationCases()
	runOperationTable(t, ctx, p, cases)
}

// ── TestConformance_AllOperations_Base11 ──────────────────────────────────────

// TestConformance_AllOperations_Base11 exercises all 13 RFC 6241 operations
// end-to-end with base:1.1 (chunked) framing.
func TestConformance_AllOperations_Base11(t *testing.T) {
	p := newLoopbackPair(t, caps11, caps11, 2)
	ctx := context.Background()

	// Verify chunked framing was negotiated.
	assert.Equal(t, netconf.FramingChunked, p.serverSess.FramingMode(),
		"base:1.0+1.1 session must negotiate chunked framing")

	cases := allOperationCases()
	runOperationTable(t, ctx, p, cases)
}

// ── TestConformance_ErrorPropagation ──────────────────────────────────────────

// TestConformance_ErrorPropagation proves the full error propagation chain for
// three scenarios: handler RPCError, non-RPCError, and unregistered operation.
func TestConformance_ErrorPropagation(t *testing.T) {
	ctx := context.Background()

	t.Run("RPCError-from-handler", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, 10)
		handlerErr := netconf.RPCError{
			Type:     "application",
			Tag:      "invalid-value",
			Severity: "error",
			Message:  "test error from handler",
		}
		p.srv.RegisterHandler("get-config", server.HandlerFunc(
			func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
				return nil, handlerErr
			},
		))
		serveDone := p.startServe(ctx)

		_, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
		require.Error(t, err, "GetConfig must fail when handler returns RPCError")

		var rpcErr netconf.RPCError
		require.True(t, errors.As(err, &rpcErr),
			"error must be (or wrap) netconf.RPCError; got: %v", err)
		assert.Equal(t, "application", rpcErr.Type)
		assert.Equal(t, "invalid-value", rpcErr.Tag)
		assert.Equal(t, "error", rpcErr.Severity)
		assert.Equal(t, "test error from handler", rpcErr.Message)

		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})

	t.Run("non-RPCError-from-handler", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, 11)
		p.srv.RegisterHandler("get-config", server.HandlerFunc(
			func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
				return nil, fmt.Errorf("boom: internal failure")
			},
		))
		serveDone := p.startServe(ctx)

		_, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
		require.Error(t, err, "GetConfig must fail when handler returns non-RPCError")

		var rpcErr netconf.RPCError
		require.True(t, errors.As(err, &rpcErr),
			"non-RPCError must be wrapped as netconf.RPCError; got: %v", err)
		assert.Equal(t, "operation-failed", rpcErr.Tag,
			"non-RPCError from handler must produce operation-failed tag")

		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})

	t.Run("operation-not-supported", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, 12)
		// No handler for "get" registered.
		serveDone := p.startServe(ctx)

		_, err := p.cli.Get(ctx, nil)
		require.Error(t, err, "Get must fail when no handler is registered")

		var rpcErr netconf.RPCError
		require.True(t, errors.As(err, &rpcErr),
			"unregistered operation must produce netconf.RPCError; got: %v", err)
		assert.Equal(t, "operation-not-supported", rpcErr.Tag,
			"unregistered operation must produce operation-not-supported tag")

		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})
}

// ── TestConformance_SessionLifecycle ─────────────────────────────────────────

// TestConformance_SessionLifecycle verifies session-id propagation,
// CloseSession termination, and KillSession dispatch.
func TestConformance_SessionLifecycle(t *testing.T) {
	ctx := context.Background()
	const assignedSessionID = uint32(42)

	t.Run("session-id-propagation", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, assignedSessionID)

		assert.Equal(t, assignedSessionID, p.serverSess.SessionID(),
			"server session must carry the assigned session-id")

		serveDone := p.startServe(ctx)
		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})

	t.Run("CloseSession-terminates-Serve", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, 43)
		serveDone := p.startServe(ctx)

		require.NoError(t, p.cli.CloseSession(ctx),
			"CloseSession must succeed")

		// Serve must return nil — built-in close-session intercept.
		select {
		case err := <-serveDone:
			require.NoError(t, err, "Serve must return nil after CloseSession")
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after CloseSession")
		}
	})

	t.Run("KillSession-dispatches-to-handler", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, 44)

		var (
			mu              sync.Mutex
			capturedBody    []byte
		)
		p.srv.RegisterHandler("kill-session", server.HandlerFunc(
			func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
				mu.Lock()
				capturedBody = append([]byte{}, rpc.Body...)
				mu.Unlock()
				return nil, nil
			},
		))
		serveDone := p.startServe(ctx)

		require.NoError(t, p.cli.KillSession(ctx, 99),
			"KillSession must succeed")

		mu.Lock()
		body := string(capturedBody)
		mu.Unlock()

		assert.True(t, len(body) > 0, "kill-session handler must have been called")
		assert.Contains(t, body, "99",
			"kill-session RPC body must contain the target session-id 99")

		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})
}

// ── TestConformance_FramingAutoNegotiation ────────────────────────────────────

// TestConformance_FramingAutoNegotiation proves framing negotiation for three
// capability combination scenarios using raw sessions (no client.Client).
func TestConformance_FramingAutoNegotiation(t *testing.T) {
	scenarios := []struct {
		name           string
		clientCaps     netconf.CapabilitySet
		serverCaps     netconf.CapabilitySet
		wantFraming    netconf.FramingMode
	}{
		{
			name:        "both-support-1.1",
			clientCaps:  caps11,
			serverCaps:  caps11,
			wantFraming: netconf.FramingChunked,
		},
		{
			name:        "client-only-1.1",
			clientCaps:  caps11,
			serverCaps:  caps10,
			wantFraming: netconf.FramingEOM,
		},
		{
			name:        "server-only-1.1",
			clientCaps:  caps10,
			serverCaps:  caps11,
			wantFraming: netconf.FramingEOM,
		},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
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
				s, err := netconf.ClientSession(clientT, sc.clientCaps)
				cliCh <- sessResult{s, err}
			}()
			go func() {
				s, err := netconf.ServerSession(serverT, sc.serverCaps, 1)
				srvCh <- sessResult{s, err}
			}()

			cliRes := <-cliCh
			srvRes := <-srvCh
			require.NoError(t, cliRes.err, "ClientSession must succeed")
			require.NoError(t, srvRes.err, "ServerSession must succeed")

			assert.Equal(t, sc.wantFraming, cliRes.sess.FramingMode(),
				"client session framing must match negotiation result")
			assert.Equal(t, sc.wantFraming, srvRes.sess.FramingMode(),
				"server session framing must match negotiation result")
		})
	}
}

// ── TestConformance_FilterTypes ───────────────────────────────────────────────

// TestConformance_FilterTypes proves that subtree and XPath filters reach the
// server handler on the wire with their type attribute and content intact.
func TestConformance_FilterTypes(t *testing.T) {
	ctx := context.Background()

	t.Run("subtree", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, 20)

		var (
			mu          sync.Mutex
			capturedBody []byte
		)
		p.srv.RegisterHandler("get-config", server.HandlerFunc(
			func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
				mu.Lock()
				capturedBody = append([]byte{}, rpc.Body...)
				mu.Unlock()
				return []byte(dataBody), nil
			},
		))
		serveDone := p.startServe(ctx)

		filter := &netconf.Filter{
			Type:    "subtree",
			Content: []byte(`<interfaces/>`),
		}
		dr, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, filter)
		require.NoError(t, err, "GetConfig with subtree filter must succeed")
		require.NotNil(t, dr, "DataReply must not be nil")

		mu.Lock()
		body := string(capturedBody)
		mu.Unlock()

		assert.Contains(t, body, `type="subtree"`,
			"RPC body must carry the subtree filter type attribute")
		assert.Contains(t, body, "interfaces",
			"RPC body must carry the subtree filter content")

		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})

	t.Run("xpath", func(t *testing.T) {
		p := newLoopbackPair(t, caps10, caps10, 21)

		var (
			mu          sync.Mutex
			capturedBody []byte
		)
		p.srv.RegisterHandler("get-config", server.HandlerFunc(
			func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
				mu.Lock()
				capturedBody = append([]byte{}, rpc.Body...)
				mu.Unlock()
				return []byte(dataBody), nil
			},
		))
		serveDone := p.startServe(ctx)

		filter := &netconf.Filter{
			Type:   "xpath",
			Select: "/interfaces",
		}
		dr, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, filter)
		require.NoError(t, err, "GetConfig with XPath filter must succeed")
		require.NotNil(t, dr, "DataReply must not be nil")

		mu.Lock()
		body := string(capturedBody)
		mu.Unlock()

		assert.Contains(t, body, `type="xpath"`,
			"RPC body must carry the xpath filter type attribute")
		assert.Contains(t, body, "/interfaces",
			"RPC body must carry the XPath select expression")

		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})
}

// ── TestConformance_SSHTransport ──────────────────────────────────────────────

// TestConformance_SSHTransport proves the full TCP→SSH→NETCONF stack:
// session establishment, GetConfig, and CloseSession via client.Client.
func TestConformance_SSHTransport(t *testing.T) {
	ctx := context.Background()
	p := newSSHPair(t, caps11)

	// Verify chunked framing was negotiated over SSH.
	assert.Equal(t, netconf.FramingChunked, p.serverSess.FramingMode(),
		"SSH pair with base:1.1 must negotiate chunked framing")

	p.srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(dataBody), nil
		},
	))
	serveDone := p.startServe(ctx)

	dr, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
	require.NoError(t, err, "GetConfig over SSH must succeed")
	require.NotNil(t, dr, "DataReply over SSH must not be nil")
	assert.Contains(t, string(dr.Content), "config",
		"DataReply must contain the handler-supplied data")

	require.NoError(t, p.cli.CloseSession(ctx), "CloseSession over SSH must succeed")

	select {
	case err := <-serveDone:
		require.NoError(t, err, "Serve must return nil after CloseSession over SSH")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession over SSH")
	}
}

// ── TestConformance_MessageIDMonotonicity ─────────────────────────────────────

// TestConformance_MessageIDMonotonicity drives 5 sequential operations and
// verifies that message-ids are distinct, parseable as integers, and
// monotonically increasing.
func TestConformance_MessageIDMonotonicity(t *testing.T) {
	ctx := context.Background()
	p := newLoopbackPair(t, caps10, caps10, 50)

	const n = 5
	var (
		mu  sync.Mutex
		ids []string
	)

	// Handler captures message-ids in order.
	p.srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			ids = append(ids, rpc.MessageID)
			mu.Unlock()
			return []byte(dataBody), nil
		},
	))
	serveDone := p.startServe(ctx)

	running := netconf.Datastore{Running: &struct{}{}}
	for i := 0; i < n; i++ {
		dr, err := p.cli.GetConfig(ctx, running, nil)
		require.NoError(t, err, "GetConfig %d must succeed", i+1)
		require.NotNil(t, dr, "GetConfig %d must return DataReply", i+1)
	}

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)

	mu.Lock()
	captured := append([]string{}, ids...)
	mu.Unlock()

	require.Len(t, captured, n, "must have captured exactly %d message-ids", n)

	// All IDs must be parseable as integers.
	parsedIDs := make([]int64, 0, n)
	for _, id := range captured {
		v, err := strconv.ParseInt(id, 10, 64)
		require.NoError(t, err,
			"message-id %q must be parseable as decimal integer", id)
		parsedIDs = append(parsedIDs, v)
	}

	// All IDs must be distinct.
	seen := make(map[int64]bool, n)
	for _, v := range parsedIDs {
		assert.False(t, seen[v], "message-id %d must not be duplicated", v)
		seen[v] = true
	}

	// IDs must be monotonically increasing.
	for i := 1; i < len(parsedIDs); i++ {
		assert.Greater(t, parsedIDs[i], parsedIDs[i-1],
			"message-id[%d]=%d must be greater than message-id[%d]=%d",
			i, parsedIDs[i], i-1, parsedIDs[i-1])
	}
}

// ── TestConformance_CapabilityNegotiation ─────────────────────────────────────

// TestConformance_CapabilityNegotiation verifies that the negotiated capability
// sets are accessible on both sides after session establishment.
func TestConformance_CapabilityNegotiation(t *testing.T) {
	ctx := context.Background()

	t.Run("client-sees-server-caps", func(t *testing.T) {
		// The client session's RemoteCapabilities must include the server's caps.
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
			s, err := netconf.ClientSession(clientT, caps10)
			cliCh <- sessResult{s, err}
		}()
		go func() {
			s, err := netconf.ServerSession(serverT, caps11, 1)
			srvCh <- sessResult{s, err}
		}()

		cliRes := <-cliCh
		srvRes := <-srvCh
		require.NoError(t, cliRes.err)
		require.NoError(t, srvRes.err)

		// Client's remote caps must contain base:1.1 (server advertised it).
		assert.True(t, cliRes.sess.RemoteCapabilities().Supports11(),
			"client must see server's base:1.1 capability in RemoteCapabilities")

		// Server's remote caps must contain only base:1.0 (client advertised only 1.0).
		assert.True(t, srvRes.sess.RemoteCapabilities().Supports10(),
			"server must see client's base:1.0 capability")
		assert.False(t, srvRes.sess.RemoteCapabilities().Supports11(),
			"server must NOT see base:1.1 (client did not advertise it)")

		_ = ctx
	})

	t.Run("local-caps-preserved", func(t *testing.T) {
		p := newLoopbackPair(t, caps11, caps10, 99)
		assert.True(t, p.serverSess.LocalCapabilities().Supports10())
		assert.False(t, p.serverSess.LocalCapabilities().Supports11(),
			"server local caps must be exactly what was passed to ServerSession")
		serveDone := p.startServe(ctx)
		require.NoError(t, p.cli.CloseSession(ctx))
		waitServe(t, serveDone)
	})
}

// ── TestConformance_ConcurrentOperations ─────────────────────────────────────

// TestConformance_ConcurrentOperations verifies that concurrent RPCs from
// multiple goroutines are correctly multiplexed and de-multiplexed by
// message-id over both EOM and chunked framing.
func TestConformance_ConcurrentOperations(t *testing.T) {
	for _, tc := range []struct {
		name string
		caps netconf.CapabilitySet
	}{
		{"eom", caps10},
		{"chunked", caps11},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := newLoopbackPair(t, tc.caps, tc.caps, 60)
			ctx := context.Background()

			p.srv.RegisterHandler("get-config", server.HandlerFunc(
				func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
					return []byte(dataBody), nil
				},
			))
			serveDone := p.startServe(ctx)

			const workers = 5
			errCh := make(chan error, workers)
			running := netconf.Datastore{Running: &struct{}{}}
			for i := 0; i < workers; i++ {
				go func() {
					dr, err := p.cli.GetConfig(ctx, running, nil)
					if err != nil {
						errCh <- err
						return
					}
					if dr == nil {
						errCh <- fmt.Errorf("DataReply must not be nil")
						return
					}
					errCh <- nil
				}()
			}
			for i := 0; i < workers; i++ {
				require.NoError(t, <-errCh, "concurrent GetConfig worker must succeed")
			}

			require.NoError(t, p.cli.CloseSession(ctx))
			waitServe(t, serveDone)
		})
	}
}

// ── TestConformance_AllOperations_NilFilter ───────────────────────────────────

// TestConformance_AllOperations_NilFilter confirms that Get and GetConfig with
// nil filter succeed (no filter element in the RPC body).
func TestConformance_AllOperations_NilFilter(t *testing.T) {
	ctx := context.Background()
	p := newLoopbackPair(t, caps10, caps10, 70)

	p.srv.RegisterHandler("get", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(dataBody), nil
		},
	))
	p.srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(dataBody), nil
		},
	))
	serveDone := p.startServe(ctx)

	dr1, err := p.cli.Get(ctx, nil)
	require.NoError(t, err, "Get with nil filter must succeed")
	require.NotNil(t, dr1)

	dr2, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
	require.NoError(t, err, "GetConfig with nil filter must succeed")
	require.NotNil(t, dr2)

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

// ── verify bytes.Contains helper used in tests is not needed (stdlib available)

// bytesContains is used for clarity in body inspection assertions.
func bytesContains(b []byte, s string) bool {
	return bytes.Contains(b, []byte(s))
}

// _ forces the import used only in the above helper to be recognised by the
// compiler. bytes is imported and used in bytesContains above.
var _ = bytesContains

// ── TestConformance_WithDefaults_GetConfig ────────────────────────────────────

// TestConformance_WithDefaults_GetConfig proves that a GetConfig with
// WithDefaults set to report-all emits the with-defaults parameter in the
// wire XML that the server handler receives.
//
// This closes the with-defaults round-trip proof from T01 unit tests by
// exercising the full client→server stack (not just xml.Marshal).
func TestConformance_WithDefaults_GetConfig(t *testing.T) {
	ctx := context.Background()
	p := newLoopbackPair(t, caps10, caps10, 100)

	var (
		mu           sync.Mutex
		capturedBody []byte
	)
	p.srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = append([]byte{}, rpc.Body...)
			mu.Unlock()
			return []byte(dataBody), nil
		},
	))
	serveDone := p.startServe(ctx)

	running := netconf.Datastore{Running: &struct{}{}}
	dr, err := p.cli.Do(ctx, &netconf.GetConfig{
		Source:       running,
		WithDefaults: &netconf.WithDefaultsParam{Mode: netconf.WithDefaultsReportAll},
	})
	require.NoError(t, err, "GetConfig with WithDefaults must succeed")
	require.NotNil(t, dr, "reply must not be nil")

	mu.Lock()
	body := string(capturedBody)
	mu.Unlock()

	assert.Contains(t, body, "with-defaults",
		"server-side RPC body must contain with-defaults element; body: %s", body)
	assert.Contains(t, body, "report-all",
		"server-side RPC body must contain the report-all mode value; body: %s", body)
	assert.Contains(t, body, "ietf-netconf-with-defaults",
		"server-side RPC body must carry the RFC 6243 YANG namespace; body: %s", body)

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

// ── TestConformance_WithDefaults_BackwardCompat ───────────────────────────────

// TestConformance_WithDefaults_BackwardCompat proves that GetConfig with a nil
// WithDefaults field emits NO with-defaults element on the wire — existing
// callers are unaffected by the new field.
func TestConformance_WithDefaults_BackwardCompat(t *testing.T) {
	ctx := context.Background()
	p := newLoopbackPair(t, caps10, caps10, 101)

	var (
		mu           sync.Mutex
		capturedBody []byte
	)
	p.srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = append([]byte{}, rpc.Body...)
			mu.Unlock()
			return []byte(dataBody), nil
		},
	))
	serveDone := p.startServe(ctx)

	running := netconf.Datastore{Running: &struct{}{}}
	// Use the typed GetConfig method with nil WithDefaults (the pre-existing signature).
	dr, err := p.cli.GetConfig(ctx, running, nil)
	require.NoError(t, err, "GetConfig with nil WithDefaults must succeed")
	require.NotNil(t, dr, "DataReply must not be nil")

	mu.Lock()
	body := string(capturedBody)
	mu.Unlock()

	assert.NotContains(t, body, "with-defaults",
		"RPC body must NOT contain with-defaults when field is nil; body: %s", body)
	assert.NotContains(t, body, "ietf-netconf-with-defaults",
		"RPC body must NOT carry the with-defaults namespace when field is nil; body: %s", body)

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

// ── TestConformance_PartialLock ───────────────────────────────────────────────

// TestConformance_PartialLock proves the full partial-lock round-trip:
// (a) the wire RPC body contains the <select> elements;
// (b) the client correctly unmarshals lock-id and locked-node from the reply.
func TestConformance_PartialLock(t *testing.T) {
	ctx := context.Background()
	p := newLoopbackPair(t, caps10, caps10, 102)

	var (
		mu           sync.Mutex
		capturedBody []byte
	)
	p.srv.RegisterHandler("partial-lock", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = append([]byte{}, rpc.Body...)
			mu.Unlock()
			// Return a partial-lock-reply with lock-id=7 and one locked node.
			return []byte(`<partial-lock-reply>` +
				`<lock-id>7</lock-id>` +
				`<locked-node>/interfaces</locked-node>` +
				`</partial-lock-reply>`), nil
		},
	))
	serveDone := p.startServe(ctx)

	reply, err := p.cli.PartialLock(ctx, []string{"/interfaces"})
	require.NoError(t, err, "PartialLock must succeed")
	require.NotNil(t, reply, "PartialLockReply must not be nil")

	// Verify round-trip: lock-id and locked-node decoded correctly.
	assert.Equal(t, uint32(7), reply.LockID,
		"PartialLockReply.LockID must be 7 (as returned by the handler)")
	require.Len(t, reply.LockedNode, 1,
		"PartialLockReply must contain exactly 1 locked-node")
	assert.Equal(t, "/interfaces", reply.LockedNode[0],
		"locked-node[0] must equal /interfaces")

	// Verify wire format: server handler received <select> elements.
	mu.Lock()
	body := string(capturedBody)
	mu.Unlock()

	assert.Contains(t, body, "<select>",
		"partial-lock RPC body must contain <select> element; body: %s", body)
	assert.Contains(t, body, "/interfaces",
		"partial-lock RPC body must contain the XPath expression; body: %s", body)

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

// ── TestConformance_PartialUnlock ─────────────────────────────────────────────

// TestConformance_PartialUnlock proves the full partial-unlock round-trip:
// the wire RPC body contains the <lock-id> element with the correct value,
// and the client receives a clean nil error for the <ok/> reply.
func TestConformance_PartialUnlock(t *testing.T) {
	ctx := context.Background()
	p := newLoopbackPair(t, caps10, caps10, 103)

	var (
		mu           sync.Mutex
		capturedBody []byte
	)
	p.srv.RegisterHandler("partial-unlock", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = append([]byte{}, rpc.Body...)
			mu.Unlock()
			// Return nil to let the server send <ok/>.
			return nil, nil
		},
	))
	serveDone := p.startServe(ctx)

	err := p.cli.PartialUnlock(ctx, 7)
	require.NoError(t, err, "PartialUnlock must succeed when server replies with <ok/>")

	// Verify wire format: server handler received <lock-id>7</lock-id>.
	mu.Lock()
	body := string(capturedBody)
	mu.Unlock()

	assert.Contains(t, body, "<lock-id>7</lock-id>",
		"partial-unlock RPC body must contain <lock-id>7</lock-id>; body: %s", body)

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

// ── TestConformance_TLSTransport ──────────────────────────────────────────────

// TestConformance_TLSTransport proves the full TCP→TLS→NETCONF stack:
// mutual X.509 authentication, chunked framing negotiation, GetConfig, and
// CloseSession via client.Client.  Mirrors TestConformance_SSHTransport but
// uses TLS instead of SSH.
func TestConformance_TLSTransport(t *testing.T) {
	ctx := context.Background()
	p := newTLSPair(t, caps11, 200)

	// Verify chunked framing was negotiated over TLS.
	assert.Equal(t, netconf.FramingChunked, p.serverSess.FramingMode(),
		"TLS pair with base:1.1 must negotiate chunked framing")

	p.srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(dataBody), nil
		},
	))
	serveDone := p.startServe(ctx)

	dr, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
	require.NoError(t, err, "GetConfig over TLS must succeed")
	require.NotNil(t, dr, "DataReply over TLS must not be nil")
	assert.Contains(t, string(dr.Content), "config",
		"DataReply must contain the handler-supplied data")

	require.NoError(t, p.cli.CloseSession(ctx), "CloseSession over TLS must succeed")

	select {
	case err := <-serveDone:
		require.NoError(t, err, "Serve must return nil after CloseSession over TLS")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession over TLS")
	}
}

// ── TestConformance_NotificationsOverTLS ─────────────────────────────────────

// TestConformance_NotificationsOverTLS proves that notifications and concurrent
// RPCs work correctly over a TLS-connected session (S01 + S02 cross-feature).
//
// Send-side race avoidance (D037, P015): all 3 notifications are sent after the
// create-subscription handler signals readiness (subscribedCh closed) but
// before the GetConfig RPC is issued. During this window, Serve is blocked in
// sess.Recv() and does not call sess.Send, so there is no concurrent write race
// on the server session.
func TestConformance_NotificationsOverTLS(t *testing.T) {
	ctx := context.Background()
	tlsNotifCaps := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		netconf.BaseCap11,
		netconf.CapabilityNotification,
		netconf.CapabilityInterleave,
	})
	p := newTLSPair(t, tlsNotifCaps, 201)

	subscribedCh := make(chan struct{})

	p.srv.RegisterHandler("create-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			close(subscribedCh)
			return nil, nil // nil, nil → <ok/>
		},
	))
	p.srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(dataBody), nil
		},
	))
	serveDone := p.startServe(ctx)

	// Subscribe and obtain the notification channel.
	notifCh, err := p.cli.Subscribe(ctx, netconf.CreateSubscription{})
	require.NoError(t, err, "Subscribe over TLS must succeed")
	require.NotNil(t, notifCh, "Subscribe must return a non-nil channel")

	// Wait for the create-subscription handler to signal, then send 3 notifications.
	// Serve is blocked in sess.Recv() at this point — no concurrent sess.Send.
	select {
	case <-subscribedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("create-subscription handler did not signal within 2s")
	}

	const numNotifs = 3
	for i := range numNotifs {
		n := &netconf.Notification{
			EventTime: fmt.Sprintf("2026-03-01T00:00:0%dZ", i),
			Body:      []byte(fmt.Sprintf(`<tls-event seq="%d"/>`, i)),
		}
		require.NoError(t, server.SendNotification(p.serverSess, n),
			"SendNotification %d must succeed over TLS", i)
	}

	// Drain all 3 notifications from the client channel.
	received := make([]*netconf.Notification, 0, numNotifs)
	timeout := time.After(5 * time.Second)
	for len(received) < numNotifs {
		select {
		case n, open := <-notifCh:
			if !open {
				t.Fatalf("notification channel closed before all notifications arrived (got %d/%d)", len(received), numNotifs)
			}
			received = append(received, n)
		case <-timeout:
			t.Fatalf("timeout waiting for notifications over TLS: got %d/%d", len(received), numNotifs)
		}
	}

	// Assert all 3 notifications arrived in order with correct content.
	require.Len(t, received, numNotifs, "all %d notifications must arrive over TLS", numNotifs)
	for i, n := range received {
		expectedTime := fmt.Sprintf("2026-03-01T00:00:0%dZ", i)
		assert.Equal(t, expectedTime, n.EventTime, "notification %d EventTime must match", i)
		assert.Contains(t, string(n.Body), fmt.Sprintf("seq=\"%d\"", i),
			"notification %d Body must contain seq attribute", i)
	}

	// Prove interleave: execute 1 GetConfig RPC concurrently with the session.
	dr, err := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
	require.NoError(t, err, "GetConfig over TLS after notifications must succeed")
	require.NotNil(t, dr, "GetConfig must return a DataReply")
	assert.Contains(t, string(dr.Content), "config",
		"DataReply content must contain 'config'")

	require.NoError(t, p.cli.CloseSession(ctx), "CloseSession must succeed")
	waitServe(t, serveDone)
}

// ── TestConformance_CapabilityAdvertisement ───────────────────────────────────

// TestConformance_CapabilityAdvertisement proves that all four M002 extension
// capability constants are visible in the client's RemoteCapabilities after a
// hello exchange with a server that advertises them all.
//
// Uses raw sessions (not client.Client) so RemoteCapabilities is directly
// accessible on the *netconf.Session — same approach as
// TestConformance_CapabilityNegotiation.
func TestConformance_CapabilityAdvertisement(t *testing.T) {
	// Server advertises all M002 extension capabilities.
	serverCaps := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		netconf.BaseCap11,
		netconf.CapabilityNotification,
		netconf.CapabilityInterleave,
		netconf.CapabilityWithDefaults,
		netconf.CapabilityPartialLock,
	})
	// Client advertises only base capabilities — we're testing what the
	// server sends, not what the client sends.
	clientCaps := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		netconf.BaseCap11,
	})

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
		s, err := netconf.ClientSession(clientT, clientCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, serverCaps, 202)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	// The client's RemoteCapabilities reflects what the server advertised.
	remoteCaps := cliRes.sess.RemoteCapabilities()

	assert.True(t, remoteCaps.Contains(netconf.CapabilityNotification),
		"client must see CapabilityNotification in RemoteCapabilities")
	assert.True(t, remoteCaps.Contains(netconf.CapabilityInterleave),
		"client must see CapabilityInterleave in RemoteCapabilities")
	assert.True(t, remoteCaps.Contains(netconf.CapabilityWithDefaults),
		"client must see CapabilityWithDefaults in RemoteCapabilities")
	assert.True(t, remoteCaps.Contains(netconf.CapabilityPartialLock),
		"client must see CapabilityPartialLock in RemoteCapabilities")
}

// ── TestConformance_M003_CapabilityAdvertisement ──────────────────────────────

// TestConformance_M003_CapabilityAdvertisement proves that all M003 YANG module
// namespace URIs (subscriptions, nmda, yanglibrary, yangpush, nacm) are visible
// in the client's RemoteCapabilities after a hello exchange.
func TestConformance_M003_CapabilityAdvertisement(t *testing.T) {
	serverCaps := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		subscriptions.CapabilityURI,
		subscriptions.CapabilityURINetconf,
		nmda.CapabilityURI,
		yanglibrary.CapabilityURI,
		yangpush.CapabilityURI,
		nacm.CapabilityURI,
	})
	clientCaps := netconf.NewCapabilitySet([]string{netconf.BaseCap10})

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
		s, err := netconf.ClientSession(clientT, clientCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, serverCaps, 500)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	remoteCaps := cliRes.sess.RemoteCapabilities()

	assert.True(t, remoteCaps.Contains(subscriptions.CapabilityURI),
		"client must see subscriptions CapabilityURI")
	assert.True(t, remoteCaps.Contains(subscriptions.CapabilityURINetconf),
		"client must see subscriptions CapabilityURINetconf")
	assert.True(t, remoteCaps.Contains(nmda.CapabilityURI),
		"client must see nmda CapabilityURI")
	assert.True(t, remoteCaps.Contains(yanglibrary.CapabilityURI),
		"client must see yanglibrary CapabilityURI")
	assert.True(t, remoteCaps.Contains(yangpush.CapabilityURI),
		"client must see yangpush CapabilityURI")
	assert.True(t, remoteCaps.Contains(nacm.CapabilityURI),
		"client must see nacm CapabilityURI")
}

// ── TestConformance_NMDA_GetData ──────────────────────────────────────────────

// TestConformance_NMDA_GetData proves get-data RPC delivery and DataReply decoding.
func TestConformance_NMDA_GetData(t *testing.T) {
	nmdaCapSet := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		nmda.CapabilityURI,
	})
	p := newLoopbackPair(t, nmdaCapSet, nmdaCapSet, 501)

	var mu sync.Mutex
	var capturedBody []byte

	const opState = `<op-state xmlns="urn:example:test"><running>true</running></op-state>`
	p.srv.RegisterHandler("get-data", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = make([]byte, len(rpc.Body))
			copy(capturedBody, rpc.Body)
			mu.Unlock()
			return []byte(`<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` + opState + `</data>`), nil
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- p.srv.Serve(ctx, p.serverSess)
	}()

	dr, err := p.cli.GetData(ctx, nmda.GetData{
		Datastore: nmda.DatastoreRef{Name: nmda.DatastoreOperational},
	})
	require.NoError(t, err, "GetData must succeed")
	require.NotNil(t, dr, "GetData must return a DataReply")
	assert.Contains(t, string(dr.Content), "op-state",
		"DataReply content must contain the handler-supplied data")

	mu.Lock()
	body := capturedBody
	mu.Unlock()
	require.NotEmpty(t, body, "handler must have captured the RPC body")
	bodyStr := string(body)
	assert.Contains(t, bodyStr, nmda.NmdaNS, "RPC body must carry NMDA namespace")
	assert.Contains(t, bodyStr, nmda.DatastoreOperational, "RPC body must specify operational datastore")

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

// ── TestConformance_NACM_Enforcement ─────────────────────────────────────────

// nacmRPCLocalName extracts the XML local name of the first start element in b.
func nacmRPCLocalName(b []byte) string {
	xd := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := xd.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

// nacmGuard wraps a server.Handler with NACM enforcement using nacm.Enforce.
// user and groups are passed explicitly since netconf.Session has no auth identity.
func nacmGuard(cfg nacm.Nacm, user string, groups []string, inner server.Handler) server.Handler {
	return server.HandlerFunc(func(ctx context.Context, sess *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
		opName := nacmRPCLocalName(rpc.Body)
		req := nacm.Request{
			User:          user,
			Groups:        groups,
			OperationType: nacm.OpProtocolOperation,
			OperationName: opName,
			ModuleName:    "*",
		}
		switch nacm.Enforce(cfg, req) {
		case nacm.Permit:
			return inner.Handle(ctx, sess, rpc)
		default: // Deny or DefaultDeny
			return nil, netconf.RPCError{
				Type:     "protocol",
				Tag:      "access-denied",
				Severity: "error",
				Message:  "NACM access denied",
			}
		}
	})
}

// TestConformance_NACM_Enforcement proves the nacmGuard middleware pattern:
//  1. Get with 'admin' group → Permit (rule allows).
//  2. EditConfig with 'admin' group → Deny (rule denies).
//  3. GetConfig with 'admin' group → DefaultDeny (no matching rule).
func TestConformance_NACM_Enforcement(t *testing.T) {
	p := newLoopbackPair(t, caps10, caps10, 502)

	cfg := nacm.Nacm{
		EnableNacm: true,
		RuleLists: []nacm.RuleList{
			{
				Name:  "admin-rules",
				Group: []string{"admin"},
				Rules: []nacm.Rule{
					{
						Name:              "allow-get",
						ModuleName:        "*",
						ProtocolOperation: &nacm.ProtocolOperationRule{RPCName: "get"},
						AccessOperations:  "exec",
						Action:            nacm.ActionPermit,
					},
					{
						Name:              "deny-edit-config",
						ModuleName:        "*",
						ProtocolOperation: &nacm.ProtocolOperationRule{RPCName: "edit-config"},
						AccessOperations:  "exec",
						Action:            nacm.ActionDeny,
					},
				},
			},
		},
	}

	p.srv.RegisterHandler("get", nacmGuard(cfg, "alice", []string{"admin"}, server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(dataBody), nil
		},
	)))
	p.srv.RegisterHandler("edit-config", nacmGuard(cfg, "alice", []string{"admin"}, server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil
		},
	)))
	p.srv.RegisterHandler("get-config", nacmGuard(cfg, "alice", []string{"admin"}, server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(dataBody), nil
		},
	)))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- p.srv.Serve(ctx, p.serverSess)
	}()

	// Test 1: Get → Permit.
	dr, err := p.cli.Get(ctx, nil)
	require.NoError(t, err, "Get must succeed with admin group (NACM permit)")
	require.NotNil(t, dr, "Get must return a DataReply")

	// Test 2: EditConfig → Deny.
	editErr := p.cli.EditConfig(ctx, netconf.EditConfig{
		Target: netconf.Datastore{Running: &struct{}{}},
		Config: []byte(`<config/>`),
	})
	require.Error(t, editErr, "EditConfig must fail with NACM deny")
	var rpcErr netconf.RPCError
	require.ErrorAs(t, editErr, &rpcErr, "error must be an RPCError")
	assert.Equal(t, "access-denied", rpcErr.Tag, "RPCError tag must be access-denied")

	// Test 3: GetConfig → DefaultDeny (no matching rule for get-config).
	_, gcErr := p.cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
	require.Error(t, gcErr, "GetConfig must fail with NACM default-deny")
	var rpcErr2 netconf.RPCError
	require.ErrorAs(t, gcErr, &rpcErr2, "error must be an RPCError")
	assert.Equal(t, "access-denied", rpcErr2.Tag, "DefaultDeny RPCError tag must be access-denied")

	require.NoError(t, p.cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

// ── TestConformance_Subscriptions_CallHome_TLS ────────────────────────────────

// TestConformance_Subscriptions_CallHome_TLS proves subscriptions over TLS call-home:
//  1. Server dials TLS call-home to a client listener.
//  2. Client accepts, NETCONF hello with subscription capabilities.
//  3. EstablishSubscription → server returns id=1.
//  4. Server sends PushUpdate notification.
//  5. Client receives the notification on notifCh.
func TestConformance_Subscriptions_CallHome_TLS(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "generate CA key")
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caCertDER)
	require.NoError(t, err)

	makeLeafCert := func(cn string) (cryptotls.Certificate, error) {
		leafKey, kErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if kErr != nil {
			return cryptotls.Certificate{}, kErr
		}
		leafTemplate := &x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Minute),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			DNSNames:     []string{"localhost"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		}
		der, cErr := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
		if cErr != nil {
			return cryptotls.Certificate{}, cErr
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyDER, mErr := x509.MarshalECPrivateKey(leafKey)
		if mErr != nil {
			return cryptotls.Certificate{}, mErr
		}
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		return cryptotls.X509KeyPair(certPEM, keyPEM)
	}

	serverCert, err := makeLeafCert("test-server")
	require.NoError(t, err)
	clientCert, err := makeLeafCert("test-client")
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	serverTLSConfig := &cryptotls.Config{
		// The NETCONF server acts as TLS server in call-home (accepts TLS from client).
		Certificates: []cryptotls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   cryptotls.RequireAndVerifyClientCert,
	}
	clientTLSConfig := &cryptotls.Config{
		// The NETCONF client acts as TLS client in call-home (initiates TLS to server).
		Certificates: []cryptotls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   "localhost",
	}

	chSubCaps := netconf.NewCapabilitySet([]string{
		netconf.BaseCap10,
		netconf.CapabilityNotification,
		netconf.CapabilityInterleave,
		subscriptions.CapabilityURI,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	serverErrCh := make(chan error, 1)
	var serverSess *netconf.Session
	var serverSessMu sync.Mutex

	go func() {
		trp, dialErr := nctls.DialCallHome(ln.Addr().String(), serverTLSConfig)
		if dialErr != nil {
			serverErrCh <- dialErr
			return
		}
		sess, sessErr := netconf.ServerSession(trp, chSubCaps, 503)
		if sessErr != nil {
			serverErrCh <- sessErr
			return
		}
		serverSessMu.Lock()
		serverSess = sess
		serverSessMu.Unlock()
		serverErrCh <- nil
	}()

	cli, err := client.AcceptCallHomeTLS(context.Background(), ln, clientTLSConfig, chSubCaps)
	require.NoError(t, err, "AcceptCallHomeTLS must succeed")
	defer func() { _ = cli.Close() }()

	require.NoError(t, <-serverErrCh, "server-side TLS call home must succeed")

	srv := server.NewServer()
	subscribedCh := make(chan struct{})

	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			close(subscribedCh)
			b, mErr := xml.Marshal(subscriptions.EstablishSubscriptionReply{ID: 1})
			if mErr != nil {
				return nil, mErr
			}
			return b, nil
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	serverSessMu.Lock()
	ss := serverSess
	serverSessMu.Unlock()
	go func() {
		serveDone <- srv.Serve(ctx, ss)
	}()

	id, notifCh, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{
		Stream: "NETCONF",
	})
	require.NoError(t, err, "EstablishSubscription over TLS call-home must succeed")
	assert.Equal(t, subscriptions.SubscriptionID(1), id)

	select {
	case <-subscribedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("establish-subscription handler did not signal within 2s")
	}

	// Send PushUpdate notification — Serve is blocked in Recv(), no race.
	pushBody, mErr := xml.Marshal(yangpush.PushUpdate{
		ID:              1,
		ObservationTime: "2026-01-01T00:00:00Z",
		Datastore:       nmda.DatastoreOperational,
		Updates:         []byte(`<data xmlns="urn:example:test"><value>42</value></data>`),
	})
	require.NoError(t, mErr)

	serverSessMu.Lock()
	sendErr := server.SendNotification(serverSess, &netconf.Notification{
		EventTime: "2026-01-01T00:00:00Z",
		Body:      pushBody,
	})
	serverSessMu.Unlock()
	require.NoError(t, sendErr, "SendNotification over TLS call-home must succeed")

	select {
	case received, open := <-notifCh:
		require.True(t, open, "notification channel must be open")
		assert.Contains(t, string(received.Body), "push-update",
			"notification body must contain push-update from yangpush")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for push-update notification over TLS call-home")
	}

	require.NoError(t, cli.CloseSession(ctx))
	waitServe(t, serveDone)
}

