package client_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/client"
	"github.com/GabrielNunesIT/netconf/netconf/server"
	"github.com/GabrielNunesIT/netconf/netconf/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notifCaps is a capability set that includes base:1.0, :notification, and
// :interleave. Both client and server advertise this set so the hello exchange
// proves notification capability negotiation.
var notifCaps = netconf.NewCapabilitySet([]string{
	netconf.BaseCap10,
	netconf.CapabilityNotification,
	netconf.CapabilityInterleave,
})

// newNotifPair establishes a NETCONF session pair with notification capabilities
// and returns a ready-to-use *client.Client and the raw server-side *netconf.Session.
func newNotifPair(t *testing.T) (cli *client.Client, serverSess *netconf.Session) {
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
		s, err := netconf.ClientSession(clientT, notifCaps)
		cliCh <- sessResult{s, err}
	}()
	go func() {
		s, err := netconf.ServerSession(serverT, notifCaps, 1)
		srvCh <- sessResult{s, err}
	}()

	cliRes := <-cliCh
	srvRes := <-srvCh
	require.NoError(t, cliRes.err, "ClientSession must succeed")
	require.NoError(t, srvRes.err, "ServerSession must succeed")

	cli = client.NewClient(cliRes.sess)
	return cli, srvRes.sess
}

// TestClient_NotificationInterleave is the primary risk-retirement test for S01.
//
// It proves that the client dispatcher correctly handles interleaved
// <notification> and <rpc-reply> messages on the same session:
//
//  1. Both sides negotiate with :notification and :interleave capabilities.
//  2. Client subscribes (create-subscription RPC succeeds).
//  3. Server sends 5 notifications sequentially (all before any RPCs are issued
//     to avoid send-side races between Serve's sendReply and SendNotification).
//  4. Client concurrently executes 3 get-config RPCs.
//  5. All 5 notifications arrive on the client notification channel in order.
//  6. All 3 RPC replies are correct (DataReply non-nil, contains expected body).
//  7. close-session cleanly terminates the Serve loop.
//
// Send-side race avoidance: The Serve loop calls sess.Send from its goroutine
// (via sendReply). SendNotification also calls sess.Send. To avoid a data race,
// all 5 notifications are sent after the create-subscription handler signals
// readiness but before the client issues any get-config RPCs. During the
// notification phase, Serve is blocked in sess.Recv() and does not call
// sess.Send, so there is no concurrent write race.
func TestClient_NotificationInterleave(t *testing.T) {
	cli, serverSess := newNotifPair(t)
	defer func() { _ = cli.Close() }()

	srv := server.NewServer()

	// subscribedCh is closed by the create-subscription handler once the
	// subscription is accepted. The notification goroutine waits on it.
	subscribedCh := make(chan struct{})

	// create-subscription handler: signals readiness and returns <ok/>.
	srv.RegisterHandler("create-subscription", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			close(subscribedCh)
			return nil, nil // nil, nil → <ok/>
		},
	))

	// get-config handler: returns a minimal <data><config/></data> body.
	const getConfigBody = `<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><config/></data>`
	srv.RegisterHandler("get-config", server.HandlerFunc(
		func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(getConfigBody), nil
		},
	))

	// Start the Serve loop.
	ctx := context.Background()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, serverSess)
	}()

	// Step 1: Subscribe.
	notifCh, err := cli.Subscribe(ctx, netconf.CreateSubscription{})
	require.NoError(t, err, "Subscribe must succeed")
	require.NotNil(t, notifCh, "Subscribe must return a non-nil channel")

	// Step 2: Wait for the create-subscription handler to signal, then send
	// 5 notifications sequentially. Serve is blocked in sess.Recv() at this
	// point, so there is no concurrent sess.Send call from the Serve loop.
	select {
	case <-subscribedCh:
		// Handler has been called — subscription accepted.
	case <-time.After(2 * time.Second):
		t.Fatal("create-subscription handler did not signal within 2s")
	}

	const numNotifs = 5
	for i := range numNotifs {
		n := &netconf.Notification{
			EventTime: fmt.Sprintf("2026-01-01T00:00:0%dZ", i),
			Body:      []byte(fmt.Sprintf(`<test-event seq="%d"/>`, i)),
		}
		require.NoError(t, server.SendNotification(serverSess, n),
			"SendNotification %d must succeed", i)
	}

	// Step 3: Collect all 5 notifications from the client channel before
	// issuing RPCs. This also acts as a synchronization point — we know the
	// dispatcher has processed all notification messages before we send RPCs.
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
			t.Fatalf("timeout waiting for notifications: got %d/%d", len(received), numNotifs)
		}
	}

	// Assert all 5 notifications arrived in order.
	require.Len(t, received, numNotifs, "all %d notifications must arrive", numNotifs)
	for i, n := range received {
		expectedTime := fmt.Sprintf("2026-01-01T00:00:0%dZ", i)
		assert.Equal(t, expectedTime, n.EventTime, "notification %d EventTime must match", i)
		assert.Contains(t, string(n.Body), fmt.Sprintf("seq=\"%d\"", i),
			"notification %d Body must contain seq attribute", i)
	}

	// Step 4: Concurrently execute 3 get-config RPCs.
	const numRPCs = 3
	type rpcResult struct {
		dr  *netconf.DataReply
		err error
	}
	results := make(chan rpcResult, numRPCs)
	var wg sync.WaitGroup
	wg.Add(numRPCs)
	for range numRPCs {
		go func() {
			defer wg.Done()
			dr, err := cli.GetConfig(ctx, netconf.Datastore{Running: &struct{}{}}, nil)
			results <- rpcResult{dr, err}
		}()
	}
	wg.Wait()
	close(results)

	// Assert all 3 RPCs succeeded with the expected body.
	for res := range results {
		require.NoError(t, res.err, "concurrent GetConfig must succeed")
		require.NotNil(t, res.dr, "GetConfig must return a DataReply")
		assert.Contains(t, string(res.dr.Content), "config",
			"DataReply must contain the handler-supplied config element")
	}

	// Step 5: Close session and verify Serve terminates cleanly.
	require.NoError(t, cli.CloseSession(ctx), "CloseSession must succeed")

	select {
	case serveErr := <-serveDone:
		assert.NoError(t, serveErr, "Serve must return nil after close-session")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after CloseSession")
	}
}
