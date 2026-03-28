package netconf_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers ─────────────────────────────────────────────────────────────────────

// assertNotificationsNS asserts the marshaled XML carries the RFC 6470
// ietf-netconf-notifications namespace.
func assertNotificationsNS(t *testing.T, s string) {
	t.Helper()
	assert.Contains(t, s,
		`xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-notifications"`,
		"output must carry the RFC 6470 notifications namespace")
}

// ── NetconfNotificationsNS constant ──────────────────────────────────────────

func TestNetconfNotificationsNS_Value(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"urn:ietf:params:xml:ns:yang:ietf-netconf-notifications",
		netconf.NetconfNotificationsNS,
		"NetconfNotificationsNS must match the RFC 6470 YANG module namespace")
}

// ── NetconfConfigChange ───────────────────────────────────────────────────────

func TestNetconfConfigChange_RoundTrip(t *testing.T) {
	t.Parallel()
	original := netconf.ConfigChange{
		ChangedBy: netconf.ChangedBy{
			Username:   "alice",
			SessionID:  7,
			SourceHost: "192.0.2.1",
		},
		Datastore: "running",
		Edit: []netconf.EditRecord{
			{Target: "running", Operation: "merge"},
			{Target: "running", Operation: "delete"},
		},
	}

	// Marshal
	out := mustMarshal(t, &original)
	assertNotificationsNS(t, out)
	assert.Contains(t, out, "<netconf-config-change ", "root element name")
	assert.Contains(t, out, "<username>alice</username>")
	assert.Contains(t, out, "<session-id>7</session-id>")
	assert.Contains(t, out, "<source-host>192.0.2.1</source-host>")
	assert.Contains(t, out, "<datastore>running</datastore>")
	assert.Contains(t, out, "<operation>merge</operation>")
	assert.Contains(t, out, "<operation>delete</operation>")

	// Unmarshal
	var decoded netconf.ConfigChange
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, "alice", decoded.ChangedBy.Username)
	assert.Equal(t, uint32(7), decoded.ChangedBy.SessionID)
	assert.Equal(t, "192.0.2.1", decoded.ChangedBy.SourceHost)
	assert.Equal(t, "running", decoded.Datastore)
	require.Len(t, decoded.Edit, 2)
	assert.Equal(t, "merge", decoded.Edit[0].Operation)
	assert.Equal(t, "delete", decoded.Edit[1].Operation)
}

func TestNetconfConfigChange_ServerInitiated(t *testing.T) {
	t.Parallel()
	// ChangedBy with Server set (YANG choice: server-or-user = server)
	s := struct{}{}
	original := netconf.ConfigChange{
		ChangedBy: netconf.ChangedBy{Server: &s},
	}
	out := mustMarshal(t, &original)

	// <server/> must appear; username/session-id/source-host must not
	assert.Contains(t, out, "<server></server>", "server element must be present")
	assert.NotContains(t, out, "<username>", "username must be absent for server-initiated")
	assert.NotContains(t, out, "<session-id>", "session-id must be absent for server-initiated")

	// Unmarshal back
	var decoded netconf.ConfigChange
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.NotNil(t, decoded.ChangedBy.Server, "Server pointer must be non-nil after unmarshal")
	assert.Empty(t, decoded.ChangedBy.Username)
}

func TestNetconfConfigChange_UnmarshalFromWire(t *testing.T) {
	t.Parallel()
	// Simulate a wire-format notification body fragment (no envelope wrapper)
	wire := `<netconf-config-change xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-notifications">
  <changed-by><username>bob</username><session-id>3</session-id></changed-by>
  <datastore>candidate</datastore>
  <edit><target>candidate</target><operation>replace</operation></edit>
</netconf-config-change>`

	var decoded netconf.ConfigChange
	require.NoError(t, xml.Unmarshal([]byte(wire), &decoded))
	assert.Equal(t, "bob", decoded.ChangedBy.Username)
	assert.Equal(t, uint32(3), decoded.ChangedBy.SessionID)
	assert.Equal(t, "candidate", decoded.Datastore)
	require.Len(t, decoded.Edit, 1)
	assert.Equal(t, "replace", decoded.Edit[0].Operation)
}

