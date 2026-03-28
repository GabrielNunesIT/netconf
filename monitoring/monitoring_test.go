package monitoring_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/GabrielNunesIT/netconf/monitoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustMarshal marshals v to XML and fails the test on error.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := xml.Marshal(v)
	require.NoError(t, err, "xml.Marshal must succeed")
	return b
}

// ── TestNetconfState_RoundTrip ────────────────────────────────────────────────

// TestNetconfState_RoundTrip verifies that a fully populated NetconfState
// marshals to XML with the correct monitoring namespace and then unmarshals
// back to an equal value.
func TestNetconfState_RoundTrip(t *testing.T) {
	t.Parallel()
	original := monitoring.NetconfState{
		Capabilities: []string{
			"urn:ietf:params:netconf:base:1.0",
			monitoring.CapabilityURI,
		},
		Datastores: []monitoring.Datastore{
			{
				Name: "running",
				Locks: &monitoring.LockInfo{
					GlobalLock: &monitoring.GlobalLock{
						LockedBySession: 42,
						LockedTime:      "2024-01-01T00:00:00Z",
					},
				},
			},
			{
				Name: "candidate",
				Locks: &monitoring.LockInfo{
					PartialLock: []monitoring.PartialLockInfo{
						{
							LockID:     1,
							LockedTime: "2024-06-01T12:00:00Z",
							LockedNode: []string{"/interfaces", "/routing"},
							Select:     []string{"/interfaces"},
						},
					},
				},
			},
		},
		Schemas: []monitoring.Schema{
			{
				Identifier: "ietf-interfaces",
				Version:    "2018-02-20",
				Format:     "yang",
				Namespace:  "urn:ietf:params:xml:ns:yang:ietf-interfaces",
				Location:   []string{"NETCONF"},
			},
		},
		Sessions: []monitoring.Session{
			{
				SessionID:        7,
				Transport:        "netconf-ssh",
				Username:         "admin",
				SourceHost:       "192.0.2.1",
				LoginTime:        "2024-03-15T08:00:00Z",
				InRPCs:           10,
				InBadRPCs:        1,
				OutRPCErrors:     0,
				OutNotifications: 3,
			},
		},
		Statistics: &monitoring.Statistics{
			NetconfStartTime: "2024-01-01T00:00:00Z",
			InBadHellos:      2,
			InSessions:       50,
			DroppedSessions:  3,
			InRPCs:           1000,
			InBadRPCs:        5,
			OutRPCErrors:     2,
			OutNotifications: 100,
		},
	}

	xmlBytes := mustMarshal(t, original)
	xmlStr := string(xmlBytes)
	t.Logf("marshaled NetconfState:\n%s", xmlStr)

	// Verify the monitoring namespace is present.
	assert.Contains(t, xmlStr, monitoring.MonitoringNS,
		"marshaled XML must contain the monitoring namespace")
	assert.Contains(t, xmlStr, "netconf-state",
		"marshaled XML must contain the netconf-state element name")

	// Verify key content is present.
	assert.Contains(t, xmlStr, "ietf-interfaces",
		"schema identifier must appear in marshaled XML")
	assert.Contains(t, xmlStr, "locked-by-session",
		"global lock fields must appear in marshaled XML")

	// Round-trip: unmarshal back and compare.
	var got monitoring.NetconfState
	require.NoError(t, xml.Unmarshal(xmlBytes, &got), "xml.Unmarshal must succeed")

	assert.Equal(t, original.Capabilities, got.Capabilities, "Capabilities must round-trip")
	require.Len(t, got.Datastores, 2, "must have 2 datastores after round-trip")
	assert.Equal(t, "running", got.Datastores[0].Name, "first datastore name must be running")
	require.NotNil(t, got.Datastores[0].Locks, "running datastore must have locks")
	require.NotNil(t, got.Datastores[0].Locks.GlobalLock, "running datastore must have global lock")
	assert.Equal(t, uint32(42), got.Datastores[0].Locks.GlobalLock.LockedBySession,
		"LockedBySession must round-trip")
	assert.Equal(t, "candidate", got.Datastores[1].Name, "second datastore name must be candidate")
	require.Len(t, got.Datastores[1].Locks.PartialLock, 1, "candidate must have 1 partial lock")
	assert.Equal(t, uint32(1), got.Datastores[1].Locks.PartialLock[0].LockID,
		"PartialLockInfo.LockID must round-trip")
	assert.Equal(t, []string{"/interfaces", "/routing"},
		got.Datastores[1].Locks.PartialLock[0].LockedNode,
		"PartialLockInfo.LockedNode must round-trip")

	require.Len(t, got.Schemas, 1, "must have 1 schema after round-trip")
	assert.Equal(t, original.Schemas[0], got.Schemas[0], "Schema must round-trip exactly")

	require.Len(t, got.Sessions, 1, "must have 1 session after round-trip")
	assert.Equal(t, original.Sessions[0], got.Sessions[0], "Session must round-trip exactly")

	require.NotNil(t, got.Statistics, "Statistics must not be nil after round-trip")
	assert.Equal(t, *original.Statistics, *got.Statistics, "Statistics must round-trip exactly")
}

