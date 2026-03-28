package netconf_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Shared helpers ────────────────────────────────────────────────────────────

// mustMarshal marshals v to XML and fails the test on error.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	data, err := xml.Marshal(v)
	require.NoError(t, err, "xml.Marshal must not fail")
	return string(data)
}

// assertNSPresent checks that the given XML string carries the NETCONF base namespace.
func assertNSPresent(t *testing.T, xmlStr, element string) {
	t.Helper()
	assert.Contains(t, xmlStr, `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`,
		"%s must carry NETCONF base namespace", element)
}

// ── Datastore encoding tests ──────────────────────────────────────────────────

// TestDatastore_RunningEncoding verifies that Datastore{Running: &struct{}{}}
// marshals the running datastore as a child element, not an attribute.
func TestDatastore_RunningEncoding(t *testing.T) {
	t.Parallel()
	// Wrap in a parent element so xml.Marshal is happy with the struct tag context.
	type wrapper struct {
		XMLName xml.Name          `xml:"source"`
		DS      netconf.Datastore `xml:",omitempty"`
	}
	w := wrapper{DS: netconf.Datastore{Running: &struct{}{}}}
	data, err := xml.Marshal(w)
	require.NoError(t, err)
	xmlStr := string(data)

	// Must encode as child element, not attribute.
	assert.Contains(t, xmlStr, "<running>", "running must be a child element")
	assert.NotContains(t, xmlStr, `running="`, "running must not be an attribute")
	assert.NotContains(t, xmlStr, "<candidate>", "candidate must be absent")
	assert.NotContains(t, xmlStr, "<startup>", "startup must be absent")
}

// TestDatastore_CandidateEncoding mirrors TestDatastore_RunningEncoding for <candidate/>.
func TestDatastore_CandidateEncoding(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		XMLName xml.Name          `xml:"target"`
		DS      netconf.Datastore `xml:",omitempty"`
	}
	w := wrapper{DS: netconf.Datastore{Candidate: &struct{}{}}}
	data, err := xml.Marshal(w)
	require.NoError(t, err)
	xmlStr := string(data)

	assert.Contains(t, xmlStr, "<candidate>", "candidate must be a child element")
	assert.NotContains(t, xmlStr, "<running>", "running must be absent")
	assert.NotContains(t, xmlStr, "<startup>", "startup must be absent")
}

// TestDatastore_StartupEncoding mirrors for <startup/>.
func TestDatastore_StartupEncoding(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		XMLName xml.Name          `xml:"source"`
		DS      netconf.Datastore `xml:",omitempty"`
	}
	w := wrapper{DS: netconf.Datastore{Startup: &struct{}{}}}
	data, err := xml.Marshal(w)
	require.NoError(t, err)
	xmlStr := string(data)

	assert.Contains(t, xmlStr, "<startup>", "startup must be a child element")
	assert.NotContains(t, xmlStr, "<running>", "running must be absent")
}

// TestDatastore_URLEncoding verifies that a URL datastore encodes as <url>.
func TestDatastore_URLEncoding(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		XMLName xml.Name          `xml:"source"`
		DS      netconf.Datastore `xml:",omitempty"`
	}
	w := wrapper{DS: netconf.Datastore{URL: "https://example.com/cfg.xml"}}
	data, err := xml.Marshal(w)
	require.NoError(t, err)
	xmlStr := string(data)

	assert.Contains(t, xmlStr, "<url>https://example.com/cfg.xml</url>",
		"URL must encode as <url> child element")
	assert.NotContains(t, xmlStr, "<running>")
	assert.NotContains(t, xmlStr, "<candidate>")
}

// ── Filter type tests ─────────────────────────────────────────────────────────

// TestFilter_SubtreeRoundTrip verifies a subtree filter with arbitrary XML content
// round-trips through marshal/unmarshal with content preserved.
func TestFilter_SubtreeRoundTrip(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		XMLName xml.Name       `xml:"get"`
		F       netconf.Filter `xml:"filter"`
	}
	subtreeContent := []byte(`<interfaces><interface><name>eth0</name></interface></interfaces>`)
	orig := wrapper{F: netconf.Filter{Type: "subtree", Content: subtreeContent}}

	data, err := xml.Marshal(orig)
	require.NoError(t, err)
	xmlStr := string(data)

	// Type must appear as attribute.
	assert.Contains(t, xmlStr, `type="subtree"`, "subtree type must be an attribute")
	assert.Contains(t, xmlStr, "interfaces", "subtree content must be preserved")

	var got wrapper
	require.NoError(t, xml.Unmarshal(data, &got))
	assert.Equal(t, "subtree", got.F.Type, "Type must round-trip")
	assert.Contains(t, string(got.F.Content), "interfaces", "Content must round-trip")
}

