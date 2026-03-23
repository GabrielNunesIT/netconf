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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
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