// ── TestNetconfState_UnmarshalFromWire ────────────────────────────────────────

// TestNetconfState_UnmarshalFromWire unmarshals a realistic wire-format
// <netconf-state> document (simulating a server response) and verifies all
// fields parse correctly.
func TestNetconfState_UnmarshalFromWire(t *testing.T) {
	t.Parallel()
	const wireXML = `<netconf-state xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring">` +
		`<capabilities>` +
		`<capability>urn:ietf:params:netconf:base:1.1</capability>` +
		`<capability>urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring</capability>` +
		`</capabilities>` +
		`<datastores>` +
		`<datastore><name>running</name></datastore>` +
		`</datastores>` +
		`<schemas>` +
		`<schema>` +
		`<identifier>ietf-netconf-monitoring</identifier>` +
		`<version>2010-10-04</version>` +
		`<format>yang</format>` +
		`<namespace>urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring</namespace>` +
		`<location>NETCONF</location>` +
		`</schema>` +
		`</schemas>` +
		`<sessions>` +
		`<session>` +
		`<session-id>3</session-id>` +
		`<transport>netconf-ssh</transport>` +
		`<username>operator</username>` +
		`<login-time>2024-12-01T09:00:00Z</login-time>` +
		`<in-rpcs>5</in-rpcs>` +
		`<in-bad-rpcs>0</in-bad-rpcs>` +
		`<out-rpc-errors>0</out-rpc-errors>` +
		`<out-notifications>0</out-notifications>` +
		`</session>` +
		`</sessions>` +
		`<statistics>` +
		`<netconf-start-time>2024-11-01T00:00:00Z</netconf-start-time>` +
		`<in-bad-hellos>0</in-bad-hellos>` +
		`<in-sessions>10</in-sessions>` +
		`<dropped-sessions>1</dropped-sessions>` +
		`<in-rpcs>200</in-rpcs>` +
		`<in-bad-rpcs>3</in-bad-rpcs>` +
		`<out-rpc-errors>1</out-rpc-errors>` +
		`<out-notifications>50</out-notifications>` +
		`</statistics>` +
		`</netconf-state>`

	var state monitoring.NetconfState
	require.NoError(t, xml.Unmarshal([]byte(wireXML), &state), "must unmarshal wire XML")

	require.Len(t, state.Capabilities, 2, "must have 2 capabilities")
	assert.Equal(t, "urn:ietf:params:netconf:base:1.1", state.Capabilities[0])
	assert.Equal(t, monitoring.CapabilityURI, state.Capabilities[1])

	require.Len(t, state.Datastores, 1)
	assert.Equal(t, "running", state.Datastores[0].Name)
	assert.Nil(t, state.Datastores[0].Locks, "datastore with no locks must have nil LockInfo")

	require.Len(t, state.Schemas, 1)
	assert.Equal(t, "ietf-netconf-monitoring", state.Schemas[0].Identifier)
	assert.Equal(t, "2010-10-04", state.Schemas[0].Version)
	assert.Equal(t, "yang", state.Schemas[0].Format)
	assert.Equal(t, []string{"NETCONF"}, state.Schemas[0].Location)

	require.Len(t, state.Sessions, 1)
	assert.Equal(t, uint32(3), state.Sessions[0].SessionID)
	assert.Equal(t, "operator", state.Sessions[0].Username)
	assert.Equal(t, uint32(5), state.Sessions[0].InRPCs)

	require.NotNil(t, state.Statistics)
	assert.Equal(t, "2024-11-01T00:00:00Z", state.Statistics.NetconfStartTime)
	assert.Equal(t, uint32(10), state.Statistics.InSessions)
	assert.Equal(t, uint32(1), state.Statistics.DroppedSessions)
}