// TestFilter_XPathRoundTrip verifies an XPath filter with a select expression
// round-trips and encodes the select attribute correctly.
func TestFilter_XPathRoundTrip(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		XMLName xml.Name       `xml:"get"`
		F       netconf.Filter `xml:"filter"`
	}
	orig := wrapper{F: netconf.Filter{
		Type:   "xpath",
		Select: "/interfaces/interface[name='eth0']",
	}}

	data, err := xml.Marshal(orig)
	require.NoError(t, err)
	xmlStr := string(data)

	assert.Contains(t, xmlStr, `type="xpath"`, "xpath type must be an attribute")
	assert.Contains(t, xmlStr, `select=`, "select expression must be an attribute")
	assert.Contains(t, xmlStr, "eth0", "select value must be present")

	var got wrapper
	require.NoError(t, xml.Unmarshal(data, &got))
	assert.Equal(t, "xpath", got.F.Type, "Type must round-trip")
	assert.Equal(t, orig.F.Select, got.F.Select, "Select must round-trip")
}

// TestFilter_SubtreeOmitsSelectAttr verifies that the select attribute is absent
// from a subtree filter output.
func TestFilter_SubtreeOmitsSelectAttr(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		XMLName xml.Name       `xml:"get"`
		F       netconf.Filter `xml:"filter"`
	}
	w := wrapper{F: netconf.Filter{Type: "subtree", Content: []byte(`<foo/>`)}}
	xmlStr := mustMarshal(t, w)
	assert.NotContains(t, xmlStr, `select=`, "subtree filter must not have select attr")
}

// ── DataReply decode test ─────────────────────────────────────────────────────

// TestDataReply_DecodeFromBody simulates decoding the <data> element from
// RPCReply.Body, which is the normal path after a get or get-config response.
func TestDataReply_DecodeFromBody(t *testing.T) {
	t.Parallel()
	// Construct a realistic RPCReply.Body containing a <data> element.
	body := []byte(`<data><interfaces><interface><name>lo</name></interface></interfaces></data>`)

	var dr netconf.DataReply
	require.NoError(t, xml.Unmarshal(body, &dr), "DataReply must decode from <data> bytes")

	assert.Contains(t, string(dr.Content), "lo",
		"DataReply.Content must hold the inner XML of <data>")
	assert.Contains(t, string(dr.Content), "interfaces",
		"DataReply.Content must preserve element structure")
}

// TestDataReply_EmptyData verifies that an empty <data/> element is handled
// without error and Content is empty/nil.
func TestDataReply_EmptyData(t *testing.T) {
	t.Parallel()
	var dr netconf.DataReply
	require.NoError(t, xml.Unmarshal([]byte(`<data/>`), &dr))
	assert.Empty(t, dr.Content, "empty <data/> must yield empty Content")
}

// ── Operation round-trip tests ────────────────────────────────────────────────

// TestGet_MarshalRoundTrip verifies Get with a subtree filter round-trips.
func TestGet_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.Get{
		Filter: &netconf.Filter{
			Type:    "subtree",
			Content: []byte(`<interfaces/>`),
		},
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "get")
	assert.Contains(t, xmlStr, "<get ", "must be a <get> element")
	assert.Contains(t, xmlStr, "interfaces", "filter content must be present")

	var got netconf.Get
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	require.NotNil(t, got.Filter, "Filter must survive round-trip")
	assert.Equal(t, "subtree", got.Filter.Type)
}

// TestGet_NoFilter verifies Get with no filter omits the <filter> element.
func TestGet_NoFilter(t *testing.T) {
	t.Parallel()
	orig := netconf.Get{}
	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "get")
	assert.NotContains(t, xmlStr, "<filter", "omitted filter must not appear in XML")
}

// TestGetConfig_MarshalRoundTrip verifies GetConfig with running source
// and a subtree filter round-trips correctly.
func TestGetConfig_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.GetConfig{
		Source: netconf.Datastore{Running: &struct{}{}},
		Filter: &netconf.Filter{
			Type:   "xpath",
			Select: "/config/system",
		},
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "get-config")
	assert.Contains(t, xmlStr, "<get-config ", "must be <get-config>")
	assert.Contains(t, xmlStr, "<running>", "running datastore must be present")
	assert.Contains(t, xmlStr, `select=`, "XPath select must be present")

	var got netconf.GetConfig
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Source.Running, "Source.Running must survive round-trip")
	require.NotNil(t, got.Filter)
	assert.Equal(t, "/config/system", got.Filter.Select)
}

