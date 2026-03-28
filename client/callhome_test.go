// callhome_test.go — end-to-end tests for RFC 8071 call home.
//
// Tests verify the complete call home stack: TCP connection (role-inverted) →
// SSH/TLS handshake → NETCONF hello → typed GetConfig operation.
//
// In normal operation the client dials the server. In call home the server
// dials the client. The SSH/TLS server/client roles are unchanged.
//
// Test inventory:
//   - TestCallHome_SSH — server dials via ssh.DialCallHome; client accepts
//     via AcceptCallHomeSSH; GetConfig succeeds (session-id 301).
//   - TestCallHome_TLS — server dials via tls.DialCallHome; client accepts
//     via AcceptCallHomeTLS; GetConfig succeeds (session-id 302).
package client_test

import (
	"context"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/xml"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	netconf "github.com/GabrielNunesIT/netconf"
	client "github.com/GabrielNunesIT/netconf/client"
	ncssh "github.com/GabrielNunesIT/netconf/transport/ssh"
	nctls "github.com/GabrielNunesIT/netconf/transport/tls"
)

// ─── Call Home SSH ────────────────────────────────────────────────────────────

// TestCallHome_SSH verifies the complete SSH call home stack:
//
//	NETCONF client listens → server dials via ssh.DialCallHome →
//	AcceptCallHomeSSH → NETCONF hello → GetConfig → DataReply
func TestCallHome_SSH(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	serverCfg, clientCfg := testSSHConfigs(t)

	// Client listens first — the port is bound before the server dials.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "client listen")
	defer ln.Close()

	addr := ln.Addr().String()

	// Server goroutine: dial out (call home), run SSH server, run echo server.
	type srvResult struct {
		sess *netconf.Session
		err  error
	}
	srvCh := make(chan srvResult, 1)
	go func() {
		srvTrp, err := ncssh.DialCallHome(addr, serverCfg)
		if err != nil {
			srvCh <- srvResult{err: fmt.Errorf("DialCallHome: %w", err)}
			return
		}
		sess, err := netconf.ServerSession(srvTrp, caps, 301) // P021: M003 uses 300+
		if err != nil {
			_ = srvTrp.Close()
			srvCh <- srvResult{err: fmt.Errorf("ServerSession: %w", err)}
			return
		}
		srvCh <- srvResult{sess: sess}
	}()

	// Client foreground: accept the call-home connection and negotiate hello.
	ctx := context.Background()
	cli, err := client.AcceptCallHomeSSH(ctx, ln, clientCfg, caps)
	require.NoError(t, err, "AcceptCallHomeSSH must succeed")
	defer cli.Close()

	// Collect server result.
	var sr srvResult
	select {
	case sr = <-srvCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server session")
	}
	require.NoError(t, sr.err, "server session setup must succeed")

	// Drive the echo server.
	go callHomeEchoServer(t, sr.sess)

	// Verify GetConfig works end-to-end.
	dr, err := cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
	require.NoError(t, err, "GetConfig over SSH call home must succeed")
	assert.NotNil(t, dr, "DataReply must be non-nil")
}

// ─── Call Home TLS ────────────────────────────────────────────────────────────

