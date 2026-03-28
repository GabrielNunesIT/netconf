package client_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf"
	"github.com/GabrielNunesIT/netconf/client"
	"github.com/GabrielNunesIT/netconf/server"
	"github.com/GabrielNunesIT/netconf/subscriptions"
	"github.com/GabrielNunesIT/netconf/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subCaps includes base:1.0, notification, interleave, and both RFC 8639/8640
// YANG module namespace URIs so both sides announce subscriptions capability.
var subCaps = netconf.NewCapabilitySet([]string{
	netconf.BaseCap10,
	netconf.CapabilityNotification,
	netconf.CapabilityInterleave,
	subscriptions.CapabilityURI,
	subscriptions.CapabilityURINetconf,
})

// newSubscriptionPair establishes a NETCONF session pair with subscriptions
// capabilities. Returns a ready-to-use *client.Client, a *server.Server with
// no handlers registered yet, and the raw server-side *netconf.Session.
func newSubscriptionPair(t *testing.T, sessionID uint32) (cli *client.Client, srv *server.Server, serverSess *netconf.Session) {
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
		s, err := netconf.ClientSession(clientT, subCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, subCaps, sessionID)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	srv = server.NewServer()
	return client.NewClient(cliRes.sess), srv, srvRes.sess
}

// establishReplyBody marshals an EstablishSubscriptionReply body for use in
// server handlers. Returns the raw inner XML bytes expected by the handler
// return convention (nil wrapping → the bytes go inside <rpc-reply>).
func establishReplyBody(t *testing.T, id subscriptions.SubscriptionID) []byte {
	t.Helper()
	b, err := xml.Marshal(subscriptions.EstablishSubscriptionReply{ID: id})
	require.NoError(t, err, "marshal EstablishSubscriptionReply")
	return b
}

// sendSubscriptionNotif marshals a SubscriptionStarted body, wraps it in a
// Notification envelope, and sends it to the client via SendNotification.
func sendSubscriptionNotif(t *testing.T, sess *netconf.Session, id subscriptions.SubscriptionID, stream string) {
	t.Helper()
	body, err := xml.Marshal(subscriptions.SubscriptionStarted{ID: id, Stream: stream})
	require.NoError(t, err, "marshal SubscriptionStarted")
	n := &netconf.Notification{
		EventTime: "2026-01-01T00:00:00Z",
		Body:      body,
	}
	require.NoError(t, server.SendNotification(sess, n), "SendNotification must succeed")
}

// ─── TestClient_EstablishAndDeleteSubscription ────────────────────────────────

// TestClient_EstablishAndDeleteSubscription is the primary lifecycle test for S03.
// It proves:
//  1. EstablishSubscription sends the RPC, the server returns a reply with ID=1.
//  2. A subscription-started notification arrives on the client notification channel.
//  3. DeleteSubscription sends the delete-subscription RPC and it succeeds.
func TestClient_EstablishAndDeleteSubscription(t *testing.T) {
	// Session ID 200 per P021.
	cli, srv, serverSess := newSubscriptionPair(t, 200)
	defer func() { _ = cli.Close() }()

	// subscribedCh is closed after the establish-subscription handler accepts.
	subscribedCh := make(chan struct{})

	// establish-subscription handler: returns an EstablishSubscriptionReply with id=1.
	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			close(subscribedCh)
			return establishReplyBody(t, 1), nil
		},
	))

	// delete-subscription handler: returns <ok/>.
	srv.RegisterHandler("delete-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil // nil → <ok/>
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// Step 1: Establish subscription.
	id, notifCh, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{
		Stream: "NETCONF",
	})
	require.NoError(t, err, "EstablishSubscription must succeed")
	assert.Equal(t, subscriptions.SubscriptionID(1), id, "server must return id=1")
	require.NotNil(t, notifCh, "EstablishSubscription must return the notification channel")

	// Step 2: Wait for handler to close subscribedCh, then send a notification.
	// Serve is blocked in Recv() at this point — no concurrent Send race.
	select {
	case <-subscribedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("establish-subscription handler did not signal within 2s")
	}

	sendSubscriptionNotif(t, serverSess, 1, "NETCONF")

	// Step 3: Read the notification from the client channel.
	select {
	case n, open := <-notifCh:
		require.True(t, open, "notification channel must be open")
		assert.Equal(t, "2026-01-01T00:00:00Z", n.EventTime)
		assert.Contains(t, string(n.Body), "subscription-started",
			"notification body must contain subscription-started element")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscription-started notification")
	}

	// Step 4: Delete subscription.
	require.NoError(t, cli.DeleteSubscription(ctx, id), "DeleteSubscription must succeed")

	// Tear down.
	require.NoError(t, cli.CloseSession(ctx))
	select {
	case serveErr := <-serveDone:
		assert.NoError(t, serveErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession")
	}
}

// ─── TestClient_ModifySubscription ───────────────────────────────────────────

// TestClient_ModifySubscription proves that ModifySubscription sends the RPC
// and the server reply (ok) results in no error.
func TestClient_ModifySubscription(t *testing.T) {
	// Session ID 201 per P021.
	cli, srv, serverSess := newSubscriptionPair(t, 201)
	defer func() { _ = cli.Close() }()

	subscribedCh := make(chan struct{})

	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			close(subscribedCh)
			return establishReplyBody(t, 5), nil
		},
	))
	srv.RegisterHandler("modify-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil // <ok/>
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	id, _, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{})
	require.NoError(t, err)
	assert.Equal(t, subscriptions.SubscriptionID(5), id)

	// ModifySubscription: change the filter.
	modErr := cli.ModifySubscription(ctx, subscriptions.ModifySubscriptionRequest{
		ID:     id,
		Filter: &subscriptions.FilterSpec{XPathFilter: "/interfaces"},
	})
	require.NoError(t, modErr, "ModifySubscription must succeed")

	require.NoError(t, cli.CloseSession(ctx))
	<-serveDone
}