// TestEditConfig_MarshalRoundTrip verifies EditConfig with all optional fields set.
func TestEditConfig_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	config := []byte(`<config><system><hostname>router1</hostname></system></config>`)
	orig := netconf.EditConfig{
		Target:           netconf.Datastore{Running: &struct{}{}},
		DefaultOperation: "merge",
		TestOption:       "test-then-set",
		ErrorOption:      "rollback-on-error",
		Config:           config,
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "edit-config")
	assert.Contains(t, xmlStr, "<edit-config ", "must be <edit-config>")
	assert.Contains(t, xmlStr, "<running>", "running target must be present")
	assert.Contains(t, xmlStr, "merge", "default-operation must be present")
	assert.Contains(t, xmlStr, "rollback-on-error", "error-option must be present")
	assert.Contains(t, xmlStr, "router1", "config content must be present")

	var got netconf.EditConfig
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Target.Running)
	assert.Equal(t, "merge", got.DefaultOperation)
	assert.Equal(t, "test-then-set", got.TestOption)
	assert.Equal(t, "rollback-on-error", got.ErrorOption)
	assert.Contains(t, string(got.Config), "router1")
}

// TestEditConfig_OptionalFieldsOmitted verifies optional EditConfig fields
// are absent when zero.
func TestEditConfig_OptionalFieldsOmitted(t *testing.T) {
	t.Parallel()
	orig := netconf.EditConfig{
		Target: netconf.Datastore{Running: &struct{}{}},
		Config: []byte(`<config/>`),
	}
	xmlStr := mustMarshal(t, orig)
	assert.NotContains(t, xmlStr, "default-operation", "must be omitted when empty")
	assert.NotContains(t, xmlStr, "test-option", "must be omitted when empty")
	assert.NotContains(t, xmlStr, "error-option", "must be omitted when empty")
}

// TestCopyConfig_MarshalRoundTrip verifies CopyConfig with candidate→running round-trips.
func TestCopyConfig_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.CopyConfig{
		Source: netconf.Datastore{Candidate: &struct{}{}},
		Target: netconf.Datastore{Running: &struct{}{}},
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "copy-config")
	assert.Contains(t, xmlStr, "<copy-config ", "must be <copy-config>")
	assert.Contains(t, xmlStr, "<running>", "target running must be present")
	assert.Contains(t, xmlStr, "<candidate>", "source candidate must be present")

	var got netconf.CopyConfig
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Source.Candidate)
	assert.NotNil(t, got.Target.Running)
}

// TestDeleteConfig_MarshalRoundTrip verifies DeleteConfig with startup target.
func TestDeleteConfig_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.DeleteConfig{
		Target: netconf.Datastore{Startup: &struct{}{}},
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "delete-config")
	assert.Contains(t, xmlStr, "<delete-config ", "must be <delete-config>")
	assert.Contains(t, xmlStr, "<startup>", "startup target must be present")

	var got netconf.DeleteConfig
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Target.Startup)
}

// TestLock_MarshalRoundTrip verifies Lock with running datastore.
func TestLock_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.Lock{
		Target: netconf.Datastore{Running: &struct{}{}},
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "lock")
	assert.Contains(t, xmlStr, "<lock ", "must be <lock>")
	assert.Contains(t, xmlStr, "<running>")

	var got netconf.Lock
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Target.Running)
}

// TestUnlock_MarshalRoundTrip verifies Unlock with candidate datastore.
func TestUnlock_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.Unlock{
		Target: netconf.Datastore{Candidate: &struct{}{}},
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "unlock")
	assert.Contains(t, xmlStr, "<unlock ", "must be <unlock>")
	assert.Contains(t, xmlStr, "<candidate>")

	var got netconf.Unlock
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Target.Candidate)
}

// TestCloseSession_MarshalRoundTrip verifies CloseSession has no body fields.
func TestCloseSession_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.CloseSession{}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "close-session")
	assert.Contains(t, xmlStr, "close-session", "must contain close-session element")

	var got netconf.CloseSession
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	// Struct has no fields to verify beyond successful unmarshal.
	_ = got
}

// TestKillSession_MarshalRoundTrip verifies KillSession with a session ID.
func TestKillSession_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.KillSession{SessionID: 42}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "kill-session")
	assert.Contains(t, xmlStr, "kill-session", "must contain kill-session element")
	assert.Contains(t, xmlStr, "42", "session-id value must be present")

	var got netconf.KillSession
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.Equal(t, uint32(42), got.SessionID, "SessionID must round-trip")
}

// TestKillSession_ZeroSessionID verifies zero SessionID is still encoded
// (it is a required field per RFC 6241).
func TestKillSession_ZeroSessionID(t *testing.T) {
	t.Parallel()
	orig := netconf.KillSession{SessionID: 0}
	xmlStr := mustMarshal(t, orig)
	// The element must be present even when zero — kill-session always requires it.
	assert.Contains(t, xmlStr, "session-id", "session-id element must always be present")
}

