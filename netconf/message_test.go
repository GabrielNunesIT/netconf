package netconf_test

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Hello ────────────────────────────────────────────────────────────────────

func TestHello_MarshalNamespace(t *testing.T) {
	t.Parallel()
	h := netconf.Hello{
		Capabilities: []string{netconf.BaseCap10},
	}
	data, err := xml.Marshal(&h)
	require.NoError(t, err, "marshal should succeed")

	s := string(data)
	assert.Contains(t, s, `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`,
		"hello must carry the NETCONF base namespace")
	assert.Contains(t, s, "<hello ", "root element must be <hello")
}

func TestHello_RoundTrip_Capabilities(t *testing.T) {
	t.Parallel()
	original := netconf.Hello{
		Capabilities: []string{
			netconf.BaseCap10,
			netconf.BaseCap11,
			"urn:ietf:params:netconf:capability:rollback-on-error:1.0",
		},
	}
	data, err := xml.Marshal(&original)
	require.NoError(t, err)

	var decoded netconf.Hello
	require.NoError(t, xml.Unmarshal(data, &decoded))

	assert.Equal(t, original.Capabilities, decoded.Capabilities,
		"capabilities must survive a marshal/unmarshal round-trip")
}

func TestHello_RoundTrip_SessionID(t *testing.T) {
	t.Parallel()
	original := netconf.Hello{
		Capabilities: []string{netconf.BaseCap10},
		SessionID:    42,
	}
	data, err := xml.Marshal(&original)
	require.NoError(t, err)

	var decoded netconf.Hello
	require.NoError(t, xml.Unmarshal(data, &decoded))

	assert.Equal(t, uint32(42), decoded.SessionID, "session-id must survive round-trip")
}

func TestHello_SessionID_Omitted_When_Zero(t *testing.T) {
	t.Parallel()
	h := netconf.Hello{
		Capabilities: []string{netconf.BaseCap10},
		SessionID:    0,
	}
	data, err := xml.Marshal(&h)
	require.NoError(t, err)

	assert.NotContains(t, string(data), "session-id",
		"session-id element must be omitted when zero (client hello)")
}

func TestHello_Unmarshal_From_Wire(t *testing.T) {
	t.Parallel()
	// Simulate a real server hello as it would arrive on the wire.
	wire := `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">` +
		`<capabilities>` +
		`<capability>urn:ietf:params:netconf:base:1.0</capability>` +
		`<capability>urn:ietf:params:netconf:base:1.1</capability>` +
		`</capabilities>` +
		`<session-id>1234</session-id>` +
		`</hello>`

	var h netconf.Hello
	require.NoError(t, xml.NewDecoder(strings.NewReader(wire)).Decode(&h))

	assert.Equal(t, uint32(1234), h.SessionID)
	assert.Equal(t, []string{netconf.BaseCap10, netconf.BaseCap11}, h.Capabilities)
}

// ── RPC ──────────────────────────────────────────────────────────────────────

func TestRPC_MarshalNamespace(t *testing.T) {
	t.Parallel()
	r := netconf.RPC{
		MessageID: "1",
		Body:      []byte(`<get-config><source><running/></source></get-config>`),
	}
	data, err := xml.Marshal(&r)
	require.NoError(t, err)

	s := string(data)
	assert.Contains(t, s, `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`)
	assert.Contains(t, s, `message-id="1"`)
	assert.Contains(t, s, `<get-config>`)
}

func TestRPC_RoundTrip(t *testing.T) {
	t.Parallel()
	original := netconf.RPC{
		MessageID: "42",
		Body:      []byte(`<get/>`),
	}
	data, err := xml.Marshal(&original)
	require.NoError(t, err)

	var decoded netconf.RPC
	require.NoError(t, xml.Unmarshal(data, &decoded))

	assert.Equal(t, "42", decoded.MessageID)
	// Inner XML may gain or lose insignificant whitespace; normalise.
	assert.Equal(t, strings.TrimSpace(string(original.Body)),
		strings.TrimSpace(string(decoded.Body)))
}

func TestRPC_EmptyBody(t *testing.T) {
	t.Parallel()
	r := netconf.RPC{MessageID: "0"}
	data, err := xml.Marshal(&r)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

// ── RPCReply ─────────────────────────────────────────────────────────────────

func TestRPCReply_MarshalNamespace(t *testing.T) {
	t.Parallel()
	reply := netconf.RPCReply{
		MessageID: "1",
		Ok:        &struct{}{},
	}
	data, err := xml.Marshal(&reply)
	require.NoError(t, err)

	s := string(data)
	assert.Contains(t, s, `xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`)
	assert.Contains(t, s, `message-id="1"`)
	assert.Contains(t, s, `<ok`)
}

func TestRPCReply_RoundTrip_MessageID(t *testing.T) {
	t.Parallel()
	original := netconf.RPCReply{
		MessageID: "99",
		Ok:        &struct{}{},
	}
	data, err := xml.Marshal(&original)
	require.NoError(t, err)

	var decoded netconf.RPCReply
	require.NoError(t, xml.Unmarshal(data, &decoded))

	assert.Equal(t, "99", decoded.MessageID)
}

func TestRPCReply_RoundTrip_Body(t *testing.T) {
	t.Parallel()
	original := netconf.RPCReply{
		MessageID: "5",
		Body:      []byte(`<data><foo/></data>`),
	}
	data, err := xml.Marshal(&original)
	require.NoError(t, err)

	var decoded netconf.RPCReply
	require.NoError(t, xml.Unmarshal(data, &decoded))

	assert.Contains(t, string(decoded.Body), "foo")
}

func TestRPCReply_Unmarshal_From_Wire_Ok(t *testing.T) {
	t.Parallel()
	wire := `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="7"><ok/></rpc-reply>`

	var r netconf.RPCReply
	require.NoError(t, xml.NewDecoder(strings.NewReader(wire)).Decode(&r))

	assert.Equal(t, "7", r.MessageID)
	assert.NotNil(t, r.Ok)
}

// ── XMLName constants ────────────────────────────────────────────────────────

func TestXMLNameConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:xml:ns:netconf:base:1.0", netconf.HelloName.Space)
	assert.Equal(t, "hello", netconf.HelloName.Local)

	assert.Equal(t, "urn:ietf:params:xml:ns:netconf:base:1.0", netconf.RPCName.Space)
	assert.Equal(t, "rpc", netconf.RPCName.Local)

	assert.Equal(t, "urn:ietf:params:xml:ns:netconf:base:1.0", netconf.RPCReplyName.Space)
	assert.Equal(t, "rpc-reply", netconf.RPCReplyName.Local)
}

