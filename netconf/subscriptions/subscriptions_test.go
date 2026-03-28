package subscriptions_test

import (
	"encoding/xml"
	"testing"

	"github.com/GabrielNunesIT/netconf/netconf/subscriptions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubscriptions_NamespaceConstants verifies the namespace and capability
// URI constants match the values specified in RFC 8639 and RFC 8640.
func TestSubscriptions_NamespaceConstants(t *testing.T) {
	assert.Equal(t, "urn:ietf:params:xml:ns:yang:ietf-subscriptions", subscriptions.SubscriptionsNS)
	assert.Equal(t, subscriptions.SubscriptionsNS, subscriptions.CapabilityURI,
		"CapabilityURI must equal SubscriptionsNS")
	assert.Equal(t, "urn:ietf:params:xml:ns:yang:ietf-netconf-subscriptions", subscriptions.NetconfSubscriptionsNS)
	assert.Equal(t, subscriptions.NetconfSubscriptionsNS, subscriptions.CapabilityURINetconf,
		"CapabilityURINetconf must equal NetconfSubscriptionsNS")
	assert.NotEqual(t, subscriptions.SubscriptionsNS, subscriptions.NetconfSubscriptionsNS,
		"base and netconf-specific namespaces must be distinct")
}

// TestEstablishSubscriptionRequest_RoundTrip verifies that a full
// EstablishSubscriptionRequest with stream, xpath filter, and stop-time
// round-trips through xml.Marshal/Unmarshal correctly.
func TestEstablishSubscriptionRequest_RoundTrip(t *testing.T) {
	original := subscriptions.EstablishSubscriptionRequest{
		Stream:   "NETCONF",
		Filter:   &subscriptions.FilterSpec{XPathFilter: "/interfaces/interface[enabled='true']"},
		StopTime: "2026-12-31T23:59:59Z",
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled EstablishSubscriptionRequest:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-subscriptions`)
	assert.Contains(t, xmlStr, `establish-subscription`)
	assert.Contains(t, xmlStr, `<stream>NETCONF</stream>`)
	assert.Contains(t, xmlStr, `xpath-filter`)
	assert.Contains(t, xmlStr, `<stop-time>2026-12-31T23:59:59Z</stop-time>`)

	var decoded subscriptions.EstablishSubscriptionRequest
	require.NoError(t, xml.Unmarshal(b, &decoded))

	assert.Equal(t, original.Stream, decoded.Stream)
	assert.Equal(t, original.StopTime, decoded.StopTime)
	require.NotNil(t, decoded.Filter)
	assert.Equal(t, original.Filter.XPathFilter, decoded.Filter.XPathFilter)
}

// TestEstablishSubscriptionReply_RoundTrip verifies that an
// EstablishSubscriptionReply with a subscription ID round-trips correctly.
func TestEstablishSubscriptionReply_RoundTrip(t *testing.T) {
	original := subscriptions.EstablishSubscriptionReply{
		ID: 42,
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled EstablishSubscriptionReply:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-subscriptions`)
	assert.Contains(t, xmlStr, `establish-subscription-reply`)
	assert.Contains(t, xmlStr, `<id>42</id>`)

	var decoded subscriptions.EstablishSubscriptionReply
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(42), decoded.ID)
}

// TestModifySubscriptionRequest_RoundTrip verifies that a
// ModifySubscriptionRequest with ID and new filter round-trips correctly.
func TestModifySubscriptionRequest_RoundTrip(t *testing.T) {
	original := subscriptions.ModifySubscriptionRequest{
		ID:     7,
		Filter: &subscriptions.FilterSpec{XPathFilter: "/syslog/entries"},
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled ModifySubscriptionRequest:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `modify-subscription`)
	assert.Contains(t, xmlStr, `<id>7</id>`)
	assert.Contains(t, xmlStr, `xpath-filter`)

	var decoded subscriptions.ModifySubscriptionRequest
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(7), decoded.ID)
	require.NotNil(t, decoded.Filter)
	assert.Equal(t, original.Filter.XPathFilter, decoded.Filter.XPathFilter)
}

// TestDeleteSubscription_RoundTrip verifies DeleteSubscription round-trip.
func TestDeleteSubscription_RoundTrip(t *testing.T) {
	original := subscriptions.DeleteSubscription{ID: 99}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled DeleteSubscription:\n%s", b)

	assert.Contains(t, string(b), `<id>99</id>`)
	assert.Contains(t, string(b), `delete-subscription`)

	var decoded subscriptions.DeleteSubscription
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(99), decoded.ID)
}

// TestKillSubscription_RoundTrip verifies KillSubscription round-trip with Reason.
func TestKillSubscription_RoundTrip(t *testing.T) {
	original := subscriptions.KillSubscription{
		ID:     15,
		Reason: "admin request",
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled KillSubscription:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `kill-subscription`)
	assert.Contains(t, xmlStr, `<id>15</id>`)
	assert.Contains(t, xmlStr, `<reason>admin request</reason>`)

	var decoded subscriptions.KillSubscription
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(15), decoded.ID)
	assert.Equal(t, "admin request", decoded.Reason)
}