// TestValidate_MarshalRoundTrip verifies Validate with candidate source.
func TestValidate_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.Validate{
		Source: netconf.Datastore{Candidate: &struct{}{}},
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "validate")
	assert.Contains(t, xmlStr, "<validate ", "must be <validate>")
	assert.Contains(t, xmlStr, "<candidate>")

	var got netconf.Validate
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Source.Candidate)
}

// TestCommit_MarshalRoundTrip verifies a plain commit (no confirmed-commit fields).
func TestCommit_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.Commit{}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "commit")
	assert.Contains(t, xmlStr, "commit", "must be a commit element")
	// Optional fields must be absent.
	assert.NotContains(t, xmlStr, "confirmed", "confirmed must be absent when not set")
	assert.NotContains(t, xmlStr, "confirm-timeout", "confirm-timeout must be absent")

	var got netconf.Commit
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.Nil(t, got.Confirmed, "Confirmed must be nil after round-trip of plain commit")
}

// TestCommit_ConfirmedCommit_MarshalRoundTrip verifies all confirmed-commit
// optional fields are encoded and preserved.
func TestCommit_ConfirmedCommit_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.Commit{
		Confirmed:      &struct{}{},
		ConfirmTimeout: 300,
		Persist:        "my-token",
		PersistID:      "prior-token",
	}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "commit")
	assert.Contains(t, xmlStr, "confirmed", "confirmed element must be present")
	assert.Contains(t, xmlStr, "300", "confirm-timeout must be present")
	assert.Contains(t, xmlStr, "my-token", "persist token must be present")
	assert.Contains(t, xmlStr, "prior-token", "persist-id must be present")

	var got netconf.Commit
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.NotNil(t, got.Confirmed)
	assert.Equal(t, uint32(300), got.ConfirmTimeout)
	assert.Equal(t, "my-token", got.Persist)
	assert.Equal(t, "prior-token", got.PersistID)
}

// TestDiscardChanges_MarshalRoundTrip verifies DiscardChanges has no body.
func TestDiscardChanges_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.DiscardChanges{}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "discard-changes")
	assert.Contains(t, xmlStr, "discard-changes", "must contain discard-changes element")

	var got netconf.DiscardChanges
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	_ = got
}

// TestCancelCommit_MarshalRoundTrip verifies CancelCommit with and without PersistID.
func TestCancelCommit_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	// With PersistID.
	orig := netconf.CancelCommit{PersistID: "token-abc"}

	xmlStr := mustMarshal(t, orig)
	assertNSPresent(t, xmlStr, "cancel-commit")
	assert.Contains(t, xmlStr, "cancel-commit", "must contain cancel-commit element")
	assert.Contains(t, xmlStr, "token-abc", "persist-id must be present")

	var got netconf.CancelCommit
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.Equal(t, "token-abc", got.PersistID)
}

// TestCancelCommit_NoPersistID verifies PersistID is omitted when zero.
func TestCancelCommit_NoPersistID(t *testing.T) {
	t.Parallel()
	orig := netconf.CancelCommit{}
	xmlStr := mustMarshal(t, orig)
	assert.NotContains(t, xmlStr, "persist-id", "persist-id must be absent when empty")
}

// ── CreateSubscription (RFC 5277) ─────────────────────────────────────────────

// TestCreateSubscription_MarshalNamespace verifies that CreateSubscription marshals
// with the RFC 5277 notification namespace and the <create-subscription> element name.
func TestCreateSubscription_MarshalNamespace(t *testing.T) {
	t.Parallel()
	cs := netconf.CreateSubscription{}
	xmlStr := mustMarshal(t, cs)

	assert.Contains(t, xmlStr,
		`xmlns="urn:ietf:params:xml:ns:netconf:notification:1.0"`,
		"create-subscription must carry the RFC 5277 notification namespace")
	assert.True(t,
		strings.Contains(xmlStr, "<create-subscription ") ||
			strings.Contains(xmlStr, "<create-subscription>") ||
			strings.Contains(xmlStr, "<create-subscription/>"),
		"root element must be <create-subscription>, got: %s", xmlStr)
	// Must NOT carry the base NETCONF namespace.
	assert.NotContains(t, xmlStr,
		`xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`,
		"create-subscription must NOT carry the base NETCONF namespace")
}