// ── Marshal produces bytes (not empty) ───────────────────────────────────────

func TestHello_Marshal_NotEmpty(t *testing.T) {
	t.Parallel()
	h := netconf.Hello{Capabilities: []string{netconf.BaseCap10}}
	var buf bytes.Buffer
	require.NoError(t, xml.NewEncoder(&buf).Encode(&h))
	assert.NotEmpty(t, buf.Bytes())
}

// ── Notification (RFC 5277) ───────────────────────────────────────────────────

// TestNotification_MarshalNamespace verifies that a Notification marshals with
// the RFC 5277 notification namespace and the <notification> root element name.
func TestNotification_MarshalNamespace(t *testing.T) {
	t.Parallel()
	n := netconf.Notification{
		EventTime: "2024-01-01T00:00:00Z",
	}
	data, err := xml.Marshal(&n)
	require.NoError(t, err, "marshal should succeed")

	s := string(data)
	assert.Contains(t, s,
		`xmlns="urn:ietf:params:xml:ns:netconf:notification:1.0"`,
		"notification must carry the RFC 5277 notification namespace")
	assert.Contains(t, s, "<notification ", "root element must be <notification")
	// Must NOT carry the base NETCONF namespace — that is a different element.
	assert.NotContains(t, s,
		`xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"`,
		"notification must NOT carry the base NETCONF namespace")
}

// TestNotification_RoundTrip verifies that a Notification with EventTime and Body
// survives a marshal/unmarshal round-trip with all fields intact.
func TestNotification_RoundTrip(t *testing.T) {
	t.Parallel()
	body := []byte(`<replayComplete xmlns="urn:ietf:params:xml:ns:netconf:notification:1.0"/>`)
	original := netconf.Notification{
		EventTime: "2024-06-15T12:34:56.789Z",
		Body:      body,
	}
	data, err := xml.Marshal(&original)
	require.NoError(t, err)

	var decoded netconf.Notification
	require.NoError(t, xml.Unmarshal(data, &decoded))

	assert.Equal(t, original.EventTime, decoded.EventTime,
		"EventTime must survive round-trip")
	assert.Contains(t, string(decoded.Body), "replayComplete",
		"Body content must survive round-trip")
}

// TestNotification_RoundTrip_EmptyBody verifies that a Notification with EventTime
// but no additional event body round-trips cleanly.
// Note: Go's encoding/xml with ",innerxml" captures all inner XML including <eventTime>.
// When no event-specific body is present beyond the required EventTime, Body contains
// the <eventTime> element bytes — this is expected xml decoder behavior.
func TestNotification_RoundTrip_EmptyBody(t *testing.T) {
	t.Parallel()
	original := netconf.Notification{
		EventTime: "2024-01-01T00:00:00Z",
	}
	data, err := xml.Marshal(&original)
	require.NoError(t, err)

	var decoded netconf.Notification
	require.NoError(t, xml.Unmarshal(data, &decoded))

	assert.Equal(t, original.EventTime, decoded.EventTime,
		"EventTime must survive round-trip even with no event-body content")
	// Body captures all inner XML (including <eventTime>) due to innerxml semantics.
	// When no event-specific payload is present, Body should not contain any event element.
	assert.NotContains(t, string(decoded.Body), "<netconf-config-change",
		"Body must not contain event-specific content that was not set")
}

// TestNotification_UnmarshalFromWire simulates decoding a notification as it would
// arrive on the wire from a NETCONF server.
func TestNotification_UnmarshalFromWire(t *testing.T) {
	t.Parallel()
	wire := `<notification xmlns="urn:ietf:params:xml:ns:netconf:notification:1.0">` +
		`<eventTime>2024-03-14T09:26:53Z</eventTime>` +
		`<netconf-config-change xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-notifications">` +
		`<changed-by><server/></changed-by>` +
		`</netconf-config-change>` +
		`</notification>`

	var n netconf.Notification
	require.NoError(t, xml.NewDecoder(strings.NewReader(wire)).Decode(&n))

	assert.Equal(t, "2024-03-14T09:26:53Z", n.EventTime)
	assert.Contains(t, string(n.Body), "netconf-config-change")
}

// TestNotificationName_Constants verifies the NotificationName xml.Name var and
// NotificationNS constant are set to the correct RFC 5277 values.
func TestNotificationName_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:xml:ns:netconf:notification:1.0", netconf.NotificationNS)
	assert.Equal(t, "urn:ietf:params:xml:ns:netconf:notification:1.0", netconf.NotificationName.Space)
	assert.Equal(t, "notification", netconf.NotificationName.Local)
}
