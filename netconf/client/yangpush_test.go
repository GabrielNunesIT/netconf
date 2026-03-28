package client_test

import (
	"context"
	"encoding/xml"
	"sync"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/server"
	"github.com/GabrielNunesIT/netconf/netconf/subscriptions"
	"github.com/GabrielNunesIT/netconf/netconf/yangpush"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// yangPushCaps extends subCaps with the YANG-push capability URI.
var yangPushCaps = append(subCaps, yangpush.CapabilityURI)

// ─── TestClient_YangPush_PeriodicSubscription ─────────────────────────────────

// TestClient_YangPush_PeriodicSubscription proves the YANG-push periodic
// subscription flow:
//  1. Client establishes a subscription with a Period (centiseconds) value.
//  2. The server handler receives the RPC body containing the period parameter.
//  3. The server sends a push-update notification.
//  4. The client receives the push-update notification on its notification channel.
func TestClient_YangPush_PeriodicSubscription(t *testing.T) {
	// Session ID 400 — new range for S05.
	cli, srv, serverSess := newSubscriptionPair(t, 400)
	defer func() { _ = cli.Close() }()

	subscribedCh := make(chan struct{})

	// Conformance capture per P018: store RPC body to verify Period reaches server.
	var mu sync.Mutex
	var capturedBody []byte

	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = make([]byte, len(rpc.Body))
			copy(capturedBody, rpc.Body)
			mu.Unlock()

			close(subscribedCh)
			return establishReplyBody(t, 1), nil
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// Establish a periodic subscription with Period=1000 centiseconds (10s).
	id, notifCh, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{
		Stream: "NETCONF",
		Period: 1000,
	})
	require.NoError(t, err, "EstablishSubscription with Period must succeed")
	assert.Equal(t, subscriptions.SubscriptionID(1), id)

	// Wait for the handler to process the establish RPC.
	select {
	case <-subscribedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("establish-subscription handler did not signal within 2s")
	}

	// Conformance: verify the Period value was encoded in the RPC body.
	mu.Lock()
	body := capturedBody
	mu.Unlock()
	require.NotNil(t, body, "handler must have captured the RPC body")
	assert.Contains(t, string(body), `<period>1000</period>`,
		"establish-subscription RPC body must contain the Period field")
	assert.Contains(t, string(body), `establish-subscription`,
		"RPC body must contain establish-subscription element")

	// Build and send a push-update notification from the server.
	// Serve is blocked in Recv() at this point — no concurrent Send race.
	updatesBody := []byte(`<operational xmlns="urn:example:operational"><counter>42</counter></operational>`)
	pushUpdate := yangpush.PushUpdate{
		ID:              uint32(id),
		ObservationTime: "2026-01-01T00:00:00Z",
		Datastore:       "urn:ietf:params:netconf:datastore:operational",
		Updates:         updatesBody,
	}
	pushBytes, err := xml.Marshal(pushUpdate)
	require.NoError(t, err, "marshal PushUpdate must succeed")

	n := &netconf.Notification{
		EventTime: "2026-01-01T00:00:00Z",
		Body:      pushBytes,
	}
	require.NoError(t, server.SendNotification(serverSess, n), "SendNotification must succeed")

	// Client should receive the push-update notification.
	select {
	case received, open := <-notifCh:
		require.True(t, open, "notification channel must be open")
		assert.Equal(t, "2026-01-01T00:00:00Z", received.EventTime)
		assert.Contains(t, string(received.Body), "push-update",
			"notification body must contain push-update element")
		assert.Contains(t, string(received.Body), "counter",
			"notification body must contain the pushed data")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for push-update notification")
	}

	require.NoError(t, cli.CloseSession(ctx))
	select {
	case serveErr := <-serveDone:
		assert.NoError(t, serveErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession")
	}
}

// TestClient_YangPush_OnChangeSubscription proves that a subscription request
// with no Period (on-change style) establishes correctly and that the
// subscribe RPC body does not contain a period element.
func TestClient_YangPush_OnChangeSubscription(t *testing.T) {
	cli, srv, serverSess := newSubscriptionPair(t, 401)
	defer func() { _ = cli.Close() }()

	subscribedCh := make(chan struct{})

	var mu sync.Mutex
	var capturedBody []byte

	srv.RegisterHandler("establish-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			mu.Lock()
			capturedBody = make([]byte, len(rpc.Body))
			copy(capturedBody, rpc.Body)
			mu.Unlock()
			close(subscribedCh)
			return establishReplyBody(t, 2), nil
		},
	))

	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// EstablishSubscription without Period — no periodic trigger.
	id, _, err := cli.EstablishSubscription(ctx, subscriptions.EstablishSubscriptionRequest{
		Stream: "NETCONF",
		// Period is zero — must not appear in the RPC body.
	})
	require.NoError(t, err)
	assert.Equal(t, subscriptions.SubscriptionID(2), id)

	select {
	case <-subscribedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not signal within 2s")
	}

	mu.Lock()
	body := capturedBody
	mu.Unlock()

	// Verify that no period element was emitted (omitempty on Period=0).
	assert.NotContains(t, string(body), `<period>`,
		"establish-subscription without Period must not emit <period> element")

	require.NoError(t, cli.CloseSession(ctx))
	<-serveDone
}