// TestCreateSubscription_RoundTrip verifies that a CreateSubscription with Stream,
// StartTime, and StopTime set survives a marshal/unmarshal round-trip.
func TestCreateSubscription_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.CreateSubscription{
		Stream:    "NETCONF",
		StartTime: "2024-01-01T00:00:00Z",
		StopTime:  "2024-01-02T00:00:00Z",
	}
	xmlStr := mustMarshal(t, orig)

	assert.Contains(t, xmlStr, "NETCONF", "stream must be present")
	assert.Contains(t, xmlStr, "2024-01-01T00:00:00Z", "startTime must be present")
	assert.Contains(t, xmlStr, "2024-01-02T00:00:00Z", "stopTime must be present")

	var got netconf.CreateSubscription
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.Equal(t, orig.Stream, got.Stream, "Stream must survive round-trip")
	assert.Equal(t, orig.StartTime, got.StartTime, "StartTime must survive round-trip")
	assert.Equal(t, orig.StopTime, got.StopTime, "StopTime must survive round-trip")
}

// TestCreateSubscription_OmitEmpty verifies that a CreateSubscription with no optional
// fields set emits only the root element with namespace — no child elements.
func TestCreateSubscription_OmitEmpty(t *testing.T) {
	t.Parallel()
	orig := netconf.CreateSubscription{}
	xmlStr := mustMarshal(t, orig)

	assert.NotContains(t, xmlStr, "<stream>", "stream must be omitted when empty")
	assert.NotContains(t, xmlStr, "<filter>", "filter must be omitted when nil")
	assert.NotContains(t, xmlStr, "<startTime>", "startTime must be omitted when empty")
	assert.NotContains(t, xmlStr, "<stopTime>", "stopTime must be omitted when empty")
	assert.Contains(t, xmlStr,
		`xmlns="urn:ietf:params:xml:ns:netconf:notification:1.0"`,
		"namespace must still be present on empty create-subscription")
}

// TestCreateSubscription_WithFilter verifies that a CreateSubscription with a filter
// marshals the filter child element correctly.
func TestCreateSubscription_WithFilter(t *testing.T) {
	t.Parallel()
	orig := netconf.CreateSubscription{
		Stream: "NETCONF",
		Filter: &netconf.Filter{
			Type:    "subtree",
			Content: []byte(`<netconf-config-change/>`),
		},
	}
	xmlStr := mustMarshal(t, orig)

	assert.Contains(t, xmlStr, `type="subtree"`, "filter type attribute must be present")
	assert.Contains(t, xmlStr, "netconf-config-change", "filter content must be present")

	var got netconf.CreateSubscription
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	require.NotNil(t, got.Filter, "Filter must survive round-trip")
	assert.Equal(t, "subtree", got.Filter.Type, "Filter.Type must survive round-trip")
}

// ── RPC composition test ──────────────────────────────────────────────────────

// TestRPC_WithGetConfig_Composition proves that a GetConfig operation
// can be marshaled into RPC.Body, the full RPC then marshaled to wire format,
// and the result correctly decoded back through RPCReply.Body → DataReply.
func TestRPC_WithGetConfig_Composition(t *testing.T) {
	t.Parallel()
	// Step 1: marshal the GetConfig operation.
	gc := netconf.GetConfig{
		Source: netconf.Datastore{Running: &struct{}{}},
	}
	gcBytes, err := xml.Marshal(gc)
	require.NoError(t, err, "GetConfig marshal must succeed")

	// Step 2: embed into an RPC wrapper.
	rpc := netconf.RPC{
		MessageID: "1",
		Body:      gcBytes,
	}
	rpcBytes, err := xml.Marshal(rpc)
	require.NoError(t, err, "RPC marshal must succeed")

	rpcStr := string(rpcBytes)
	assert.Contains(t, rpcStr, `message-id="1"`, "RPC must have message-id attribute")
	assert.Contains(t, rpcStr, "get-config", "RPC body must contain get-config")
	assert.Contains(t, rpcStr, "<running>", "source datastore must appear in output")

	// Step 3: unmarshal back as an RPC to verify structure.
	var decodedRPC netconf.RPC
	require.NoError(t, xml.Unmarshal(rpcBytes, &decodedRPC))
	assert.Equal(t, "1", decodedRPC.MessageID)
	assert.Contains(t, string(decodedRPC.Body), "get-config")

	// Step 4: decode the GetConfig back from the RPC Body (simulating a server reading the request).
	var gotGC netconf.GetConfig
	require.NoError(t, xml.Unmarshal(decodedRPC.Body, &gotGC),
		"GetConfig must decode from RPC.Body")
	assert.NotNil(t, gotGC.Source.Running, "source running must survive full round-trip")

	// Step 5: simulate a DataReply coming back in RPCReply.Body.
	dataXML := `<data><interfaces><interface><name>lo</name></interface></interfaces></data>`
	reply := netconf.RPCReply{
		MessageID: "1",
		Body:      []byte(dataXML),
	}
	var dr netconf.DataReply
	require.NoError(t, xml.Unmarshal(reply.Body, &dr),
		"DataReply must decode from RPCReply.Body")
	assert.Contains(t, string(dr.Content), "lo",
		"DataReply content must be accessible after composition")
}