// ── TestGetSchemaRequest_Marshal ──────────────────────────────────────────────

// TestGetSchemaRequest_Marshal verifies that GetSchemaRequest marshals with the
// correct monitoring namespace on the <get-schema> element, and that optional
// fields are omitted when empty.
func TestGetSchemaRequest_Marshal(t *testing.T) {
	t.Parallel()
	t.Run("full", func(t *testing.T) {
		t.Parallel()
		req := monitoring.GetSchemaRequest{
			Identifier: "ietf-interfaces",
			Version:    "2018-02-20",
			Format:     "yang",
		}
		xmlBytes := mustMarshal(t, req)
		xmlStr := string(xmlBytes)
		t.Logf("marshaled GetSchemaRequest (full):\n%s", xmlStr)

		assert.Contains(t, xmlStr, monitoring.MonitoringNS,
			"get-schema must carry monitoring namespace")
		assert.Contains(t, xmlStr, "get-schema",
			"element name must be get-schema")
		assert.Contains(t, xmlStr, "ietf-interfaces",
			"identifier must appear in marshaled XML")
		assert.Contains(t, xmlStr, "2018-02-20",
			"version must appear in marshaled XML")
		assert.Contains(t, xmlStr, "yang",
			"format must appear in marshaled XML")
	})

	t.Run("identifier_only", func(t *testing.T) {
		t.Parallel()
		req := monitoring.GetSchemaRequest{Identifier: "ietf-netconf"}
		xmlBytes := mustMarshal(t, req)
		xmlStr := string(xmlBytes)
		t.Logf("marshaled GetSchemaRequest (identifier only):\n%s", xmlStr)

		assert.Contains(t, xmlStr, monitoring.MonitoringNS,
			"namespace must be present even when version/format omitted")
		assert.NotContains(t, xmlStr, "<version",
			"version element must be absent when empty")
		assert.NotContains(t, xmlStr, "<format",
			"format element must be absent when empty")
	})
}

// ── TestGetSchemaReply_Unmarshal ──────────────────────────────────────────────

// TestGetSchemaReply_Unmarshal verifies that raw schema text content in a
// <data> element survives unmarshal as []byte in GetSchemaReply.Content.
func TestGetSchemaReply_Unmarshal(t *testing.T) {
	t.Parallel()
	const yangSchema = `module ietf-interfaces {
  yang-version 1.1;
  namespace "urn:ietf:params:xml:ns:yang:ietf-interfaces";
}`
	// The server wraps the YANG text inside a <data> element.
	dataXML := `<data>` + yangSchema + `</data>`

	var reply monitoring.GetSchemaReply
	require.NoError(t, xml.Unmarshal([]byte(dataXML), &reply),
		"must unmarshal GetSchemaReply")

	assert.Equal(t, "data", reply.XMLName.Local,
		"XMLName.Local must be 'data'")
	content := string(reply.Content)
	assert.Contains(t, content, "ietf-interfaces",
		"Content must contain the YANG module name")
	assert.Contains(t, content, "yang-version",
		"Content must contain YANG text content")
}

// TestGetSchemaReply_MarshalRoundTrip verifies that GetSchemaReply also
// marshals correctly (in case a test or tool needs to re-encode it).
func TestGetSchemaReply_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	original := monitoring.GetSchemaReply{
		Content: []byte(`<yang-text>module foo { }</yang-text>`),
	}
	xmlBytes := mustMarshal(t, original)
	xmlStr := string(xmlBytes)
	t.Logf("marshaled GetSchemaReply:\n%s", xmlStr)

	assert.Contains(t, xmlStr, "<data",
		"marshaled GetSchemaReply must use <data> element")
	assert.Contains(t, xmlStr, "yang-text",
		"Content bytes must appear in marshaled output")

	var got monitoring.GetSchemaReply
	require.NoError(t, xml.Unmarshal(xmlBytes, &got), "must unmarshal back")
	assert.Equal(t, string(original.Content), strings.TrimSpace(string(got.Content)),
		"Content must round-trip")
}