// TestCallHome_TLS verifies the complete TLS call home stack:
//
//	NETCONF client listens → server dials via tls.DialCallHome →
//	AcceptCallHomeTLS → NETCONF hello → GetConfig → DataReply
func TestCallHome_TLS(t *testing.T) {
	caps := netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

	serverCfg, clientCfg := testCallHomeTLSConfigs(t)

	// Client listens first.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "client listen")
	defer ln.Close()

	addr := ln.Addr().String()

	// Server goroutine: dial out (call home), run TLS server, run echo server.
	type srvResult struct {
		sess *netconf.Session
		err  error
	}
	srvCh := make(chan srvResult, 1)
	go func() {
		srvTrp, err := nctls.DialCallHome(addr, serverCfg)
		if err != nil {
			srvCh <- srvResult{err: fmt.Errorf("DialCallHome: %w", err)}
			return
		}
		sess, err := netconf.ServerSession(srvTrp, caps, 302) // P021: M003 uses 300+
		if err != nil {
			_ = srvTrp.Close()
			srvCh <- srvResult{err: fmt.Errorf("ServerSession: %w", err)}
			return
		}
		srvCh <- srvResult{sess: sess}
	}()

	// Client foreground: accept the call-home connection and negotiate hello.
	ctx := context.Background()
	cli, err := client.AcceptCallHomeTLS(ctx, ln, clientCfg, caps)
	require.NoError(t, err, "AcceptCallHomeTLS must succeed")
	defer cli.Close()

	// Collect server result.
	var sr srvResult
	select {
	case sr = <-srvCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server session")
	}
	require.NoError(t, sr.err, "server session setup must succeed")

	// Drive the echo server.
	go callHomeEchoServer(t, sr.sess)

	// Verify GetConfig works end-to-end.
	dr, err := cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
	require.NoError(t, err, "GetConfig over TLS call home must succeed")
	assert.NotNil(t, dr, "DataReply must be non-nil")
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// callHomeEchoServer drives a NETCONF server session: it reads incoming RPCs
// and replies with <data><config/></data> for get/get-config, or <ok/> for
// everything else. It exits when Recv fails (session closed).
func callHomeEchoServer(t *testing.T, sess *netconf.Session) {
	t.Helper()
	const ns = "urn:ietf:params:xml:ns:netconf:base:1.0"
	for {
		raw, err := sess.Recv()
		if err != nil {
			return
		}
		// Decode just enough to get message-id and the operation.
		var rpc struct {
			XMLName   xml.Name  `xml:"rpc"`
			MessageID string    `xml:"message-id,attr"`
			GetConfig *struct{} `xml:"get-config"`
			Get       *struct{} `xml:"get"`
		}
		_ = xml.Unmarshal(raw, &rpc)
		msgID := rpc.MessageID
		if msgID == "" {
			msgID = "0"
		}

		var body string
		if rpc.GetConfig != nil || rpc.Get != nil {
			body = `<data xmlns="` + ns + `"><config/></data>`
		} else {
			body = `<ok/>`
		}
		reply := fmt.Sprintf(
			`<rpc-reply xmlns=%q message-id=%q>%s</rpc-reply>`,
			ns, msgID, body,
		)
		if err := sess.Send([]byte(reply)); err != nil {
			return
		}
	}
}

// testCallHomeTLSConfigs returns a matched server+client *cryptotls.Config
// pair for TLS call home tests, with mutual auth enabled. Uses the existing
// generateClientTestCA / generateClientTestCert helpers from client_test.go.
func testCallHomeTLSConfigs(t *testing.T) (serverCfg, clientCfg *cryptotls.Config) {
	t.Helper()

	ca := generateClientTestCA(t)

	caPool := x509.NewCertPool()
	caPool.AddCert(ca.cert)

	sCertPEM, sKeyPEM := generateClientTestCert(t, ca, &x509.Certificate{
		SerialNumber: big.NewInt(200),
		Subject:      pkix.Name{CommonName: "callhome-server.test"},
		DNSNames:     []string{"localhost", "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	serverTLSCert, err := cryptotls.X509KeyPair(sCertPEM, sKeyPEM)
	require.NoError(t, err, "server TLS key pair")

	cCertPEM, cKeyPEM := generateClientTestCert(t, ca, &x509.Certificate{
		SerialNumber: big.NewInt(201),
		Subject:      pkix.Name{CommonName: "callhome-client.test"},
		DNSNames:     []string{"callhome-client.test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	clientTLSCert, err := cryptotls.X509KeyPair(cCertPEM, cKeyPEM)
	require.NoError(t, err, "client TLS key pair")

	serverCfg = &cryptotls.Config{
		Certificates: []cryptotls.Certificate{serverTLSCert},
		ClientAuth:   cryptotls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
	clientCfg = &cryptotls.Config{
		Certificates: []cryptotls.Certificate{clientTLSCert},
		RootCAs:      caPool,
		ServerName:   "localhost",
	}
	return
}