// ── Additional namespace verification tests ───────────────────────────────────

// TestAllOperations_HaveNetconfNamespace verifies every operation element
// carries the NETCONF base namespace as xmlns="…" in marshaled output.
// This is the L001 invariant: namespace in struct tag, not set at runtime.
func TestAllOperations_HaveNetconfNamespace(t *testing.T) {
	t.Parallel()
	const ns = `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`

	running := netconf.Datastore{Running: &struct{}{}}

	cases := []struct {
		name string
		v    any
	}{
		{"Get", netconf.Get{}},
		{"GetConfig", netconf.GetConfig{Source: running}},
		{"EditConfig", netconf.EditConfig{Target: running, Config: []byte(`<config/>`)}},
		{"CopyConfig", netconf.CopyConfig{Target: running, Source: running}},
		{"DeleteConfig", netconf.DeleteConfig{Target: running}},
		{"Lock", netconf.Lock{Target: running}},
		{"Unlock", netconf.Unlock{Target: running}},
		{"CloseSession", netconf.CloseSession{}},
		{"KillSession", netconf.KillSession{SessionID: 1}},
		{"Validate", netconf.Validate{Source: running}},
		{"Commit", netconf.Commit{}},
		{"DiscardChanges", netconf.DiscardChanges{}},
		{"CancelCommit", netconf.CancelCommit{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := xml.Marshal(tc.v)
			require.NoError(t, err)
			assert.Contains(t, string(data), ns,
				"%s must carry NETCONF base namespace", tc.name)
		})
	}
}

// ── Correct element names ─────────────────────────────────────────────────────

// TestAllOperations_ElementNames verifies the XML element name for every
// operation matches the RFC 6241 §7 element name exactly.
func TestAllOperations_ElementNames(t *testing.T) {
	t.Parallel()
	running := netconf.Datastore{Running: &struct{}{}}

	cases := []struct {
		name    string
		v       any
		element string
	}{
		{"Get", netconf.Get{}, "get"},
		{"GetConfig", netconf.GetConfig{Source: running}, "get-config"},
		{"EditConfig", netconf.EditConfig{Target: running, Config: []byte(`<config/>`)}, "edit-config"},
		{"CopyConfig", netconf.CopyConfig{Target: running, Source: running}, "copy-config"},
		{"DeleteConfig", netconf.DeleteConfig{Target: running}, "delete-config"},
		{"Lock", netconf.Lock{Target: running}, "lock"},
		{"Unlock", netconf.Unlock{Target: running}, "unlock"},
		{"CloseSession", netconf.CloseSession{}, "close-session"},
		{"KillSession", netconf.KillSession{SessionID: 1}, "kill-session"},
		{"Validate", netconf.Validate{Source: running}, "validate"},
		{"Commit", netconf.Commit{}, "commit"},
		{"DiscardChanges", netconf.DiscardChanges{}, "discard-changes"},
		{"CancelCommit", netconf.CancelCommit{}, "cancel-commit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := xml.Marshal(tc.v)
			require.NoError(t, err)
			xmlStr := string(data)
			assert.True(t, strings.Contains(xmlStr, "<"+tc.element+" ") ||
				strings.Contains(xmlStr, "<"+tc.element+">") ||
				strings.Contains(xmlStr, "<"+tc.element+"/>"),
				"%s must have element name <%s>, got: %s", tc.name, tc.element, xmlStr)
		})
	}
}

// ── Backward-compatibility proof: WithDefaults nil produces unchanged XML ─────

// TestGet_BackwardCompat_NilWithDefaults proves that Get{} with a nil WithDefaults
// field produces XML identical to the pre-change struct (no with-defaults element,
// no extra namespace declarations). This is the key R034 backward-compat assertion.
func TestGet_BackwardCompat_NilWithDefaults(t *testing.T) {
	t.Parallel()
	op := netconf.Get{}
	xmlStr := mustMarshal(t, op)

	assert.NotContains(t, xmlStr, "with-defaults",
		"nil WithDefaults must not emit <with-defaults> element")
	assert.NotContains(t, xmlStr, "ietf-netconf-with-defaults",
		"nil WithDefaults must not introduce with-defaults namespace")
	assertNSPresent(t, xmlStr, "get")
}

