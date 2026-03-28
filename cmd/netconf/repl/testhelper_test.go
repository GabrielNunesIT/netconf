// testhelper_test.go provides shared test infrastructure for the repl package tests.
// It is compiled only during tests (package repl, not repl_test, so it can
// access unexported symbols).
package repl

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/server"
	ncssh "github.com/GabrielNunesIT/netconf/transport/ssh"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

// startTestSSHServer starts a minimal NETCONF-over-SSH server on a random
// loopback port with the given handlers. It accepts any password and serves
// a single connection.
//
// Returns the listening address and a cleanup function.
// The server goroutine exits when either the connection closes or the
// 10-second deadline fires.
func startTestSSHServer(t *testing.T, caps netconf.CapabilitySet, sessionID uint32, handlers map[string]server.Handler) string {
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
	t.Cleanup(func() { ln.Close() })

	go func() {
		trp, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		sess, sessErr := netconf.ServerSession(trp, caps, sessionID)
		if sessErr != nil {
			_ = trp.Close()
			return
		}

		srv := server.NewServer()
		for name, h := range handlers {
			srv.RegisterHandler(name, h)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Serve(ctx, sess)
		_ = trp.Close()
	}()

	return nl.Addr().String()
}

// connectTestSession establishes a NETCONF session to addr using password auth
// and returns a connected Session. The caller is responsible for cleanup.
func connectTestSession(t *testing.T, addr string) *Session {
	t.Helper()

	host, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	sess := &Session{}
	args := []string{
		"--host", host, "--port", port,
		"--user", "test", "--password", "test", "--insecure",
	}

	require.NoError(t, handleConnect(args, nil, sess, &nopWriter{}, &nopWriter{}))
	require.True(t, sess.Connected(), "session must be connected after handleConnect")
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// nopWriter is an io.Writer that discards all output.
type nopWriter struct{}

func (*nopWriter) Write(p []byte) (int, error) { return len(p), nil }