// ─── TestClient_KillSubscription ─────────────────────────────────────────────

// TestClient_KillSubscription proves that KillSubscription sends the kill RPC
// with a reason and the server reply (ok) results in no error.
func TestClient_KillSubscription(t *testing.T) {
	// Session ID 202 per P021.
	cli, srv, serverSess := newSubscriptionPair(t, 202)
	defer func() { _ = cli.Close() }()

	subscribedCh := make(chan struct{})

	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			close(subscribedCh)
			return establishReplyBody(t, 10), nil
		},
	))

	// Capture the kill RPC body for conformance verification per P018.
	var killedID subscriptions.SubscriptionID
	var killedReason string
	srv.RegisterHandler("kill-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			var ks subscriptions.KillSubscription
			// Wrap in synthetic root per P012 to handle potential siblings;
			// for kill-subscription the body is a single root so direct unmarshal works.
			_ = xml.Unmarshal(rpc.Body, &ks)
			killedID = ks.ID
			killedReason = ks.Reason
			return nil, nil // <ok/>
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	id, _, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{})
	require.NoError(t, err)
	assert.Equal(t, subscriptions.SubscriptionID(10), id)

	killErr := cli.KillSubscription(ctx, id, "admin request")
	require.NoError(t, killErr, "KillSubscription must succeed")

	// Conformance: verify the ID and reason reached the server.
	assert.Equal(t, id, killedID, "server must receive the correct subscription ID")
	assert.Equal(t, "admin request", killedReason, "server must receive the kill reason")

	require.NoError(t, cli.CloseSession(ctx))
	<-serveDone
}

// ─── TestClient_MultipleSubscriptions ────────────────────────────────────────

// TestClient_MultipleSubscriptions proves that a single NETCONF session supports
// multiple concurrent subscriptions and that notifications for both subscriptions
// arrive on the shared notification channel.
func TestClient_MultipleSubscriptions(t *testing.T) {
	// Session ID 203 per P021.
	cli, srv, serverSess := newSubscriptionPair(t, 203)
	defer func() { _ = cli.Close() }()

	// Track call count to assign distinct IDs on each establish call.
	var callCount atomic.Uint32

	// Gate channel: closed when the second establish has been handled.
	bothEstablished := make(chan struct{})

	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			n := callCount.Add(1)
			if n == 2 {
				close(bothEstablished)
			}
			return establishReplyBody(t, n), nil
		},
	))
	srv.RegisterHandler("delete-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// Establish two subscriptions sequentially.
	id1, notifCh, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{
		Stream: "NETCONF",
	})
	require.NoError(t, err, "first EstablishSubscription must succeed")

	id2, _, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{
		Stream: "NETCONF",
	})
	require.NoError(t, err, "second EstablishSubscription must succeed")

	assert.NotEqual(t, id1, id2, "two subscriptions must have distinct IDs")

	// Wait for both establishes to be processed by the server handler.
	select {
	case <-bothEstablished:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not process both establish-subscription RPCs within 2s")
	}

	// Send two notifications — one per subscription ID.
	// Serve is blocked in Recv() at this point, so no concurrent Send race.
	for i, id := range []subscriptions.SubscriptionID{id1, id2} {
		body, err := xml.Marshal(subscriptions.SubscriptionStarted{
			ID:     id,
			Stream: "NETCONF",
		})
		require.NoError(t, err)
		n := &netconf.Notification{
			EventTime: fmt.Sprintf("2026-01-01T00:00:0%dZ", i),
			Body:      body,
		}
		require.NoError(t, server.SendNotification(serverSess, n),
			"SendNotification for sub %d must succeed", id)
	}

	// Collect both notifications from the shared channel.
	received := make([]*netconf.Notification, 0, 2)
	timeout := time.After(3 * time.Second)
	for len(received) < 2 {
		select {
		case n, open := <-notifCh:
			require.True(t, open, "notification channel must not be closed")
			received = append(received, n)
		case <-timeout:
			t.Fatalf("timeout waiting for notifications: got %d/2", len(received))
		}
	}

	assert.Len(t, received, 2, "both notifications must arrive on the shared channel")

	// Both notifications must contain subscription-started body elements.
	for i, n := range received {
		assert.Contains(t, string(n.Body), "subscription-started",
			"notification %d must contain subscription-started", i)
	}

	// Delete both subscriptions.
	require.NoError(t, cli.DeleteSubscription(ctx, id1))
	require.NoError(t, cli.DeleteSubscription(ctx, id2))

	require.NoError(t, cli.CloseSession(ctx))
	select {
	case serveErr := <-serveDone:
		assert.NoError(t, serveErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession")
	}
}

// ─── TestClient_EstablishSubscription_ReplyBodyParsing ────────────────────────

// TestClient_EstablishSubscription_ReplyBodyParsing proves that the client
// correctly decodes the EstablishSubscriptionReply body to extract the subscription
// ID, including when the ID is a non-trivial value.
func TestClient_EstablishSubscription_ReplyBodyParsing(t *testing.T) {
	cli, srv, serverSess := newSubscriptionPair(t, 204)
	defer func() { _ = cli.Close() }()

	const expectedID = subscriptions.SubscriptionID(99999)

	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return establishReplyBody(t, expectedID), nil
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	id, _, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{})
	require.NoError(t, err)
	assert.Equal(t, expectedID, id, "client must decode and return the correct subscription ID")

	require.NoError(t, cli.CloseSession(ctx))
	<-serveDone
}