// TestSubscriptionStarted_RoundTrip verifies the subscription-started
// notification body round-trips correctly.
func TestSubscriptionStarted_RoundTrip(t *testing.T) {
	original := subscriptions.SubscriptionStarted{
		ID:     1,
		Stream: "NETCONF",
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled SubscriptionStarted:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-subscriptions`)
	assert.Contains(t, xmlStr, `subscription-started`)
	assert.Contains(t, xmlStr, `<id>1</id>`)
	assert.Contains(t, xmlStr, `<stream>NETCONF</stream>`)

	var decoded subscriptions.SubscriptionStarted
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(1), decoded.ID)
	assert.Equal(t, "NETCONF", decoded.Stream)
}

// TestSubscriptionTerminated_RoundTrip verifies the subscription-terminated
// notification body with reason round-trips correctly.
func TestSubscriptionTerminated_RoundTrip(t *testing.T) {
	original := subscriptions.SubscriptionTerminated{
		ID:     3,
		Reason: "filter-unavailable",
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled SubscriptionTerminated:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `subscription-terminated`)
	assert.Contains(t, xmlStr, `<id>3</id>`)
	assert.Contains(t, xmlStr, `<reason>filter-unavailable</reason>`)

	var decoded subscriptions.SubscriptionTerminated
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(3), decoded.ID)
	assert.Equal(t, "filter-unavailable", decoded.Reason)
}

// TestSubscriptionModified_RoundTrip verifies the subscription-modified
// notification body round-trips correctly.
func TestSubscriptionModified_RoundTrip(t *testing.T) {
	original := subscriptions.SubscriptionModified{ID: 7}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled SubscriptionModified:\n%s", b)

	assert.Contains(t, string(b), `subscription-modified`)
	assert.Contains(t, string(b), `<id>7</id>`)

	var decoded subscriptions.SubscriptionModified
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(7), decoded.ID)
}

// TestSubscriptionKilled_RoundTrip verifies the subscription-killed
// notification body with reason round-trips correctly.
func TestSubscriptionKilled_RoundTrip(t *testing.T) {
	original := subscriptions.SubscriptionKilled{
		ID:     5,
		Reason: "operator forced",
	}

	b, err := xml.Marshal(original)
	require.NoError(t, err)
	t.Logf("marshaled SubscriptionKilled:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `subscription-killed`)
	assert.Contains(t, xmlStr, `<id>5</id>`)
	assert.Contains(t, xmlStr, `<reason>operator forced</reason>`)

	var decoded subscriptions.SubscriptionKilled
	require.NoError(t, xml.Unmarshal(b, &decoded))
	assert.Equal(t, subscriptions.SubscriptionID(5), decoded.ID)
	assert.Equal(t, "operator forced", decoded.Reason)
}

// TestOmitEmptyFields verifies that optional fields with zero/nil values
// are omitted from marshaled XML.
func TestOmitEmptyFields(t *testing.T) {
	req := subscriptions.EstablishSubscriptionRequest{}

	b, err := xml.Marshal(req)
	require.NoError(t, err)
	xmlStr := string(b)
	t.Logf("marshaled sparse EstablishSubscriptionRequest:\n%s", xmlStr)

	assert.NotContains(t, xmlStr, `<stream>`)
	assert.NotContains(t, xmlStr, `<filter>`)
	assert.NotContains(t, xmlStr, `<stop-time>`)
	assert.NotContains(t, xmlStr, `<period>`)
	assert.NotContains(t, xmlStr, `<dscp>`)

	// Only the root element with namespace should appear.
	assert.Contains(t, xmlStr, `urn:ietf:params:xml:ns:yang:ietf-subscriptions`)
	assert.Contains(t, xmlStr, `establish-subscription`)
}

// TestFilterSpec_SubtreeFilter verifies that SubtreeFilter emits raw XML bytes
// verbatim via innerxml encoding inside the <subtree-filter> element.
func TestFilterSpec_SubtreeFilter(t *testing.T) {
	innerXML := []byte(`<interfaces xmlns="urn:ietf:params:xml:ns:yang:ietf-interfaces"/>`)

	req := subscriptions.EstablishSubscriptionRequest{
		Filter: &subscriptions.FilterSpec{
			SubtreeFilter: &subscriptions.SubtreeFilterContent{Content: innerXML},
		},
	}

	b, err := xml.Marshal(req)
	require.NoError(t, err)
	t.Logf("marshaled with subtree filter:\n%s", b)

	xmlStr := string(b)
	assert.Contains(t, xmlStr, `subtree-filter`)
	assert.Contains(t, xmlStr, `ietf-interfaces`)
	// Must appear as raw XML, not as escaped entities.
	assert.Contains(t, xmlStr, `<interfaces`, "subtree content must be raw XML, not escaped")
}