// TestGetConfig_BackwardCompat_NilWithDefaults proves that GetConfig with Source set
// but nil WithDefaults produces XML identical to the pre-change struct.
func TestGetConfig_BackwardCompat_NilWithDefaults(t *testing.T) {
	t.Parallel()
	op := netconf.GetConfig{Source: netconf.Datastore{Running: &struct{}{}}}
	xmlStr := mustMarshal(t, op)

	assert.NotContains(t, xmlStr, "with-defaults",
		"nil WithDefaults must not emit <with-defaults> element")
	assert.NotContains(t, xmlStr, "ietf-netconf-with-defaults",
		"nil WithDefaults must not introduce with-defaults namespace")
	assertNSPresent(t, xmlStr, "get-config")
	assert.Contains(t, xmlStr, "<running>", "source datastore must still be present")
}

// TestCopyConfig_BackwardCompat_NilWithDefaults proves that CopyConfig with nil
// WithDefaults produces XML identical to the pre-change struct.
func TestCopyConfig_BackwardCompat_NilWithDefaults(t *testing.T) {
	t.Parallel()
	op := netconf.CopyConfig{
		Source: netconf.Datastore{Candidate: &struct{}{}},
		Target: netconf.Datastore{Running: &struct{}{}},
	}
	xmlStr := mustMarshal(t, op)

	assert.NotContains(t, xmlStr, "with-defaults",
		"nil WithDefaults must not emit <with-defaults> element")
	assert.NotContains(t, xmlStr, "ietf-netconf-with-defaults",
		"nil WithDefaults must not introduce with-defaults namespace")
	assertNSPresent(t, xmlStr, "copy-config")
	assert.Contains(t, xmlStr, "<candidate>", "source candidate must still be present")
	assert.Contains(t, xmlStr, "<running>", "target running must still be present")
}

// ── with-defaults round-trip tests ───────────────────────────────────────────

// TestGet_WithDefaults_RoundTrip verifies that Get with WithDefaults set marshals
// the with-defaults element with the correct namespace and value, and that
// unmarshaling recovers the same mode.
func TestGet_WithDefaults_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.Get{
		WithDefaults: &netconf.WithDefaultsParam{Mode: netconf.WithDefaultsReportAll},
	}
	xmlStr := mustMarshal(t, orig)

	assert.Contains(t, xmlStr,
		`xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults"`,
		"with-defaults element must carry the RFC 6243 YANG namespace")
	assert.Contains(t, xmlStr, "report-all",
		"with-defaults mode value must be present")
	assert.Contains(t, xmlStr, "with-defaults",
		"with-defaults element name must be present")

	var got netconf.Get
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	require.NotNil(t, got.WithDefaults, "WithDefaults must survive round-trip")
	assert.Equal(t, netconf.WithDefaultsReportAll, got.WithDefaults.Mode,
		"WithDefaultsMode must survive round-trip")
}

// TestGetConfig_WithDefaults_RoundTrip verifies GetConfig with WithDefaults set
// round-trips with the correct namespace and mode value.
func TestGetConfig_WithDefaults_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.GetConfig{
		Source:       netconf.Datastore{Running: &struct{}{}},
		WithDefaults: &netconf.WithDefaultsParam{Mode: netconf.WithDefaultsTrim},
	}
	xmlStr := mustMarshal(t, orig)

	assert.Contains(t, xmlStr,
		`xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults"`,
		"with-defaults element must carry the RFC 6243 YANG namespace")
	assert.Contains(t, xmlStr, "trim", "with-defaults mode value must be present")

	var got netconf.GetConfig
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	require.NotNil(t, got.WithDefaults, "WithDefaults must survive round-trip")
	assert.Equal(t, netconf.WithDefaultsTrim, got.WithDefaults.Mode)
}

// TestCopyConfig_WithDefaults_RoundTrip verifies CopyConfig with WithDefaults set
// round-trips with the correct namespace and mode value.
func TestCopyConfig_WithDefaults_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.CopyConfig{
		Source:       netconf.Datastore{Candidate: &struct{}{}},
		Target:       netconf.Datastore{Running: &struct{}{}},
		WithDefaults: &netconf.WithDefaultsParam{Mode: netconf.WithDefaultsExplicit},
	}
	xmlStr := mustMarshal(t, orig)

	assert.Contains(t, xmlStr,
		`xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults"`,
		"with-defaults element must carry the RFC 6243 YANG namespace")
	assert.Contains(t, xmlStr, "explicit", "with-defaults mode value must be present")

	var got netconf.CopyConfig
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	require.NotNil(t, got.WithDefaults, "WithDefaults must survive round-trip")
	assert.Equal(t, netconf.WithDefaultsExplicit, got.WithDefaults.Mode)
}