// ── NetconfCapabilityChange ───────────────────────────────────────────────────

func TestNetconfCapabilityChange_RoundTrip(t *testing.T) {
	t.Parallel()
	original := netconf.CapabilityChange{
		ChangedBy: netconf.ChangedBy{
			Username:  "carol",
			SessionID: 12,
		},
		AddedCapability:    []string{"urn:ietf:params:netconf:capability:startup:1.0"},
		DeletedCapability:  []string{"urn:ietf:params:netconf:capability:candidate:1.0"},
		ModifiedCapability: []string{"urn:ietf:params:netconf:capability:rollback-on-error:1.0"},
	}

	out := mustMarshal(t, &original)
	assertNotificationsNS(t, out)
	assert.Contains(t, out, "<netconf-capability-change ")
	assert.Contains(t, out, "<added-capability>")
	assert.Contains(t, out, "<deleted-capability>")
	assert.Contains(t, out, "<modified-capability>")

	var decoded netconf.CapabilityChange
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, "carol", decoded.ChangedBy.Username)
	require.Len(t, decoded.AddedCapability, 1)
	assert.Equal(t, "urn:ietf:params:netconf:capability:startup:1.0", decoded.AddedCapability[0])
	require.Len(t, decoded.DeletedCapability, 1)
	require.Len(t, decoded.ModifiedCapability, 1)
}

func TestNetconfCapabilityChange_EmptyLists(t *testing.T) {
	t.Parallel()
	// When no capabilities change, the list fields should be omitted
	original := netconf.CapabilityChange{
		ChangedBy: netconf.ChangedBy{Username: "dave"},
	}
	out := mustMarshal(t, &original)
	assert.NotContains(t, out, "<added-capability>", "empty added list must be omitted")
	assert.NotContains(t, out, "<deleted-capability>", "empty deleted list must be omitted")
	assert.NotContains(t, out, "<modified-capability>", "empty modified list must be omitted")
}

// ── NetconfSessionStart ───────────────────────────────────────────────────────

func TestNetconfSessionStart_RoundTrip(t *testing.T) {
	t.Parallel()
	original := netconf.SessionStart{
		Username:   "eve",
		SessionID:  42,
		SourceHost: "10.0.0.1",
	}

	out := mustMarshal(t, &original)
	assertNotificationsNS(t, out)
	assert.Contains(t, out, "<netconf-session-start ")
	assert.Contains(t, out, "<username>eve</username>")
	assert.Contains(t, out, "<session-id>42</session-id>")
	assert.Contains(t, out, "<source-host>10.0.0.1</source-host>")

	var decoded netconf.SessionStart
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, "eve", decoded.Username)
	assert.Equal(t, uint32(42), decoded.SessionID)
	assert.Equal(t, "10.0.0.1", decoded.SourceHost)
}

func TestNetconfSessionStart_NoSourceHost(t *testing.T) {
	t.Parallel()
	// SourceHost is optional — must be omitted when empty
	original := netconf.SessionStart{
		Username:  "frank",
		SessionID: 1,
	}
	out := mustMarshal(t, &original)
	assert.NotContains(t, out, "<source-host>", "absent SourceHost must be omitted")

	var decoded netconf.SessionStart
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Empty(t, decoded.SourceHost)
}

// ── NetconfSessionEnd ─────────────────────────────────────────────────────────

func TestNetconfSessionEnd_RoundTrip(t *testing.T) {
	t.Parallel()
	original := netconf.SessionEnd{
		Username:          "grace",
		SessionID:         99,
		SourceHost:        "172.16.0.1",
		TerminationReason: "closed",
	}

	out := mustMarshal(t, &original)
	assertNotificationsNS(t, out)
	assert.Contains(t, out, "<netconf-session-end ")
	assert.Contains(t, out, "<username>grace</username>")
	assert.Contains(t, out, "<session-id>99</session-id>")
	assert.Contains(t, out, "<termination-reason>closed</termination-reason>")

	var decoded netconf.SessionEnd
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, "grace", decoded.Username)
	assert.Equal(t, uint32(99), decoded.SessionID)
	assert.Equal(t, "closed", decoded.TerminationReason)
}

