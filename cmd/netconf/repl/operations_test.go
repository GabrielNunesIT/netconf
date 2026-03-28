package repl

import (
	"bytes"
	"context"
	"testing"

	netconf "github.com/GabrielNunesIT/netconf/netconf"
	"github.com/GabrielNunesIT/netconf/netconf/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseCaps is a minimal capability set for operation tests.
var baseCaps = netconf.NewCapabilitySet([]string{netconf.BaseCap10, netconf.BaseCap11})

// dataBody is the data reply body returned by mock get/get-config handlers.
const testDataBody = `<data xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><config/></data>`

// ── Unit tests (no network) ───────────────────────────────────────────────────

// TestCmdGet_NotConnected verifies the not-connected guard.
func TestCmdGet_NotConnected(t *testing.T) {
	sess := &Session{}
	var out, errOut bytes.Buffer
	err := dispatchOperation("get", nil, sess, &out, &errOut)
	assert.NoError(t, err)
	assert.Contains(t, errOut.String(), "not connected")
}

// TestCmdGetConfig_DefaultSource verifies that parseDatastore("running") returns
// a Running-set Datastore.
func TestCmdGetConfig_DefaultSource(t *testing.T) {
	ds := parseDatastore("running")
	assert.NotNil(t, ds.Running, "running datastore must have Running field set")
	assert.Nil(t, ds.Candidate)
	assert.Nil(t, ds.Startup)

	ds2 := parseDatastore("candidate")
	assert.NotNil(t, ds2.Candidate)
	assert.Nil(t, ds2.Running)

	ds3 := parseDatastore("startup")
	assert.NotNil(t, ds3.Startup)
	assert.Nil(t, ds3.Running)
}

// TestCmdEditConfig_MissingConfig verifies that edit-config without --config
// prints an error.
func TestCmdEditConfig_MissingConfig(t *testing.T) {
	// Use a connected-looking session (actual client is nil but the not-connected
	// guard is bypassed by using dispatchOperation with an explicitly connected mock).
	// Test flag validation only — use a fake connected session by injecting a
	// non-nil but unusable client.
	// We can't easily inject a mock client, so test the flag parsing directly.
	var errOut bytes.Buffer
	err := cmdEditConfig([]string{"--target", "running"}, &Session{}, &bytes.Buffer{}, &errOut)
	assert.NoError(t, err, "flag error must be non-fatal")
	assert.Contains(t, errOut.String(), "--config")
}

// TestCmdLock_DefaultTarget verifies parseDatastore defaults.
func TestCmdLock_DefaultTarget(t *testing.T) {
	ds := parseDatastore("")
	assert.NotNil(t, ds.Running, "empty string defaults to running")
}

// TestCmdKillSession_MissingID verifies that kill-session without --id prints error.
func TestCmdKillSession_MissingID(t *testing.T) {
	sess := &Session{}
	var errOut bytes.Buffer
	err := cmdKillSession([]string{}, sess, &bytes.Buffer{}, &errOut)
	assert.NoError(t, err)
	assert.Contains(t, errOut.String(), "--id")
}

// ── Integration tests (loopback SSH) ─────────────────────────────────────────

// TestCmdGet_Loopback verifies get calls the server and prints the data reply.
func TestCmdGet_Loopback(t *testing.T) {
	// Session ID 601 per P021 M004 range.
	addr := startTestSSHServer(t, baseCaps, 601, map[string]server.Handler{
		"get": server.HandlerFunc(func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return []byte(testDataBody), nil
		}),
	})

	sess := connectTestSession(t, addr)

	var out, errOut bytes.Buffer
	err := dispatchOperation("get", nil, sess, &out, &errOut)
	require.NoError(t, err)
	assert.Empty(t, errOut.String(), "no errors expected")
	assert.Contains(t, out.String(), "config", "output must contain data body content")
}

// TestCmdGetConfig_Loopback verifies get-config with --source running.
func TestCmdGetConfig_Loopback(t *testing.T) {
	addr := startTestSSHServer(t, baseCaps, 602, map[string]server.Handler{
		"get-config": server.HandlerFunc(func(_ context.Context, _ *netconf.Session, rpc *netconf.RPC) ([]byte, error) {
			return []byte(testDataBody), nil
		}),
	})

	sess := connectTestSession(t, addr)

	var out, errOut bytes.Buffer
	err := dispatchOperation("get-config", []string{"--source", "running"}, sess, &out, &errOut)
	require.NoError(t, err)
	assert.Empty(t, errOut.String())
	assert.Contains(t, out.String(), "config")
}

// TestCmdLockUnlock_Loopback verifies lock and unlock round-trip.
func TestCmdLockUnlock_Loopback(t *testing.T) {
	addr := startTestSSHServer(t, baseCaps, 603, map[string]server.Handler{
		"lock": server.HandlerFunc(func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil // <ok/>
		}),
		"unlock": server.HandlerFunc(func(_ context.Context, _ *netconf.Session, _ *netconf.RPC) ([]byte, error) {
			return nil, nil
		}),
	})

	sess := connectTestSession(t, addr)

	var out, errOut bytes.Buffer

	err := dispatchOperation("lock", []string{"--target", "running"}, sess, &out, &errOut)
	require.NoError(t, err)
	assert.Empty(t, errOut.String())
	assert.Contains(t, out.String(), "locked")

	out.Reset()
	errOut.Reset()

	err = dispatchOperation("unlock", []string{"--target", "running"}, sess, &out, &errOut)
	require.NoError(t, err)
	assert.Empty(t, errOut.String())
	assert.Contains(t, out.String(), "unlocked")
}

// TestCmdCapabilities_Loopback verifies capabilities prints the remote cap list.
func TestCmdCapabilities_Loopback(t *testing.T) {
	addr := startTestSSHServer(t, baseCaps, 604, map[string]server.Handler{})

	sess := connectTestSession(t, addr)

	var out, errOut bytes.Buffer
	err := dispatchOperation("capabilities", nil, sess, &out, &errOut)
	require.NoError(t, err)
	assert.Empty(t, errOut.String())
	// Server advertises baseCaps — both base URIs must appear.
	assert.Contains(t, out.String(), netconf.BaseCap10,
		"capabilities output must contain base:1.0")
}