// TestWithDefaultsMode_AllModes is a table-driven test verifying that all 4
// with-defaults modes marshal to their RFC 6243 string values and unmarshal back
// to the same typed constant.
func TestWithDefaultsMode_AllModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode    netconf.WithDefaultsMode
		wantStr string
	}{
		{netconf.WithDefaultsReportAll, "report-all"},
		{netconf.WithDefaultsTrim, "trim"},
		{netconf.WithDefaultsExplicit, "explicit"},
		{netconf.WithDefaultsReportAllTagged, "report-all-tagged"},
	}
	for _, tc := range cases {
		t.Run(tc.wantStr, func(t *testing.T) {
			t.Parallel()
			param := netconf.WithDefaultsParam{Mode: tc.mode}
			data, err := xml.Marshal(param)
			require.NoError(t, err)
			xmlStr := string(data)

			assert.Contains(t, xmlStr, tc.wantStr,
				"mode string must appear in marshaled XML")
			assert.Contains(t, xmlStr,
				`xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-with-defaults"`,
				"RFC 6243 namespace must be present for mode %s", tc.wantStr)

			var got netconf.WithDefaultsParam
			require.NoError(t, xml.Unmarshal(data, &got))
			assert.Equal(t, tc.mode, got.Mode,
				"mode must survive round-trip for %s", tc.wantStr)
		})
	}
}

// ── Partial-lock round-trip tests ─────────────────────────────────────────────

// TestPartialLock_MarshalRoundTrip verifies that PartialLock with two XPath
// select expressions marshals with the NETCONF base namespace, the correct element
// name, and both select child elements, and that unmarshal recovers the same data.
func TestPartialLock_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.PartialLock{
		Select: []string{
			"/interfaces/interface[name='eth0']",
			"/interfaces/interface[name='eth1']",
		},
	}
	xmlStr := mustMarshal(t, orig)

	assertNSPresent(t, xmlStr, "partial-lock")
	assert.True(t,
		strings.Contains(xmlStr, "<partial-lock ") ||
			strings.Contains(xmlStr, "<partial-lock>"),
		"root element must be <partial-lock>, got: %s", xmlStr)
	assert.Contains(t, xmlStr, "eth0", "first select expression must be present")
	assert.Contains(t, xmlStr, "eth1", "second select expression must be present")
	assert.Contains(t, xmlStr, "<select>", "select must be a child element")

	var got netconf.PartialLock
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	require.Len(t, got.Select, 2, "both select expressions must survive round-trip")
	assert.Equal(t, orig.Select[0], got.Select[0], "first select must round-trip")
	assert.Equal(t, orig.Select[1], got.Select[1], "second select must round-trip")
}

// TestPartialUnlock_MarshalRoundTrip verifies that PartialUnlock with lock-id 42
// marshals with the NETCONF base namespace, the correct element name, and the
// lock-id child element, and that unmarshal recovers the same lock-id.
func TestPartialUnlock_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	orig := netconf.PartialUnlock{LockID: 42}
	xmlStr := mustMarshal(t, orig)

	assertNSPresent(t, xmlStr, "partial-unlock")
	assert.True(t,
		strings.Contains(xmlStr, "<partial-unlock ") ||
			strings.Contains(xmlStr, "<partial-unlock>"),
		"root element must be <partial-unlock>, got: %s", xmlStr)
	assert.Contains(t, xmlStr, "42", "lock-id value must be present")
	assert.Contains(t, xmlStr, "lock-id", "lock-id element must be present")

	var got netconf.PartialUnlock
	require.NoError(t, xml.Unmarshal([]byte(xmlStr), &got))
	assert.Equal(t, uint32(42), got.LockID, "LockID must survive round-trip")
}

// TestPartialLockReply_Unmarshal verifies that a realistic <partial-lock-reply>
// element (as returned by the device inside the <rpc-reply> body) deserializes
// correctly into PartialLockReply.
func TestPartialLockReply_Unmarshal(t *testing.T) {
	t.Parallel()
	replyXML := `<partial-lock-reply>` +
		`<lock-id>17</lock-id>` +
		`<locked-node>/interfaces/interface[name='eth0']</locked-node>` +
		`<locked-node>/interfaces/interface[name='eth1']</locked-node>` +
		`</partial-lock-reply>`

	var got netconf.PartialLockReply
	require.NoError(t, xml.Unmarshal([]byte(replyXML), &got),
		"PartialLockReply must unmarshal from realistic reply body")

	assert.Equal(t, uint32(17), got.LockID, "LockID must be parsed correctly")
	require.Len(t, got.LockedNode, 2, "both locked-node entries must be parsed")
	assert.Contains(t, got.LockedNode[0], "eth0",
		"first locked-node must contain eth0 reference")
	assert.Contains(t, got.LockedNode[1], "eth1",
		"second locked-node must contain eth1 reference")
}