func TestNetconfSessionEnd_KilledBy(t *testing.T) {
	t.Parallel()
	// When a session is killed, KilledBy carries the killing session-id
	original := netconf.SessionEnd{
		Username:          "hunter",
		SessionID:         5,
		KilledBy:          2,
		TerminationReason: "killed",
	}
	out := mustMarshal(t, &original)
	assert.Contains(t, out, "<killed-by>2</killed-by>")

	var decoded netconf.SessionEnd
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, uint32(2), decoded.KilledBy)
	assert.Equal(t, "killed", decoded.TerminationReason)
}

func TestNetconfSessionEnd_KilledByOmittedWhenZero(t *testing.T) {
	t.Parallel()
	// KilledBy must be absent when termination-reason is not "killed"
	original := netconf.SessionEnd{
		Username:          "ivan",
		SessionID:         8,
		KilledBy:          0, // zero — must be omitted
		TerminationReason: "timeout",
	}
	out := mustMarshal(t, &original)
	assert.NotContains(t, out, "<killed-by>", "KilledBy must be omitted when zero")

	var decoded netconf.SessionEnd
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, uint32(0), decoded.KilledBy)
	assert.Equal(t, "timeout", decoded.TerminationReason)
}

// ── NetconfConfirmedCommit ────────────────────────────────────────────────────

func TestNetconfConfirmedCommit_RoundTrip(t *testing.T) {
	t.Parallel()
	original := netconf.ConfirmedCommit{
		Username:     "judy",
		SessionID:    15,
		SourceHost:   "203.0.113.5",
		ConfirmEvent: "start",
		Timeout:      600,
	}

	out := mustMarshal(t, &original)
	assertNotificationsNS(t, out)
	assert.Contains(t, out, "<netconf-confirmed-commit ")
	assert.Contains(t, out, "<username>judy</username>")
	assert.Contains(t, out, "<confirm-event>start</confirm-event>")
	assert.Contains(t, out, "<timeout>600</timeout>")

	var decoded netconf.ConfirmedCommit
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, "judy", decoded.Username)
	assert.Equal(t, uint32(15), decoded.SessionID)
	assert.Equal(t, "203.0.113.5", decoded.SourceHost)
	assert.Equal(t, "start", decoded.ConfirmEvent)
	assert.Equal(t, uint32(600), decoded.Timeout)
}

func TestNetconfConfirmedCommit_TimeoutEvent(t *testing.T) {
	t.Parallel()
	// confirm-event=timeout means no user session — Username/SessionID absent
	original := netconf.ConfirmedCommit{
		ConfirmEvent: "timeout",
	}
	out := mustMarshal(t, &original)
	assert.NotContains(t, out, "<username>", "username must be absent for timeout event")
	assert.NotContains(t, out, "<session-id>", "session-id must be absent for timeout event")
	assert.Contains(t, out, "<confirm-event>timeout</confirm-event>")

	var decoded netconf.ConfirmedCommit
	require.NoError(t, xml.Unmarshal([]byte(out), &decoded))
	assert.Empty(t, decoded.Username)
	assert.Equal(t, uint32(0), decoded.SessionID)
	assert.Equal(t, "timeout", decoded.ConfirmEvent)
}

func TestNetconfConfirmedCommit_UnmarshalFromWire(t *testing.T) {
	t.Parallel()
	wire := `<netconf-confirmed-commit xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-notifications">
  <username>kate</username>
  <session-id>20</session-id>
  <source-host>198.51.100.1</source-host>
  <confirm-event>extend</confirm-event>
  <timeout>300</timeout>
</netconf-confirmed-commit>`

	var decoded netconf.ConfirmedCommit
	require.NoError(t, xml.Unmarshal([]byte(wire), &decoded))
	assert.Equal(t, "kate", decoded.Username)
	assert.Equal(t, uint32(20), decoded.SessionID)
	assert.Equal(t, "198.51.100.1", decoded.SourceHost)
	assert.Equal(t, "extend", decoded.ConfirmEvent)
	assert.Equal(t, uint32(300), decoded.Timeout)
}

