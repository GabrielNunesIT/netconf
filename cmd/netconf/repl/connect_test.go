package repl_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"testing"
	"time"

	"github.com/GabrielNunesIT/netconf/cmd/netconf/repl"
	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/client"
	"github.com/GabrielNunesIT/netconf/netconf/server"
	ncssh "github.com/GabrielNunesIT/netconf/netconf/transport/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

// ── SSH loopback helpers ──────────────────────────────────────────────────────

// startLoopbackSSHServer starts a minimal NETCONF-over-SSH server on a random
// loopback port. It accepts any password and serves a single connection.
// Returns the listening address and a cleanup function.
func startLoopbackSSHServer(t *testing.T, caps netconf.CapabilitySet, sessionID uint32) (addr string, cleanup func()) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	require.NoError(t, err)

	serverCfg := &gossh.ServerConfig{
		PasswordCallback: func(_ gossh.ConnMetadata, _ []byte) (*gossh.Permissions, error) {
			return &gossh.Permissions{}, nil
		},
	}
	serverCfg.AddHostKey(signer)

	nl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ln := ncssh.NewListener(nl, serverCfg)

	// Accept exactly one connection in a goroutine and serve a minimal NETCONF
	// server (get-config handler returning <data><config/></data>).
	go func() {
		trp, acceptErr := ln.Accept()
		if acceptErr != nil {
			return // listener closed
		}
		sess, sessErr := netconf.ServerSession(trp, caps, sessionID)
		if sessErr != nil {
			_ = trp.Close()
			return
		}

		srv := server.NewServer()
		srv.RegisterHandler("get-config", server.HandlerFunc(
			func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
				return []byte(`<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><config/></data>`), nil
			},
		))
		srv.RegisterHandler("close-session", server.HandlerFunc(
			func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
				return nil, nil
			},
		))

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Serve(ctx, sess)
		_ = trp.Close()
	}()

	return nl.Addr().String(), func() { ln.Close() }
}

// ── TestConnect_Loopback ──────────────────────────────────────────────────────

// TestConnect_Loopback proves the full connect→operation→disconnect flow
// against a real loopback SSH NETCONF server.
func TestConnect_Loopback(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})
	// Session ID 600 per P021 — new range for M004.
	addr, cleanup := startLoopbackSSHServer(t, caps, 600)
	defer cleanup()

	host, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	sess := &repl.Session{}
	var out, errOut bytes.Buffer

	// Build connect args: --host --port --user --password --insecure.
	args := []string{
		"--host", host,
		"--port", port,
		"--user", "admin",
		"--password", "secret",
		"--insecure",
	}

	// handleConnect is exported for testing via the repl_test package.
	// We use ExportedHandleConnect which is a test-only export wrapper.
	err = repl.ExportedHandleConnect(args, nil, sess, &out, &errOut)
	require.NoError(t, err, "handleConnect must not return a fatal error")

	assert.True(t, sess.Connected(), "session must be connected after handleConnect")
	assert.Contains(t, out.String(), "connected", "stdout must confirm connection")
	assert.NotEmpty(t, sess.Host(), "session host must be set")

	// Disconnect.
	var out2, errOut2 bytes.Buffer
	err = repl.ExportedHandleDisconnect(sess, &out2, &errOut2)
	require.NoError(t, err)
	assert.False(t, sess.Connected(), "session must be disconnected after handleDisconnect")
	assert.Contains(t, out2.String(), "disconnected")
}

// ── TestConnect_AlreadyConnected ──────────────────────────────────────────────

// TestConnect_AlreadyConnected verifies that calling connect when already
// connected prints a warning and does not establish a second session.
func TestConnect_AlreadyConnected(t *testing.T) {
	// Inject a fake non-nil client to simulate connected state.
	sess := repl.NewSessionWithClient(&client.Client{}, "fake-host")
	var out, errOut bytes.Buffer

	args := []string{"--host", "other-host", "--user", "u", "--password", "p", "--insecure"}
	err := repl.ExportedHandleConnect(args, nil, sess, &out, &errOut)
	require.NoError(t, err)

	assert.Contains(t, errOut.String(), "already connected",
		"must print 'already connected' when session exists")
	// The host must not change.
	assert.Equal(t, "fake-host", sess.Host(), "host must not be replaced when already connected")
}

// ── TestConnect_MissingHost ───────────────────────────────────────────────────

// TestConnect_MissingHost verifies that connect without --host prints an error.
func TestConnect_MissingHost(t *testing.T) {
	sess := &repl.Session{}
	var out, errOut bytes.Buffer

	err := repl.ExportedHandleConnect([]string{"--user", "admin", "--password", "pw", "--insecure"}, nil, sess, &out, &errOut)
	require.NoError(t, err, "missing host is non-fatal")

	assert.False(t, sess.Connected(), "must not connect when --host is missing")
	assert.Contains(t, errOut.String(), "--host", "error must mention --host")
}

// ── TestConnect_MissingUser ───────────────────────────────────────────────────

// TestConnect_MissingUser verifies that connect without --user prints an error.
func TestConnect_MissingUser(t *testing.T) {
	sess := &repl.Session{}
	var out, errOut bytes.Buffer

	err := repl.ExportedHandleConnect([]string{"--host", "localhost", "--password", "pw", "--insecure"}, nil, sess, &out, &errOut)
	require.NoError(t, err, "missing user is non-fatal")

	assert.False(t, sess.Connected(), "must not connect when --user is missing")
	assert.Contains(t, errOut.String(), "--user", "error must mention --user")
}

// ── TestDisconnect_NotConnected ───────────────────────────────────────────────

// TestDisconnect_NotConnected verifies disconnect when no session is active.
func TestDisconnect_NotConnected(t *testing.T) {
	sess := &repl.Session{}
	var out, errOut bytes.Buffer

	err := repl.ExportedHandleDisconnect(sess, &out, &errOut)
	require.NoError(t, err)
	assert.Contains(t, errOut.String(), "not connected")
}

// ── TestConnect_NoSpuriousWarning ─────────────────────────────────────────────

// TestConnect_NoSpuriousWarning verifies that connecting without --insecure does
// NOT print a host-key-verification warning. The warning is only shown when the
// user explicitly passes --insecure, to indicate they are opting out of security.
func TestConnect_NoSpuriousWarning(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})
	addr, cleanup := startLoopbackSSHServer(t, caps, 605)
	defer cleanup()

	host, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	sess := &repl.Session{}
	var out, errOut bytes.Buffer

	// Connect WITHOUT --insecure flag.
	args := []string{
		"--host", host,
		"--port", port,
		"--user", "admin",
		"--password", "secret",
		// no --insecure
	}
	err = repl.ExportedHandleConnect(args, nil, sess, &out, &errOut)
	require.NoError(t, err)

	// No warning should appear on stderr.
	assert.NotContains(t, errOut.String(), "warning",
		"connecting without --insecure must not print a warning; stderr was: %q", errOut.String())
	assert.True(t, sess.Connected(), "session must be connected")

	// Cleanup.
	var out2, errOut2 bytes.Buffer
	_ = repl.ExportedHandleDisconnect(sess, &out2, &errOut2)
}