// ── TestSession_RoundTrip ─────────────────────────────────────────────────────

// TestSession_RoundTrip verifies that Session marshals and unmarshals correctly
// including the omitempty behaviour on SourceHost.
func TestSession_RoundTrip(t *testing.T) {
	t.Parallel()
	t.Run("with_source_host", func(t *testing.T) {
		t.Parallel()
		original := monitoring.Session{
			SessionID:        99,
			Transport:        "netconf-tls",
			Username:         "alice",
			SourceHost:       "10.0.0.1",
			LoginTime:        "2025-01-01T00:00:00Z",
			InRPCs:           42,
			InBadRPCs:        0,
			OutRPCErrors:     1,
			OutNotifications: 7,
		}
		xmlBytes := mustMarshal(t, original)
		var got monitoring.Session
		require.NoError(t, xml.Unmarshal(xmlBytes, &got))
		assert.Equal(t, original, got, "Session must round-trip exactly with SourceHost")
	})

	t.Run("without_source_host", func(t *testing.T) {
		t.Parallel()
		original := monitoring.Session{
			SessionID: 1,
			Transport: "netconf-ssh",
			Username:  "bob",
			LoginTime: "2025-06-01T12:00:00Z",
		}
		xmlBytes := mustMarshal(t, original)
		xmlStr := string(xmlBytes)
		assert.NotContains(t, xmlStr, "source-host",
			"source-host element must be absent when SourceHost is empty")
		var got monitoring.Session
		require.NoError(t, xml.Unmarshal(xmlBytes, &got))
		assert.Equal(t, original, got, "Session must round-trip without SourceHost")
	})
}

// ── TestStatistics_RoundTrip ──────────────────────────────────────────────────

// TestStatistics_RoundTrip verifies that Statistics marshals and unmarshals
// correctly for all counter fields.
func TestStatistics_RoundTrip(t *testing.T) {
	t.Parallel()
	original := monitoring.Statistics{
		NetconfStartTime: "2024-01-15T08:30:00Z",
		InBadHellos:      5,
		InSessions:       200,
		DroppedSessions:  10,
		InRPCs:           5000,
		InBadRPCs:        25,
		OutRPCErrors:     15,
		OutNotifications: 1000,
	}
	xmlBytes := mustMarshal(t, original)
	t.Logf("marshaled Statistics:\n%s", string(xmlBytes))

	var got monitoring.Statistics
	require.NoError(t, xml.Unmarshal(xmlBytes, &got), "Statistics must unmarshal")
	assert.Equal(t, original, got, "Statistics must round-trip exactly")
}

// ── TestConstants ─────────────────────────────────────────────────────────────

// TestConstants verifies the exported namespace constants have the correct
// values as specified by RFC 6022.
func TestConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring",
		monitoring.MonitoringNS,
		"MonitoringNS must equal the RFC 6022 YANG module namespace")
	assert.Equal(t,
		"urn:ietf:params:xml:ns:yang:ietf-netconf-monitoring",
		monitoring.CapabilityURI,
		"CapabilityURI must equal MonitoringNS for ietf-netconf-monitoring")
}

// ── TestPartialLockInfo_RoundTrip ─────────────────────────────────────────────

// TestPartialLockInfo_RoundTrip verifies PartialLockInfo (used inside LockInfo)
// round-trips with all fields including multiple LockedNode and Select values.
func TestPartialLockInfo_RoundTrip(t *testing.T) {
	t.Parallel()
	original := monitoring.PartialLockInfo{
		LockID:     7,
		LockedTime: "2025-03-01T10:00:00Z",
		LockedNode: []string{"/interfaces/interface[name='eth0']", "/routing"},
		Select:     []string{"/interfaces", "/routing"},
	}
	xmlBytes := mustMarshal(t, original)
	t.Logf("marshaled PartialLockInfo:\n%s", string(xmlBytes))

	var got monitoring.PartialLockInfo
	require.NoError(t, xml.Unmarshal(xmlBytes, &got), "PartialLockInfo must unmarshal")
	assert.Equal(t, original, got, "PartialLockInfo must round-trip exactly")
}