// ── Body-wrapping pattern (L008 / P012) ───────────────────────────────────────

// TestNotificationBody_WrapperDecode demonstrates the synthetic-wrapper pattern
// documented in the package godoc and L008. Notification.Body contains sibling
// nodes (eventTime + the notification element), so callers must wrap before decoding.
func TestNotificationBody_WrapperDecode(t *testing.T) {
	t.Parallel()
	// Simulate what Notification.Body looks like after xml.Unmarshal of a full
	// notification envelope — it contains both <eventTime> and the content element.
	simulatedBody := []byte(
		`<eventTime>2024-01-15T12:00:00Z</eventTime>` +
			`<netconf-session-start xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-notifications">` +
			`<username>leo</username><session-id>77</session-id>` +
			`</netconf-session-start>`,
	)

	// Direct unmarshal into NetconfSessionStart MUST fail or produce empty struct,
	// because the bytes do not form a single-root document.
	var direct netconf.SessionStart
	// We don't assert failure here — the behavior is implementation-defined for
	// multi-root bytes — but after the direct attempt the struct should be empty.
	_ = xml.Unmarshal(simulatedBody, &direct)
	// Direct unmarshal on multi-root bytes yields zero value — wrap is required.

	// Correct approach: synthetic wrapper
	wrapped := append([]byte("<w>"), append(simulatedBody, []byte("</w>")...)...)
	var wrapper struct {
		SessionStart netconf.SessionStart `xml:"netconf-session-start"`
	}
	require.NoError(t, xml.Unmarshal(wrapped, &wrapper))
	assert.Equal(t, "leo", wrapper.SessionStart.Username)
	assert.Equal(t, uint32(77), wrapper.SessionStart.SessionID)
}

// ── Namespace constant distinctness ──────────────────────────────────────────

func TestNamespaceConstants_AreDistinct(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t, netconf.NetconfNS, netconf.NotificationNS,
		"base NS and notification envelope NS must differ")
	assert.NotEqual(t, netconf.NotificationNS, netconf.NetconfNotificationsNS,
		"RFC 5277 envelope NS and RFC 6470 content NS must differ")
	assert.NotEqual(t, netconf.NetconfNS, netconf.NetconfNotificationsNS,
		"base NS and RFC 6470 content NS must differ")
}

// ── XML element name verification ────────────────────────────────────────────

func TestNotificationTypes_ElementNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   any
		element string
	}{
		{
			"NetconfConfigChange",
			&netconf.ConfigChange{ChangedBy: netconf.ChangedBy{Username: "x"}},
			"netconf-config-change",
		},
		{
			"NetconfCapabilityChange",
			&netconf.CapabilityChange{ChangedBy: netconf.ChangedBy{Username: "x"}},
			"netconf-capability-change",
		},
		{
			"NetconfSessionStart",
			&netconf.SessionStart{Username: "x", SessionID: 1},
			"netconf-session-start",
		},
		{
			"NetconfSessionEnd",
			&netconf.SessionEnd{Username: "x", SessionID: 1, TerminationReason: "closed"},
			"netconf-session-end",
		},
		{
			"NetconfConfirmedCommit",
			&netconf.ConfirmedCommit{ConfirmEvent: "start"},
			"netconf-confirmed-commit",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := mustMarshal(t, tc.value)
			assertNotificationsNS(t, out)
			assert.True(t,
				strings.HasPrefix(out, "<"+tc.element+" ") || strings.HasPrefix(out, "<"+tc.element+">"),
				"root element must be <%s>, got: %s", tc.element, out[:min(len(out), 80)])
		})
	}
}
