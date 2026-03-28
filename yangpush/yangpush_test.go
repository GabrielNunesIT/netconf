package yangpush_test

import (
	"encoding/xml"
	"testing"

	"github.com/GabrielNunesIT/netconf/yangpush"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestYangPush_NamespaceConstant verifies the YangPushNS and CapabilityURI
// constants match the RFC 8641 YANG module namespace.
func TestYangPush_NamespaceConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "urn:ietf:params:xml:ns:yang:ietf-yang-push", yangpush.YangPushNS)
	assert.Equal(t, yangpush.YangPushNS, yangpush.CapabilityURI,
		"CapabilityURI must equal YangPushNS")
}

// TestPeriodicTrigger_RoundTrip verifies PeriodicTrigger with Period and
// AnchorTime round-trips through xml.Marshal/Unmarshal correctly.
func TestPeriodicTrigger_RoundTrip(t *testing.T) {
	t.Parallel()
	original := yangpush.PeriodicTrigger{
		Period:     1000, // 10 seconds
		AnchorTime: "2026-01-01T00:00:00Z",
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled PeriodicTrigger:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-yang-push`)
	assert.Contains(t, xmlStr, `periodic`)
	assert.Contains(t, xmlStr, `<period>1000</period>`)
	assert.Contains(t, xmlStr, `<anchor-time>2026-01-01T00:00:00Z</anchor-time>`)

	var decoded yangpush.PeriodicTrigger
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, uint64(1000), decoded.Period)
	assert.Equal(t, "2026-01-01T00:00:00Z", decoded.AnchorTime)
}

// TestOnChangeTrigger_RoundTrip verifies OnChangeTrigger with all fields set
// round-trips correctly.
func TestOnChangeTrigger_RoundTrip(t *testing.T) {
	t.Parallel()
	original := yangpush.OnChangeTrigger{
		DampeningPeriod: 500,
		SyncOnStart:     &struct{}{},
		ExcludedChanges: []string{"delete", "move"},
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled OnChangeTrigger:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `on-change`)
	assert.Contains(t, xmlStr, `<dampening-period>500</dampening-period>`)
	assert.Contains(t, xmlStr, `<sync-on-start>`)
	assert.Contains(t, xmlStr, `<excluded-change>delete</excluded-change>`)
	assert.Contains(t, xmlStr, `<excluded-change>move</excluded-change>`)

	var decoded yangpush.OnChangeTrigger
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, uint64(500), decoded.DampeningPeriod)
	assert.NotNil(t, decoded.SyncOnStart)
	assert.Equal(t, []string{"delete", "move"}, decoded.ExcludedChanges)
}

// TestPushUpdate_RoundTrip verifies PushUpdate with all fields, including
// an Updates body that must remain as raw XML (not escaped text).
func TestPushUpdate_RoundTrip(t *testing.T) {
	t.Parallel()
	updatesBody := []byte(`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"><interface><name>eth0</name><oper-status>up</oper-status></interface></interfaces>`)

	original := yangpush.PushUpdate{
		ID:              1,
		ObservationTime: "2026-01-01T12:00:00Z",
		Datastore:       "urn:ietf:params:netconf:datastore:operational",
		Updates:         updatesBody,
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled PushUpdate:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-yang-push`)
	assert.Contains(t, xmlStr, `push-update`)
	assert.Contains(t, xmlStr, `<id>1</id>`)
	assert.Contains(t, xmlStr, `<observation-time>2026-01-01T12:00:00Z</observation-time>`)
	assert.Contains(t, xmlStr, `urn:ietf:params:netconf:datastore:operational`)
	// Updates body must be raw XML, not escaped.
	assert.Contains(t, xmlStr, `<interfaces`, "Updates body must be raw XML, not escaped entities")
	assert.Contains(t, xmlStr, `eth0`)

	var decoded yangpush.PushUpdate
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, uint32(1), decoded.ID)
	assert.Equal(t, "2026-01-01T12:00:00Z", decoded.ObservationTime)
	assert.Equal(t, "urn:ietf:params:netconf:datastore:operational", decoded.Datastore)
}

// TestPushChangeUpdate_RoundTrip verifies PushChangeUpdate with a changes body.
func TestPushChangeUpdate_RoundTrip(t *testing.T) {
	t.Parallel()
	changesBody := []byte(`<edit><target>/interfaces/interface[name='eth1']</target><operation>create</operation></edit>`)

	original := yangpush.PushChangeUpdate{
		ID:              3,
		ObservationTime: "2026-01-01T12:01:00Z",
		Datastore:       "urn:ietf:params:netconf:datastore:running",
		Changes:         changesBody,
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled PushChangeUpdate:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `push-change-update`)
	assert.Contains(t, xmlStr, `<id>3</id>`)
	// Changes body must be raw XML.
	assert.Contains(t, xmlStr, `<edit>`, "Changes body must be raw XML")

	var decoded yangpush.PushChangeUpdate
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, uint32(3), decoded.ID)
	assert.Equal(t, "urn:ietf:params:netconf:datastore:running", decoded.Datastore)
}

// TestOmitEmptyFields verifies that optional fields on trigger types are
// omitted from marshaled XML when not set.
func TestOmitEmptyFields(t *testing.T) {
	t.Parallel()
	// PeriodicTrigger with only Period.
	pt := yangpush.PeriodicTrigger{Period: 100}
	b, err := xml.Marshal(pt)
	require.NoError(t, err)
	t.Logf("marshaled minimal PeriodicTrigger:\n%s", b)
	assert.NotContains(t, string(b), `<anchor-time>`)

	// OnChangeTrigger with no fields set.
	oc := yangpush.OnChangeTrigger{}
	b2, err := xml.Marshal(oc)
	require.NoError(t, err)
	t.Logf("marshaled empty OnChangeTrigger:\n%s", b2)
	assert.NotContains(t, string(b2), `<dampening-period>`)
	assert.NotContains(t, string(b2), `<sync-on-start>`)
	assert.NotContains(t, string(b2), `<excluded-change>`)

	// PushUpdate with only ID.
	pu := yangpush.PushUpdate{ID: 1}
	b3, err := xml.Marshal(pu)
	require.NoError(t, err)
	t.Logf("marshaled minimal PushUpdate:\n%s", b3)
	assert.NotContains(t, string(b3), `<observation-time>`)
	assert.NotContains(t, string(b3), `<datastore>`)
}
