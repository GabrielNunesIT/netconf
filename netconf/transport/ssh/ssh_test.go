// ssh_test.go — integration tests for the SSH NETCONF transport.
//
// Tests use loopback TCP connections on random ports with ephemeral RSA keys,
// so they run without any external infrastructure or configuration.
// net.Pipe() cannot be used for SSH handshakes because both sides write the
// version banner first, which deadlocks on the unbuffered pipe (see the
// x/crypto/ssh test suite for the same workaround).
//
// Test inventory:
//   - TestSSH_HelloBase11 — full hello exchange with base:1.1 upgrade;
//     verifies chunked framing is active after hello and a message round-trips.
//   - TestSSH_HelloBase10Only — hello exchange where server advertises only
//     base:1.0; verifies framing stays EOM.
//   - TestSSH_NonNetconfSubsystemRejected — client requests a non-"netconf"
//     subsystem; verifies server rejects it.
//
// Observability: `go test ./netconf/transport/ssh/... -v` prints per-test
// PASS/FAIL with full error context. Server goroutine errors surface via
// errCh/resultCh so they appear in test failure messages rather than being
// silently swallowed.
package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

// generateTestSigner returns a fresh 2048-bit RSA gossh.Signer for the test
// SSH server. Keys are ephemeral; no disk I/O occurs.
func generateTestSigner(t *testing.T) gossh.Signer {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "generate RSA key")
	signer, err := gossh.NewSignerFromKey(privKey)
	require.NoError(t, err, "create SSH signer")
	return signer
}

// testSSHConfigs returns a matched server + client SSH config pair.
// The server accepts any password; the client sends "test/test".
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

// newInProcessSSHPair creates a matched server Listener and a client Transport
// connected via loopback TCP on a random port. net.Pipe() cannot be used here
// because both sides of an SSH handshake start by writing the version banner,
// which deadlocks on the unbuffered net.Pipe (confirmed by x/crypto/ssh tests
// which use the same workaround). Both sides are ready for hello exchange on
// return. The caller must close both.
func newInProcessSSHPair(t *testing.T) (*Listener, *Transport) {
	t.Helper()

	serverCfg, clientCfg := testSSHConfigs(t)

	// Bind on a random loopback port.
	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen on loopback")

	listener := NewListener(nl, serverCfg)

	// Client: connect to the server and open the netconf subsystem.
	addr := nl.Addr().String()
	clientTrp, err := Dial(addr, clientCfg)
	require.NoError(t, err, "client Dial + open netconf subsystem")

	return listener, clientTrp
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestSSH_HelloBase11 verifies the full NETCONF hello exchange over SSH with
// base:1.1 capability negotiation (chunked framing auto-upgrade).
//
// Checks:
//   - client receives the server-assigned session-id (42)
//   - both sides report FramingChunked after hello
//   - a message written in chunked framing on the client side is readable
//     on the server side (proves the transport is actually switched)
func TestSSH_HelloBase11(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	listener, clientTrp := newInProcessSSHPair(t)
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

// TestSSH_HelloBase10Only verifies that when the server advertises only
// base:1.0, framing remains in EOM mode on both sides.
func TestSSH_HelloBase10Only(t *testing.T) {
	serverCaps := netconf.NewCapabilitySet([]string{netconf.BaseCap10})
	clientCaps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	listener, clientTrp := newInProcessSSHPair(t)
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

// TestSSH_NonNetconfSubsystemRejected verifies that requesting a subsystem
// other than "netconf" is rejected by the server with a failure reply.
func TestSSH_NonNetconfSubsystemRejected(t *testing.T) {
	serverCfg, clientCfg := testSSHConfigs(t)

	// Use real TCP (loopback) to avoid net.Pipe deadlock during SSH handshake.
	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	defer nl.Close()

	// Minimal server: accept one connection, serve "session" channels, reject non-"netconf".
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := nl.Accept()
		if err != nil {
			return
		}
		sshConn, chans, reqs, err := gossh.NewServerConn(conn, serverCfg)
		if err != nil {
			return
		}
		defer sshConn.Close()
		go gossh.DiscardRequests(reqs)

		for newChan := range chans {
			if newChan.ChannelType() != "session" {
				_ = newChan.Reject(gossh.UnknownChannelType, "only session")
				continue
			}
			ch, chanReqs, err := newChan.Accept()
			if err != nil {
				continue
			}
			// Service channel requests: reject non-"netconf" subsystem names.
			go func(ch gossh.Channel, reqs <-chan *gossh.Request) {
				defer ch.Close()
				for req := range reqs {
					if req.Type == "subsystem" {
						name := parseSubsystemName(req.Payload)
						if name == "netconf" {
							if req.WantReply {
								_ = req.Reply(true, nil)
							}
						} else {
							// Reject: non-netconf subsystem.
							if req.WantReply {
								_ = req.Reply(false, nil)
							}
							return
						}
					} else {
						if req.WantReply {
							_ = req.Reply(false, nil)
						}
					}
				}
			}(ch, chanReqs)
		}
	}()

	// Client SSH connection to the server.
	addr := nl.Addr().String()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err, "dial")

	sshConn, chans, sshReqs, err := gossh.NewClientConn(conn, addr, clientCfg)
	require.NoError(t, err, "client SSH handshake")
	go gossh.DiscardRequests(sshReqs)
	go func() {
		for ch := range chans {
			_ = ch.Reject(gossh.UnknownChannelType, "")
		}
	}()

	client := gossh.NewClient(sshConn, chans, sshReqs)

	sess, err := client.NewSession()
	require.NoError(t, err, "open session channel")

	// Request a non-"netconf" subsystem — server must reject it.
	err = sess.RequestSubsystem("shell")
	assert.Error(t, err, "non-netconf subsystem request should be rejected by server")

	// Close the session and SSH connection explicitly so the server's channel
	// range loop exits (the server loops until the connection closes).
	_ = sess.Close()
	_ = sshConn.Close()

	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for server to exit")
	}
}
